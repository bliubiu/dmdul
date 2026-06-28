package dm

import (
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
			{ID: 1035, Owner: "HR_TEST", Name: "EMP_INFO", ColumnCount: 2, Tablespace: "MAIN", GroupID: 4, Storage: "CLUSTERBTR"},
		},
		Columns: []DictionaryColumn{
			{TableID: 1035, TableOwner: "HR_TEST", TableName: "EMP_INFO", ColID: 1, Name: "EMP_ID", DataType: "INT", Nullable: "N"},
			{TableID: 1035, TableOwner: "HR_TEST", TableName: "EMP_INFO", ColID: 2, Name: "EMP_NAME", DataType: "VARCHAR", Length: 50, Nullable: "Y", Default: "'匿名'"},
		},
	}

	written, err := WriteDictionaryFiles(dir, dict)
	if err != nil {
		t.Fatalf("WriteDictionaryFiles returned error: %v", err)
	}
	if written.UserCount != 1 || written.TableCount != 1 || written.ColumnCount != 2 {
		t.Fatalf("unexpected written counts: %+v", written)
	}

	loaded, files, err := LoadDictionaryFiles(dir)
	if err != nil {
		t.Fatalf("LoadDictionaryFiles returned error: %v", err)
	}
	if files.Dir != dir || files.ColumnCount != 2 {
		t.Fatalf("unexpected loaded files result: %+v", files)
	}
	if loaded.Source != "dictionary files" || loaded.DictionaryDir != dir {
		t.Fatalf("unexpected loaded source: source=%q dir=%q", loaded.Source, loaded.DictionaryDir)
	}
	if loaded.PageSize != 8192 || loaded.TableCount != 1 || loaded.ColumnCount != 2 {
		t.Fatalf("unexpected loaded dictionary counts: %+v", loaded)
	}
	if loaded.Columns[1].Default != "'匿名'" {
		t.Fatalf("default value was not preserved: %+v", loaded.Columns[1])
	}
}
