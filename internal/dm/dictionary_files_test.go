package dm

import (
	"os"
	"path/filepath"
	"testing"
)

func TestWriteAndLoadDictionaryFiles(t *testing.T) {
	dir := filepath.Join(t.TempDir(), DefaultDictionaryDirName)
	dict := &DictionaryInfo{
		SystemPath:       `D:\temp\oldpro\SYSTEM.DBF`,
		ControlPath:      `D:\temp\oldpro\dm.ctl`,
		Source:           "SYSTEM.DBF",
		ExtentSize:       16,
		ExtentSizeSource: "u32 @ 0x80",
		PageSize:         8192,
		PageCount:        9472,
		Charset:          "UTF-8",
		CharsetSource:    "UNICODE_FLAG=1",
		ObjectCount:      781,
		Users: []DictionaryUser{
			{ID: 1, Name: "HR_TEST"},
		},
		Tables: []DictionaryTable{
			{ID: 1035, Owner: "HR_TEST", Name: "EMP_INFO", ColumnCount: 2, Tablespace: "MAIN", GroupID: 4, HeaderFile: 0, HeaderBlock: 16, Bytes: 131072, Blocks: 16, Extents: 1, Storage: "CLUSTERBTR"},
		},
		Columns: []DictionaryColumn{
			{TableID: 1035, TableOwner: "HR_TEST", TableName: "EMP_INFO", ColID: 1, Name: "EMP_ID", DataType: "INT", Nullable: "N"},
			{TableID: 1035, TableOwner: "HR_TEST", TableName: "EMP_INFO", ColID: 2, Name: "EMP_NAME", DataType: "VARCHAR", Length: 50, Nullable: "Y", Default: "'匿名'"},
		},
		Views: []DictionaryView{
			{ID: 2001, Owner: "SYSDBA", Name: "V_EMP", Valid: "Y", SQL: "CREATE OR REPLACE VIEW SYSDBA.V_EMP AS SELECT 1 AS ID"},
		},
		Sequences: []DictionarySequence{
			{ID: 2101, Owner: "SYSDBA", Name: "SEQ_EMP", Valid: "Y", StartWith: 1, MinValue: 1, MaxValue: 999999999999, IncrementBy: 1, CycleFlag: "N", OrderFlag: "N", CacheSize: 20},
		},
		Triggers: []DictionaryTrigger{
			{ID: 2201, Owner: "SYSDBA", Name: "TRG_EMP", TableOwner: "HR_TEST", TableName: "EMP_INFO", Valid: "Y", SQL: "CREATE OR REPLACE TRIGGER SYSDBA.TRG_EMP BEFORE INSERT ON HR_TEST.EMP_INFO BEGIN NULL; END;"},
		},
		Synonyms: []DictionarySynonym{
			{ID: 3001, Owner: "HR_TEST", Name: "SYN_V_EMP", TableOwner: "SYSDBA", TableName: "V_EMP"},
		},
		TabPrivileges: []DictionaryTabPrivilege{
			{Grantee: "HR_TEST", Owner: "SYSDBA", ObjectName: "V_EMP", ObjectType: "VIEW", Privilege: "SELECT", Grantable: "N"},
			{Grantee: "HR_TEST", Owner: "SYSDBA", ObjectName: "SEQ_EMP", ObjectType: "SEQUENCE", Privilege: "SELECT", Grantable: "N"},
		},
	}

	written, err := WriteDictionaryFiles(dir, dict)
	if err != nil {
		t.Fatalf("WriteDictionaryFiles returned error: %v", err)
	}
	if written.UserCount != 1 || written.TableCount != 1 || written.ColumnCount != 2 || written.ViewCount != 1 || written.SequenceCount != 1 || written.TriggerCount != 1 || written.SynonymCount != 1 || written.TabPrivilegeCount != 2 {
		t.Fatalf("unexpected written counts: %+v", written)
	}

	loaded, files, err := LoadDictionaryFiles(dir)
	if err != nil {
		t.Fatalf("LoadDictionaryFiles returned error: %v", err)
	}
	if files.Dir != dir || files.ColumnCount != 2 || files.ViewCount != 1 || files.SequenceCount != 1 || files.TriggerCount != 1 || files.SynonymCount != 1 || files.TabPrivilegeCount != 2 {
		t.Fatalf("unexpected loaded files result: %+v", files)
	}
	if loaded.Source != "dictionary files" || loaded.DictionaryDir != dir {
		t.Fatalf("unexpected loaded source: source=%q dir=%q", loaded.Source, loaded.DictionaryDir)
	}
	if loaded.PageSize != 8192 || loaded.TableCount != 1 || loaded.ColumnCount != 2 {
		t.Fatalf("unexpected loaded dictionary counts: %+v", loaded)
	}
	if loaded.Tables[0].HeaderBlock != 16 || loaded.Tables[0].Bytes != 131072 || loaded.Tables[0].Blocks != 16 || loaded.Tables[0].Extents != 1 {
		t.Fatalf("segment fields were not preserved: %+v", loaded.Tables[0])
	}
	if loaded.Columns[1].Default != "'匿名'" {
		t.Fatalf("default value was not preserved: %+v", loaded.Columns[1])
	}
	if len(loaded.Views) != 1 || loaded.Views[0].Name != "V_EMP" || loaded.Views[0].SQL == "" {
		t.Fatalf("view was not preserved: %+v", loaded.Views)
	}
	if len(loaded.Sequences) != 1 || loaded.Sequences[0].Name != "SEQ_EMP" || loaded.Sequences[0].IncrementBy != 1 || loaded.Sequences[0].MaxValue != 999999999999 || loaded.Sequences[0].CacheSize != 20 {
		t.Fatalf("sequence was not preserved: %+v", loaded.Sequences)
	}
	if len(loaded.Triggers) != 1 || loaded.Triggers[0].Name != "TRG_EMP" || loaded.Triggers[0].SQL == "" {
		t.Fatalf("trigger was not preserved: %+v", loaded.Triggers)
	}
	if len(loaded.Synonyms) != 1 || loaded.Synonyms[0].Name != "SYN_V_EMP" {
		t.Fatalf("synonym was not preserved: %+v", loaded.Synonyms)
	}
	if len(loaded.TabPrivileges) != 2 || loaded.TabPrivileges[0].Privilege != "SELECT" {
		t.Fatalf("tab privilege was not preserved: %+v", loaded.TabPrivileges)
	}
}

func TestLoadDictionaryFilesAcceptsUTF8BOMHeaders(t *testing.T) {
	dir := filepath.Join(t.TempDir(), DefaultDictionaryDirName)
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	files := dictionaryFilesResultForDir(dir)
	if err := os.WriteFile(files.MetaPath, []byte("\ufeffkey\tvalue\nformat_version\t1\npage_size\t8192\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(files.UsersPath, []byte("\ufeffuser_id\tuser_name\n1\tHR_TEST\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(files.TablesPath, []byte("\ufefftable_id\towner\ttable_name\tcolumn_count\ttablespace\tgroup_id\ttemporary\tstorage\tpartitioned\n1035\tHR_TEST\tEMP_INFO\t1\tMAIN\t4\tNO\tCLUSTERBTR\tNO\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(files.ColumnsPath, []byte("\ufefftable_id\towner\ttable_name\tcol_id\tcolumn_name\tdata_type\tlength\tscale\tnullable\tdefault\n1035\tHR_TEST\tEMP_INFO\t1\tEMP_ID\tINT\t\t0\tN\t\n"), 0644); err != nil {
		t.Fatal(err)
	}

	loaded, result, err := LoadDictionaryFiles(dir)
	if err != nil {
		t.Fatalf("LoadDictionaryFiles returned error: %v", err)
	}
	if result.UserCount != 1 || result.TableCount != 1 || result.ColumnCount != 1 {
		t.Fatalf("unexpected result counts: %+v", result)
	}
	if loaded.PageSize != 8192 || loaded.Users[0].Name != "HR_TEST" || loaded.Tables[0].Name != "EMP_INFO" || loaded.Columns[0].Name != "EMP_ID" {
		t.Fatalf("unexpected loaded dictionary: %+v", loaded)
	}
}
