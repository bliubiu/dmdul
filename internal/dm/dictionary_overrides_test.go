package dm

import "testing"

func TestDictionaryOverridesReplaceTableColumnsAndStorage(t *testing.T) {
	dict := &DictionaryInfo{
		Users: []DictionaryUser{
			{Name: "FIXED_OWNER"},
		},
		Tables: []DictionaryTable{
			{ID: 100, Owner: "FIXED_OWNER", Name: "FIXED_TABLE", Tablespace: "MAIN", GroupID: 4, Storage: heapStorageOrg},
		},
		Columns: []DictionaryColumn{
			{TableID: 100, ColID: 1, Name: "FIXED_ID", DataType: "BIGINT", Nullable: "N"},
			{TableID: 100, ColID: 2, Name: "FIXED_NAME", DataType: "VARCHAR2", Length: 50, Nullable: "Y", Default: "'N/A'"},
		},
	}
	users := map[uint32]dictionaryObject{}
	tables := map[uint32]dictionaryObject{
		100: {ID: 100, Owner: "BROKEN", Name: "BAD_TABLE", Type: "SCHOBJ", Subtype: "UTAB", Info1: 0},
		200: {ID: 200, Owner: "RAW_ONLY", Name: "RAW_TABLE", Type: "SCHOBJ", Subtype: "UTAB"},
	}
	tablespaces := map[uint32]string{4: "MAIN"}

	applyDictionaryUserOverrides(dict, users)
	dictionaryTables := applyDictionaryTableOverrides(dict, tables, tablespaces)
	storage := map[uint32]indexDef{}
	applyDictionaryTableStorage(dictionaryTables, storage, tablespaces)
	columnsByTable, columnsByTableColID, count, ok := dictionaryColumnMaps(dict, dictionaryTables, tables, newOwnerMatcher("all"), newTableNameMatcher("all"), tableNameMatcher{})

	if !ok || count != 2 {
		t.Fatalf("dictionaryColumnMaps ok/count = %v/%d", ok, count)
	}
	if got := users[0xF0000000].Name; got != "FIXED_OWNER" {
		t.Fatalf("synthetic user = %q, want FIXED_OWNER", got)
	}
	if got := tables[100]; got.Owner != "FIXED_OWNER" || got.Name != "FIXED_TABLE" || got.isIOTTable() {
		t.Fatalf("table override failed: %+v", got)
	}
	if got := storage[100].GroupID; got != 4 {
		t.Fatalf("storage group = %d, want 4", got)
	}
	if len(columnsByTable[100]) != 2 || columnsByTable[100][0].Name != "FIXED_ID" || columnsByTable[100][1].Default != "'N/A'" {
		t.Fatalf("columnsByTable = %+v", columnsByTable[100])
	}
	if _, ok := columnsByTable[200]; ok {
		t.Fatalf("raw-only table should not get dictionary columns: %+v", columnsByTable[200])
	}
	if got := columnsByTableColID[tableColKey{tableID: 100, colID: 2}].Name; got != "FIXED_NAME" {
		t.Fatalf("columnsByTableColID col 2 = %q", got)
	}
}

func TestDictionaryColumnMapsHonorsOwnerTableAndExcludeFilters(t *testing.T) {
	dict := &DictionaryInfo{
		Tables: []DictionaryTable{
			{ID: 10, Owner: "A", Name: "T1"},
			{ID: 20, Owner: "A", Name: "T2"},
			{ID: 30, Owner: "B", Name: "T3"},
		},
		Columns: []DictionaryColumn{
			{TableID: 10, ColID: 1, Name: "C1", DataType: "INT"},
			{TableID: 20, ColID: 1, Name: "C2", DataType: "INT"},
			{TableID: 30, ColID: 1, Name: "C3", DataType: "INT"},
		},
	}
	tables := map[uint32]dictionaryObject{}
	dictionaryTables := applyDictionaryTableOverrides(dict, tables, nil)

	columnsByTable, columnsByTableColID, count, ok := dictionaryColumnMaps(
		dict,
		dictionaryTables,
		tables,
		newOwnerMatcher("A"),
		newTableNameMatcher("all"),
		newTableNameMatcher("A.T2"),
	)

	if !ok || count != 1 {
		t.Fatalf("dictionaryColumnMaps ok/count = %v/%d", ok, count)
	}
	if len(columnsByTable[10]) != 1 {
		t.Fatalf("table 10 columns = %+v", columnsByTable[10])
	}
	if _, ok := columnsByTable[20]; ok {
		t.Fatalf("excluded table should not be selected: %+v", columnsByTable[20])
	}
	if _, ok := columnsByTable[30]; ok {
		t.Fatalf("other owner table should not be selected: %+v", columnsByTable[30])
	}
	if _, ok := columnsByTableColID[tableColKey{tableID: 20, colID: 1}]; ok {
		t.Fatalf("excluded table should not be in col-id map")
	}
}
