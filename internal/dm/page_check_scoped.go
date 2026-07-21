package dm

// Storage-scoped page diagnosis: uses a recovered dictionary to attribute bad
// pages to owner.table, walk B-tree leaf chains for break/cycle detection, and
// check dictionary self-consistency. Requires a loaded dictionary; the plain
// whole-file page scan runs without one.

import (
	"fmt"
	"sort"
	"strings"
)

// pageAttribution maps a page to the table that owns it, first by the page's
// own storage id, then by falling back to the table segment page range for
// pages whose storage id was wiped (e.g. zeroed pages).
type pageAttribution struct {
	byStorage map[uint32]string    // storage/assist id -> "OWNER.TABLE"
	segments  []attributionSegment // segment ranges for fallback
}

type attributionSegment struct {
	owner     string
	table     string
	groupID   uint32
	fileID    int16
	startPage uint32
	endPage   uint32 // exclusive
}

func newPageAttribution(dict *DictionaryInfo) *pageAttribution {
	a := &pageAttribution{byStorage: make(map[uint32]string)}
	for _, table := range dict.Tables {
		label := table.Owner + "." + table.Name
		if table.StorageID != 0 {
			a.byStorage[table.StorageID] = label
		}
		for _, assist := range table.AssistIDs {
			if assist != 0 {
				if _, exists := a.byStorage[assist]; !exists {
					a.byStorage[assist] = label
				}
			}
		}
		if table.Blocks > 0 && table.HeaderFile >= 0 {
			a.segments = append(a.segments, attributionSegment{
				owner:     table.Owner,
				table:     table.Name,
				groupID:   table.GroupID,
				fileID:    table.HeaderFile,
				startPage: table.HeaderBlock,
				endPage:   table.HeaderBlock + table.Blocks,
			})
		}
	}
	return a
}

func (a *pageAttribution) attribute(bad *BadPage) (owner string, table string) {
	if a == nil {
		return "", ""
	}
	if bad.StorageID != 0 {
		if label, ok := a.byStorage[bad.StorageID]; ok {
			return splitOwnerTable(label)
		}
	}
	// Segment-range fallback for pages whose storage id is unreadable.
	for i := range a.segments {
		seg := &a.segments[i]
		if seg.groupID == bad.GroupID && seg.fileID == bad.FileID &&
			bad.PageNo >= seg.startPage && bad.PageNo < seg.endPage {
			return seg.owner, seg.table
		}
	}
	return "", ""
}

func splitOwnerTable(label string) (string, string) {
	if i := strings.IndexByte(label, '.'); i >= 0 {
		return label[:i], label[i+1:]
	}
	return "", label
}

// checkLeafChains walks each table's B-tree leaf chain and reports breaks or
// cycles. buildStoragePagePlanDetailed already validates root identity, page
// kind, storage id, chain links and cycles, returning a reason on failure.
func checkLeafChains(dict *DictionaryInfo, files []dataFileRef, pageSize uint32) []ChainIssue {
	if dict == nil || len(files) == 0 {
		return nil
	}
	// Only tables whose storage root actually lives in a present data file can
	// be walked. Tables in tablespaces that were not provided (a common case
	// when checking one file) would otherwise report a spurious "cannot read
	// root" — a missing file is not corruption.
	available := make(map[dataFileKey]bool, len(files))
	for _, file := range files {
		available[file.key] = true
	}
	cache := newDataFilePageCache(files, pageSize)
	var issues []ChainIssue
	for _, table := range dict.Tables {
		if table.Temporary || table.StorageID == 0 || table.RootFile < 0 {
			continue
		}
		rootKey := dataFileKey{groupID: table.GroupID, fileID: table.RootFile}
		if !available[rootKey] {
			continue
		}
		storage := indexDef{
			ID:       table.StorageID,
			GroupID:  uint16(table.GroupID),
			RootFile: table.RootFile,
			RootPage: int32(table.RootPage),
		}
		plan, reason := buildStoragePagePlanDetailed(storage, cache)
		// Only report a broken/cyclic chain whose ROOT is valid — the walk got
		// past the root and then hit a bad leaf/internal page. A root-level
		// failure (unreadable root, root identity or storage_id mismatch) is far
		// more likely a stale dictionary root pointer than page corruption, and
		// the unload path already recovers those via storage/segment fallback;
		// reporting them here floods the output with dictionary-vs-data drift.
		if reason != "" && len(plan) == 0 && isLeafChainBreakReason(reason) {
			issues = append(issues, ChainIssue{
				Owner:     table.Owner,
				Table:     table.Name,
				StorageID: table.StorageID,
				RootFile:  table.RootFile,
				RootPage:  table.RootPage,
				Reason:    reason,
			})
		}
	}
	sort.Slice(issues, func(i, j int) bool {
		if issues[i].Owner != issues[j].Owner {
			return issues[i].Owner < issues[j].Owner
		}
		return issues[i].Table < issues[j].Table
	})
	return issues
}

// isLeafChainBreakReason reports whether a page-plan failure reason describes a
// break in the leaf/internal chain (root was valid) rather than a root-level
// problem (stale pointer, unreadable/mismatched root). Only chain breaks are
// high-confidence page corruption.
func isLeafChainBreakReason(reason string) bool {
	if reason == "" {
		return false
	}
	if strings.Contains(reason, "storage root metadata") ||
		strings.Contains(reason, "cannot read root page") ||
		strings.Contains(reason, "root page identity") ||
		strings.Contains(reason, "root page storage_id") ||
		strings.Contains(reason, "unsupported root page kind") {
		return false
	}
	return true
}

// checkDictionaryConsistency finds impossible catalog entries: duplicate table
// ids, columns whose table is absent, and tables whose owner is unknown. This
// serves the same goal as dmdbchk's object-id validity check (detecting
// corrupt catalog references) without the DM id-reserve page format.
func checkDictionaryConsistency(dict *DictionaryInfo) []DictIssue {
	if dict == nil {
		return nil
	}
	var issues []DictIssue

	tableIDs := make(map[uint32]string, len(dict.Tables))
	for _, table := range dict.Tables {
		label := table.Owner + "." + table.Name
		if prev, ok := tableIDs[table.ID]; ok {
			issues = append(issues, DictIssue{
				Category: "duplicate-table-id",
				Detail:   fmt.Sprintf("table id %d used by both %s and %s", table.ID, prev, label),
			})
			continue
		}
		tableIDs[table.ID] = label
	}

	owners := make(map[string]bool, len(dict.Users))
	for _, user := range dict.Users {
		owners[strings.ToUpper(user.Name)] = true
	}
	for _, schema := range dict.Schemas {
		owners[strings.ToUpper(schema.Name)] = true
	}
	if len(owners) > 0 {
		for _, table := range dict.Tables {
			if !owners[strings.ToUpper(table.Owner)] {
				issues = append(issues, DictIssue{
					Category: "orphan-table-owner",
					Detail:   fmt.Sprintf("table %s.%s (id %d) has no matching user/schema", table.Owner, table.Name, table.ID),
				})
			}
		}
	}

	// Columns whose table id is absent from the table set.
	missingTables := make(map[uint32]int)
	for _, col := range dict.Columns {
		if _, ok := tableIDs[col.TableID]; !ok {
			missingTables[col.TableID]++
		}
	}
	for tableID, count := range missingTables {
		issues = append(issues, DictIssue{
			Category: "dangling-columns",
			Detail:   fmt.Sprintf("%d column(s) reference missing table id %d", count, tableID),
		})
	}

	sort.Slice(issues, func(i, j int) bool {
		if issues[i].Category != issues[j].Category {
			return issues[i].Category < issues[j].Category
		}
		return issues[i].Detail < issues[j].Detail
	})
	return issues
}
