package dm

import (
	"encoding/binary"
	"os"
	"sort"
)

type segmentPageStats struct {
	files map[dataFileKey]map[uint32]bool
}

func inferDictionaryTableSegments(controlPath string, controlDULPath string, dataDir string, pageSize uint32, extentSize uint32, tables map[uint32]dictionaryObject, indexObjects map[uint32]dictionaryObject, indexes map[uint32]indexDef, partitionsByTable map[uint32][]PartitionInfo, tableList []DictionaryTable) map[uint32]tableSegment {
	if pageSize == 0 || len(tableList) == 0 {
		return nil
	}
	tableSet := make(map[uint32]bool, len(tableList))
	for _, table := range tableList {
		if table.Temporary {
			continue
		}
		tableSet[table.ID] = true
	}
	if len(tableSet) == 0 {
		return nil
	}
	assistToTables := dictionaryAssistIDsByTable(tableSet, tables, indexObjects, indexes, partitionsByTable)
	if len(assistToTables) == 0 {
		return nil
	}
	refs, err := resolveDataFiles(controlPath, controlDULPath, dataDir)
	if err != nil || len(refs) == 0 {
		return nil
	}
	stats := make(map[uint32]*segmentPageStats)
	for _, ref := range refs {
		raw, err := os.ReadFile(ref.path)
		if err != nil {
			continue
		}
		pageCount := len(raw) / int(pageSize)
		for pageNo := 0; pageNo < pageCount; pageNo++ {
			start := pageNo * int(pageSize)
			page := raw[start : start+int(pageSize)]
			if !isProbableSegmentAssistPage(page, pageSize) {
				continue
			}
			assistID := binary.LittleEndian.Uint32(page[dataPageAssistIndexOff:])
			tableIDs := assistToTables[assistID]
			if len(tableIDs) == 0 {
				continue
			}
			for _, tableID := range tableIDs {
				stat := stats[tableID]
				if stat == nil {
					stat = &segmentPageStats{files: make(map[dataFileKey]map[uint32]bool)}
					stats[tableID] = stat
				}
				pages := stat.files[ref.key]
				if pages == nil {
					pages = make(map[uint32]bool)
					stat.files[ref.key] = pages
				}
				pages[uint32(pageNo)] = true
			}
		}
	}
	result := make(map[uint32]tableSegment)
	for tableID, stat := range stats {
		if seg, ok := stat.bestSegment(pageSize, extentSize); ok {
			result[tableID] = seg
		}
	}
	return result
}

func dictionaryAssistIDsByTable(tableSet map[uint32]bool, tables map[uint32]dictionaryObject, indexObjects map[uint32]dictionaryObject, indexes map[uint32]indexDef, partitionsByTable map[uint32][]PartitionInfo) map[uint32][]uint32 {
	result := make(map[uint32][]uint32)
	assistByParentID := assistIndexesByParentID(tables, indexObjects, indexes)
	for tableID := range tableSet {
		addDictionaryAssistIDs(result, tableID, tableID, assistByParentID, indexObjects)
		for _, part := range partitionsByTable[tableID] {
			addDictionaryAssistIDs(result, tableID, part.PartTableID, assistByParentID, indexObjects)
		}
	}
	return result
}

func addDictionaryAssistIDs(result map[uint32][]uint32, baseTableID uint32, physicalTableID uint32, assistByParentID map[uint32][]indexDef, indexObjects map[uint32]dictionaryObject) {
	seen := make(map[uint32]bool)
	add := func(assistID uint32) {
		if assistID == 0 || seen[assistID] {
			return
		}
		result[assistID] = append(result[assistID], baseTableID)
		seen[assistID] = true
	}
	add(tableDataAssistID(physicalTableID))
	for _, storage := range assistByParentID[physicalTableID] {
		add(storage.ID)
	}
	for indexID, obj := range indexObjects {
		if uint32(obj.ParentID) == physicalTableID && isAutoHiddenIndexObject(obj) {
			add(indexID)
		}
	}
}

func isProbableSegmentAssistPage(page []byte, pageSize uint32) bool {
	if len(page) < int(pageSize) || len(page) < dataPageAssistIndexOff+4 {
		return false
	}
	assistID := binary.LittleEndian.Uint32(page[dataPageAssistIndexOff:])
	if assistID == 0 {
		return false
	}
	nSlot := binary.LittleEndian.Uint16(page[dataPageSlotCountOff:])
	freeEnd := binary.LittleEndian.Uint16(page[dataPageFreeEndOff:])
	nRec := binary.LittleEndian.Uint16(page[dataPageRecordCountOff:])
	if nSlot >= 2048 || nRec > nSlot {
		return false
	}
	return freeEnd >= 0x52 && uint32(freeEnd) <= pageSize
}

func (s *segmentPageStats) bestSegment(pageSize uint32, extentSize uint32) (tableSegment, bool) {
	if s == nil || len(s.files) == 0 {
		return tableSegment{}, false
	}
	var bestKey dataFileKey
	var bestPages map[uint32]bool
	for key, pages := range s.files {
		if len(pages) == 0 {
			continue
		}
		if bestPages == nil || len(pages) > len(bestPages) || (len(pages) == len(bestPages) && lessDataFileKey(key, bestKey)) {
			bestKey = key
			bestPages = pages
		}
	}
	if len(bestPages) == 0 {
		return tableSegment{}, false
	}
	extentStarts := segmentExtentStarts(bestPages, extentSize)
	if len(extentStarts) == 0 {
		return tableSegment{}, false
	}
	sort.Slice(extentStarts, func(i, j int) bool { return extentStarts[i] < extentStarts[j] })
	blocksPerExtent := extentSize
	if blocksPerExtent == 0 {
		blocksPerExtent = uint32(len(bestPages))
	}
	blocks := uint32(len(extentStarts)) * blocksPerExtent
	return tableSegment{
		fileID:       bestKey.fileID,
		headerPage:   extentStarts[0],
		blocks:       blocks,
		extents:      uint32(len(extentStarts)),
		bytes:        uint64(blocks) * uint64(pageSize),
		tablespaceID: bestKey.groupID,
	}, true
}

func segmentExtentStarts(pages map[uint32]bool, extentSize uint32) []uint32 {
	seen := make(map[uint32]bool)
	var starts []uint32
	for pageNo := range pages {
		start := pageNo
		if extentSize > 0 {
			start = (pageNo / extentSize) * extentSize
		}
		if seen[start] {
			continue
		}
		seen[start] = true
		starts = append(starts, start)
	}
	return starts
}

func lessDataFileKey(left dataFileKey, right dataFileKey) bool {
	if left.groupID != right.groupID {
		return left.groupID < right.groupID
	}
	return left.fileID < right.fileID
}
