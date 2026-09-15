package dm

import (
	"fmt"
	"regexp"
	"strings"
)

var materializedViewHeader = regexp.MustCompile(`(?is)^\s*CREATE\s+(?:OR\s+REPLACE\s+)?MATERIALIZED\s+VIEW\s+(?:"(?:[^"]|"")+"|[^\s.]+)\s*\.\s*(?:"(?:[^"]|"")+"|[^\s;]+)\s*;?\s*$`)
var materializedViewPrefix = regexp.MustCompile(`(?is)^\s*CREATE\s+(?:OR\s+REPLACE\s+)?MATERIALIZED\s+VIEW\b`)

func (view DictionaryView) isMaterialized() bool {
	return view.Materialized || materializedViewPrefix.MatchString(view.SQL)
}

// Reconstruct the simple MV forms verified against SYSOBJECTS and native dexp.
// Deferred creation avoids evaluating a query before recovered base data is loaded.
func recoveredMaterializedViewSQL(view DictionaryView) (string, error) {
	if !view.HasMVFlags || view.MVFlags&0x200 == 0 {
		return "", fmt.Errorf("materialized view %s.%s needs bootstrap metadata (mv_flags)", view.Owner, view.Name)
	}
	const known = uint32(0x200000 | 0x200 | 0x20 | 0x40 | 0x80 | 0x100 | 0x400)
	if view.MVFlags & ^known != 0 || strings.TrimSpace(view.QuerySQL) == "" || !materializedViewHeader.MatchString(view.SQL) {
		return "", fmt.Errorf("materialized view %s.%s has an unverified form or missing query; inspect views.tsv", view.Owner, view.Name)
	}
	method := []string{"NEVER", "FAST", "COMPLETE", "FORCE"}[(view.MVFlags>>7)&3]
	if method == "FAST" {
		return "", fmt.Errorf("materialized view %s.%s requires FAST refresh logs which are not recovered", view.Owner, view.Name)
	}
	refresh := "NEVER REFRESH"
	if method != "NEVER" {
		mode := "DEMAND"
		if view.MVFlags&0x20 != 0 {
			mode = "COMMIT"
		}
		refresh = "REFRESH " + method + " ON " + mode
	}
	key := "PRIMARY KEY"
	if view.MVFlags&0x400 != 0 {
		key = "ROWID"
	}
	rewrite := "DISABLE"
	if view.MVFlags&0x40 != 0 {
		rewrite = "ENABLE"
	}
	if method != "NEVER" {
		refresh += " WITH " + key + " " + rewrite + " QUERY REWRITE"
	} else if view.MVFlags&(0x20|0x40|0x400) != 0 {
		return "", fmt.Errorf("materialized view %s.%s has unverified NEVER REFRESH options", view.Owner, view.Name)
	}
	return fmt.Sprintf("CREATE MATERIALIZED VIEW %s.%s\nBUILD DEFERRED\n%s\nAS\n%s;", quoteIdent(view.Owner), quoteIdent(view.Name), refresh, strings.TrimSuffix(strings.TrimSpace(view.QuerySQL), ";")), nil
}

func markMaterializedBackingTables(objects, tables map[uint32]dictionaryObject) {
	backing := make(map[ownerTableKey]bool)
	for _, obj := range objects {
		if obj.Type == "SCHOBJ" && obj.Subtype == "VIEW" && obj.Info1&0x200 != 0 {
			backing[ownerTableKey{owner: obj.Owner, table: "MTAB$_" + obj.Name}] = true
		}
	}
	for id, obj := range tables {
		if obj.Info1&0x200000 != 0 && backing[ownerTableKey{owner: obj.Owner, table: obj.Name}] {
			obj.MaterializedBacking = true
			tables[id] = obj
		}
	}
}
