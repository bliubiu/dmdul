package dm

import (
	"encoding/binary"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestDictionaryTypesSelectionAndTerminators(t *testing.T) {
	if typeDDLSource("CREATE TYPE APP.T AS OBJECT(N INT);\n/\n") != "CREATE TYPE APP.T AS OBJECT(N INT);" {
		t.Fatal("type script terminator not normalized")
	}
	objects := map[uint32]dictionaryObject{
		10: {ID: 10, Owner: "APP", Name: "Z_POINT", Type: "SCHOBJ", Subtype: "CLASS"},
		20: {ID: 20, Owner: "APP", Name: "A_ARRAY", Type: "SCHOBJ", Subtype: "TYPE"},
		30: {ID: 30, Owner: "SYS", Name: "INTERNAL", Type: "SCHOBJ", Subtype: "TYPE"},
		40: {ID: 40, Owner: "OTHER", Name: "T", Type: "SCHOBJ", Subtype: "TYPE"},
		50: {ID: 50, Owner: "APP", Name: "JAVA_CLASS", Type: "SCHOBJ", Subtype: "CLASS"},
	}
	texts := map[uint32]map[uint32]string{
		10: {0: "CREATE OR REPLACE TYPE APP.Z_POINT AS OBJECT(N INT);", 1: "CREATE TYPE BODY APP.Z_POINT AS MEMBER FUNCTION F RETURN INT IS BEGIN RETURN 1; END; END;"},
		20: {0: "CREATE TYPE APP.A_ARRAY AS VARRAY(5) OF APP.Z_POINT;"},
		50: {0: "CREATE CLASS APP.JAVA_CLASS"},
	}
	got := scanDictionaryTypes(objects, texts, newOwnerMatcher("APP"))
	if len(got) != 3 || got[0].ID != 10 || got[1].ID != 20 || got[2].ObjectType != "TYPE BODY" {
		t.Fatalf("types: %+v", got)
	}
	var out strings.Builder
	renderTypes(&out, got, false)
	out.WriteString("CREATE TABLE APP.T(ID INT);\n")
	renderTypes(&out, got, true)
	if strings.Count(out.String(), "\n/\n") != 3 || strings.Index(out.String(), "TYPE BODY") < strings.Index(out.String(), "CREATE TABLE") {
		t.Fatal(out.String())
	}
	if !matchingTypeDefinition(`CREATE TYPE "A"."T""X" AS OBJECT(N INT);`, "A", `T"X`, "TYPE") {
		t.Fatal("quoted identifier")
	}
	if matchingTypeDefinition(`CREATE TYPE APP.T AS OBJECT(N INT);`, "OTHER", "T", "TYPE") {
		t.Fatal("wrong owner accepted")
	}
	if got, ok := dictionaryTypesForDDL(&DictionaryInfo{Types: []DictionaryType{}}, newOwnerMatcher("all")); !ok || len(got) != 0 {
		t.Fatal("empty edited file must be authoritative")
	}
}

func TestExtendedDictionaryPersistenceAndLegacy(t *testing.T) {
	dir := t.TempDir()
	dict := &DictionaryInfo{PageSize: 8192, Types: []DictionaryType{{ID: 10, Owner: "APP", Name: "T", ObjectType: "TYPE", SQL: "CREATE TYPE APP.T AS OBJECT(N INT);"}}, Directories: []DictionaryDirectory{{ID: 30, Name: "D", Path: "/tmp/a'b"}}, Views: []DictionaryView{{ID: 20, Owner: "APP", Name: "MV", Materialized: true, HasMVFlags: true, MVFlags: 0x200300, SQL: "CREATE MATERIALIZED VIEW APP.MV", QuerySQL: "SELECT ID FROM APP.BASE"}}}
	files, err := WriteDictionaryFiles(dir, dict)
	if err != nil {
		t.Fatal(err)
	}
	got, result, err := LoadDictionaryFiles(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got.Types, dict.Types) || !reflect.DeepEqual(got.Directories, dict.Directories) || !got.Views[0].HasMVFlags || got.Views[0].MVFlags != 0x200300 || result.TypeCount != 1 || result.DirectoryCount != 1 {
		t.Fatalf("roundtrip %+v", got)
	}
	if _, _, err := RebuildDictionaryFiles(dir, got); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{files.TypesPath, files.DirectoriesPath} {
		if err := os.Remove(path); err != nil {
			t.Fatal(err)
		}
	}
	if err := writeTSV(files.ViewsPath, []string{"view_id", "owner", "view_name", "valid", "sql", "query_sql"}, [][]string{{"20", "APP", "MV", "Y", "CREATE MATERIALIZED VIEW APP.MV", "SELECT ID FROM APP.BASE"}}); err != nil {
		t.Fatal(err)
	}
	got, _, err = LoadDictionaryFiles(dir)
	if err != nil || got.Types != nil || got.Directories != nil {
		t.Fatalf("legacy load: %v", err)
	}
	var out strings.Builder
	renderViews(&out, got.Views)
	if !strings.Contains(out.String(), "WARNING") || strings.Contains(out.String(), "CREATE MATERIALIZED VIEW") {
		t.Fatal("legacy incomplete MV emitted:", out.String())
	}
	if err := writeTSV(files.TypesPath, []string{"type_id", "owner", "type_name", "object_type", "sql"}, [][]string{{"10", "APP", "T", "TYPE", "CREATE TYPE OTHER.T AS OBJECT(N INT);"}}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := LoadDictionaryFiles(dir); err == nil {
		t.Fatal("SQL identity mismatch accepted")
	}
}

func TestDirectoryInfo6AndNativeRecord(t *testing.T) {
	path := "/tmp/" + strings.Repeat("data", 40) + "/a'b"
	row := []byte{0x86, 1, 0, 0, 0, 0, 0}
	row = binary.BigEndian.AppendUint16(row, uint16(len(path)))
	row = append(row, []byte(path)...)
	if got := parseDirectoryPath(row, 0, textDecoder{preferred: "utf-8"}); got != path {
		t.Fatalf("INFO6=%q", got)
	}
	if parseDirectoryPath(row[:len(row)-1], 0, textDecoder{}) != "" {
		t.Fatal("truncated path accepted")
	}
	sql, err := directoryDDL(DictionaryDirectory{ID: 10, Name: "D", Path: path})
	if err != nil || !strings.Contains(sql, "a''b") {
		t.Fatalf("%q %v", sql, err)
	}
	file, err := os.Create(filepath.Join(t.TempDir(), "record"))
	if err != nil {
		t.Fatal(err)
	}
	record := DMPMetadataRecord{RecordType: dmpRecordDirectory, Name: "D", SQL: sql}
	charset, err := dmpCharsetFromName("UTF-8")
	if err != nil {
		t.Fatal(err)
	}
	if err := writeDMPMetadataRecord(file, charset, record); err != nil {
		t.Fatal(err)
	}
	file.Close()
	raw, err := os.ReadFile(file.Name())
	if err != nil {
		t.Fatal(err)
	}
	if binary.LittleEndian.Uint16(raw) != 36 || binary.LittleEndian.Uint16(raw[2:]) != 0xffff || binary.LittleEndian.Uint32(raw[len(raw)-4:]) != 0 {
		t.Fatalf("directory wire: %x", raw)
	}
}

func TestMaterializedViewFlagsAndBackingTable(t *testing.T) {
	fast := DictionaryView{Owner: "APP", Name: "FAST_MV", Materialized: true, MVFlags: 0x200280, HasMVFlags: true, SQL: "CREATE MATERIALIZED VIEW APP.FAST_MV", QuerySQL: "SELECT ID FROM APP.T"}
	if _, err := recoveredMaterializedViewSQL(fast); err == nil {
		t.Fatal("FAST without recovered log accepted")
	}
	for _, tc := range []struct {
		flags uint32
		want  string
	}{{0x200300, "REFRESH COMPLETE ON DEMAND WITH PRIMARY KEY DISABLE"}, {0x200320, "REFRESH COMPLETE ON COMMIT"}, {0x200380, "REFRESH FORCE ON DEMAND"}, {0x200200, "NEVER REFRESH"}, {0x200740, "WITH ROWID ENABLE"}} {
		v := DictionaryView{Owner: "APP", Name: "MV", Materialized: true, MVFlags: tc.flags, HasMVFlags: true, SQL: "CREATE MATERIALIZED VIEW APP.MV", QuerySQL: "SELECT ID FROM APP.T"}
		sql, err := recoveredMaterializedViewSQL(v)
		if err != nil || !strings.Contains(sql, tc.want) || !strings.Contains(sql, "BUILD DEFERRED") {
			t.Fatalf("%x: %q %v", tc.flags, sql, err)
		}
		v.MVFlags |= 0x1000000
		if _, err := recoveredMaterializedViewSQL(v); err == nil {
			t.Fatal("unknown flags accepted")
		}
	}
	objects := map[uint32]dictionaryObject{1: {Owner: "APP", Name: "MV", Type: "SCHOBJ", Subtype: "VIEW", Info1: 0x200300}}
	tables := map[uint32]dictionaryObject{2: {ID: 2, Owner: "APP", Name: "MTAB$_MV", Info1: 0x200000}, 3: {ID: 3, Owner: "OTHER", Name: "MTAB$_MV", Info1: 0x200000}, 4: {ID: 4, Owner: "APP", Name: "MTAB$_USER"}}
	markMaterializedBackingTables(objects, tables)
	if !tables[2].isSystemManagedInternalTable() || tables[3].isSystemManagedInternalTable() || tables[4].isSystemManagedInternalTable() {
		t.Fatalf("backing: %+v", tables)
	}
}
