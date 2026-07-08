package cli

import (
	"bytes"
	"encoding/binary"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"dmdul/internal/dm"
)

func TestRunHelp(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	if err := Run([]string{"help"}, &stdout, &stderr); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if !strings.Contains(stdout.String(), "dmdul") {
		t.Fatalf("help output should mention dmdul, got %q", stdout.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr should be empty, got %q", stderr.String())
	}
}

func TestRunInteractiveHelpAndExit(t *testing.T) {
	previousDir, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd failed: %v", err)
	}
	if err := os.Chdir(t.TempDir()); err != nil {
		t.Fatalf("Chdir failed: %v", err)
	}
	defer func() {
		if err := os.Chdir(previousDir); err != nil {
			t.Fatalf("restore Chdir failed: %v", err)
		}
	}()

	var stdout bytes.Buffer
	var stderr bytes.Buffer

	if err := RunInteractive(strings.NewReader("help;\nexit;\n"), &stdout, &stderr); err != nil {
		t.Fatalf("RunInteractive returned error: %v", err)
	}
	output := stdout.String()
	for _, want := range []string{"dmdul: Release v0.1.2", "Dameng Database Offline Recovery & Data Unloader", "Copyright (c) 2026 greatfinish", "https://github.com/greatfinish/dmdul", "DMDUL>", "bootstrap;", "list user;", "unload table", "unload database", "recover table", "bye"} {
		if !strings.Contains(output, want) {
			t.Fatalf("interactive output should contain %q, got %q", want, output)
		}
	}
}

func TestInteractiveOutputDirDefaultsToDataDirWhenSet(t *testing.T) {
	session := newInteractiveSession()
	if got := session.outputPath("HR_TEST_data.sql"); got != "HR_TEST_data.sql" {
		t.Fatalf("default outputPath = %q", got)
	}
	session.dataDir = `D:\temp\oldpro`
	session.dataDirSet = true
	if got := session.outputPath("HR_TEST_data.sql"); got != `D:\temp\oldpro\HR_TEST_data.sql` {
		t.Fatalf("data_dir outputPath = %q", got)
	}
	session.outputDir = `D:\out`
	session.outputDirSet = true
	if got := session.outputPath("HR_TEST_data.sql"); got != `D:\out\HR_TEST_data.sql` {
		t.Fatalf("explicit output_dir outputPath = %q", got)
	}
}

func TestInteractiveWritesInitDULToCurrentDirThenDataDir(t *testing.T) {
	previousDir, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd failed: %v", err)
	}
	currentDir := t.TempDir()
	dataDir := t.TempDir()
	if err := os.Chdir(currentDir); err != nil {
		t.Fatalf("Chdir failed: %v", err)
	}
	defer func() {
		if err := os.Chdir(previousDir); err != nil {
			t.Fatalf("restore Chdir failed: %v", err)
		}
	}()

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	input := "show parameter;\nset data_dir " + dataDir + ";\nshow parameter;\nexit;\n"
	if err := RunInteractive(strings.NewReader(input), &stdout, &stderr); err != nil {
		t.Fatalf("RunInteractive returned error: %v", err)
	}
	currentINI := currentDir + string(os.PathSeparator) + defaultInitDULPath
	dataINI := dataDir + string(os.PathSeparator) + defaultInitDULPath
	if _, err := os.Stat(currentINI); err != nil {
		t.Fatalf("current init.dul was not generated: %v", err)
	}
	content, err := os.ReadFile(dataINI)
	if err != nil {
		t.Fatalf("data_dir init.dul was not generated: %v", err)
	}
	text := string(content)
	for _, want := range []string{"data_dir=" + dataDir, "data_format=sql", "charset=auto"} {
		if !strings.Contains(text, want) {
			t.Fatalf("init.dul should contain %q, got %q", want, text)
		}
	}
	if !strings.Contains(stdout.String(), "init_dul") {
		t.Fatalf("show parameter should print init_dul path, got %q", stdout.String())
	}
}

func TestInteractiveLoadInitDULCommand(t *testing.T) {
	previousDir, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd failed: %v", err)
	}
	dir := t.TempDir()
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("Chdir failed: %v", err)
	}
	defer func() {
		if err := os.Chdir(previousDir); err != nil {
			t.Fatalf("restore Chdir failed: %v", err)
		}
	}()

	content := "\ufeffsystem=D:\\manual\\SYSTEM.DBF\ncharset=gb18030\ndata_format=csv\n"
	if err := os.WriteFile(defaultInitDULPath, []byte(content), 0644); err != nil {
		t.Fatalf("WriteFile init.dul failed: %v", err)
	}
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if err := RunInteractive(strings.NewReader("load init;\nshow parameter;\nexit;\n"), &stdout, &stderr); err != nil {
		t.Fatalf("RunInteractive returned error: %v", err)
	}
	output := stdout.String()
	for _, want := range []string{"init loaded: init.dul", "system     = D:\\manual\\SYSTEM.DBF", "data_format= csv", "charset    = gb18030", "init_load  = init.dul"} {
		if !strings.Contains(output, want) {
			t.Fatalf("interactive output should contain %q, got %q", want, output)
		}
	}
}

func TestInteractiveLoadDictionaryUpdatesAutoCharset(t *testing.T) {
	previousDir, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd failed: %v", err)
	}
	dir := t.TempDir()
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("Chdir failed: %v", err)
	}
	defer func() {
		if err := os.Chdir(previousDir); err != nil {
			t.Fatalf("restore Chdir failed: %v", err)
		}
	}()

	if _, err := dm.WriteDictionaryFiles(filepath.Join(dir, dm.DefaultDictionaryDirName), &dm.DictionaryInfo{
		SystemPath:    `D:\manual\SYSTEM.DBF`,
		Source:        "SYSTEM.DBF",
		Charset:       "GB18030 (UNICODE_FLAG=0)",
		CharsetSource: "test",
		Users:         []dm.DictionaryUser{{ID: 1, Name: "HR_TEST"}},
	}); err != nil {
		t.Fatalf("WriteDictionaryFiles failed: %v", err)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if err := RunInteractive(strings.NewReader("load dictionary;\nshow parameter;\nexit;\n"), &stdout, &stderr); err != nil {
		t.Fatalf("RunInteractive returned error: %v", err)
	}
	if !strings.Contains(stdout.String(), "charset    = gb18030") {
		t.Fatalf("load dictionary should update charset, got %q", stdout.String())
	}
}

func TestInteractiveListUserShowsObjectCounts(t *testing.T) {
	previousDir, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd failed: %v", err)
	}
	dir := t.TempDir()
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("Chdir failed: %v", err)
	}
	defer func() {
		if err := os.Chdir(previousDir); err != nil {
			t.Fatalf("restore Chdir failed: %v", err)
		}
	}()

	if _, err := dm.WriteDictionaryFiles(filepath.Join(dir, dm.DefaultDictionaryDirName), &dm.DictionaryInfo{
		Source: "SYSTEM.DBF",
		Users:  []dm.DictionaryUser{{ID: 1, Name: "APP"}},
		Tables: []dm.DictionaryTable{
			{ID: 1001, Owner: "APP", Name: "T1"},
		},
		Views: []dm.DictionaryView{
			{ID: 2001, Owner: "APP", Name: "V1"},
		},
		Sequences: []dm.DictionarySequence{
			{ID: 3001, Owner: "APP", Name: "S1"},
		},
		Routines: []dm.DictionaryRoutine{
			{ID: 4001, Owner: "APP", Name: "F1", ObjectType: "FUNCTION"},
			{ID: 4002, Owner: "APP", Name: "P1", ObjectType: "PROCEDURE"},
			{ID: 4003, Owner: "APP", Name: "PKG1", ObjectType: "PACKAGE"},
			{ID: 4003, Owner: "APP", Name: "PKG1", ObjectType: "PACKAGE BODY"},
		},
		Triggers: []dm.DictionaryTrigger{
			{ID: 5001, Owner: "APP", Name: "TRG1"},
		},
		Synonyms: []dm.DictionarySynonym{
			{ID: 6001, Owner: "APP", Name: "SYN1"},
		},
	}); err != nil {
		t.Fatalf("WriteDictionaryFiles failed: %v", err)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if err := RunInteractive(strings.NewReader("load dictionary;\nlist user;\nexit;\n"), &stdout, &stderr); err != nil {
		t.Fatalf("RunInteractive returned error: %v", err)
	}
	output := stdout.String()
	for _, want := range []string{"tables", "views", "synonyms", "sequences", "triggers", "functions", "procedures", "packages"} {
		if !strings.Contains(output, want) {
			t.Fatalf("list user output should contain %q, got %q", want, output)
		}
	}
	if !strings.Contains(output, "APP                           1        1          1          1         1          1           1         1") {
		t.Fatalf("list user should show per-owner object counts, got %q", output)
	}
}

func TestInteractiveUnloadDatabaseSQLAutoLoadsDictionary(t *testing.T) {
	cwd, outDir := setupUnloadDatabaseFixture(t)
	runInDir(t, cwd, func() {
		var stdout bytes.Buffer
		var stderr bytes.Buffer
		input := "set output_dir " + outDir + ";\nunload database;\nexit;\n"
		if err := RunInteractive(strings.NewReader(input), &stdout, &stderr); err != nil {
			t.Fatalf("RunInteractive returned error: %v", err)
		}
		output := stdout.String()
		for _, want := range []string{
			"ddl output:",
			"DATABASE_ddl.sql",
			"data output:",
			"DATABASE_data.sql",
			"users exported: 2",
			"tables exported: 2",
			"views exported: 1",
			"sequences exported: 1",
			"routines exported: 1",
			"triggers exported: 1",
			"synonyms exported: 2",
			"tab privileges exported: 3",
			"rows exported: 1",
			"rows failed: 0",
		} {
			if !strings.Contains(output, want) {
				t.Fatalf("interactive output should contain %q, got %q", want, output)
			}
		}
		ddl := readTestFile(t, filepath.Join(outDir, "DATABASE_ddl.sql"))
		for _, want := range []string{
			"CREATE USER APP",
			"CREATE USER NO_TABLE",
			"CREATE TABLE APP.WITH_ROWS",
			"CREATE TABLE APP.EMPTY_TABLE",
			"CREATE SEQUENCE APP.SEQ_WITH_ROWS",
			"START WITH 1",
			"INCREMENT BY 1",
			"MAXVALUE 999999999999",
			"CACHE 20",
			"CREATE OR REPLACE FUNCTION APP.F_WITH_ROWS RETURN INT AS BEGIN RETURN 1; END;",
			"CREATE OR REPLACE VIEW APP.V_WITH_ROWS AS SELECT ID FROM APP.WITH_ROWS;",
			"CREATE OR REPLACE TRIGGER APP.TRG_WITH_ROWS BEFORE INSERT ON APP.WITH_ROWS BEGIN NULL; END;",
			"CREATE OR REPLACE SYNONYM NO_TABLE.SYN_WITH_ROWS FOR APP.WITH_ROWS;",
			"CREATE OR REPLACE SYNONYM NO_TABLE.SYN_SEQ_WITH_ROWS FOR APP.SEQ_WITH_ROWS;",
			"GRANT SELECT ON APP.WITH_ROWS TO NO_TABLE;",
			"GRANT SELECT ON APP.V_WITH_ROWS TO NO_TABLE;",
			"GRANT SELECT ON APP.SEQ_WITH_ROWS TO NO_TABLE;",
			"STORAGE(ON \"MAIN\", CLUSTERBTR)",
		} {
			if !strings.Contains(ddl, want) {
				t.Fatalf("DATABASE_ddl.sql should contain %q, got %q", want, ddl)
			}
		}
		data := readTestFile(t, filepath.Join(outDir, "DATABASE_data.sql"))
		if !strings.Contains(data, "INSERT INTO APP.WITH_ROWS (ID) VALUES (100);") {
			t.Fatalf("DATABASE_data.sql should contain exported row, got %q", data)
		}
	})
}

func TestInteractiveUnloadDatabaseCSVExportsPerTableAndSkipsEmptyTables(t *testing.T) {
	cwd, outDir := setupUnloadDatabaseFixture(t)
	runInDir(t, cwd, func() {
		var stdout bytes.Buffer
		var stderr bytes.Buffer
		input := "set output_dir " + outDir + ";\nset data_format csv;\nunload database;\nexit;\n"
		if err := RunInteractive(strings.NewReader(input), &stdout, &stderr); err != nil {
			t.Fatalf("RunInteractive returned error: %v", err)
		}
		output := stdout.String()
		for _, want := range []string{
			"DATABASE_ddl.sql",
			"csv output:",
			"DATABASE_APP_WITH_ROWS_data.csv",
			"csv skipped: APP.EMPTY_TABLE (no rows)",
			"csv files exported: 1",
			"rows exported: 1",
			"rows failed: 0",
		} {
			if !strings.Contains(output, want) {
				t.Fatalf("interactive output should contain %q, got %q", want, output)
			}
		}
		csvPath := filepath.Join(outDir, "DATABASE_APP_WITH_ROWS_data.csv")
		csvText := readTestFile(t, csvPath)
		if strings.TrimSpace(csvText) != "ID\n100" && strings.TrimSpace(csvText) != "ID\r\n100" {
			t.Fatalf("unexpected csv output %q", csvText)
		}
		if _, err := os.Stat(filepath.Join(outDir, "DATABASE_APP_EMPTY_TABLE_data.csv")); !os.IsNotExist(err) {
			t.Fatalf("empty table csv should not exist, stat err=%v", err)
		}
	})
}

func TestInteractiveUnloadDatabaseCustomPrefix(t *testing.T) {
	cwd, outDir := setupUnloadDatabaseFixture(t)
	runInDir(t, cwd, func() {
		var stdout bytes.Buffer
		var stderr bytes.Buffer
		input := "set output_dir " + outDir + ";\nunload database to rescue_all;\nexit;\n"
		if err := RunInteractive(strings.NewReader(input), &stdout, &stderr); err != nil {
			t.Fatalf("RunInteractive returned error: %v", err)
		}
		output := stdout.String()
		for _, want := range []string{"rescue_all_ddl.sql", "rescue_all_data.sql", "rows exported: 1"} {
			if !strings.Contains(output, want) {
				t.Fatalf("interactive output should contain %q, got %q", want, output)
			}
		}
		for _, name := range []string{"rescue_all_ddl.sql", "rescue_all_data.sql"} {
			if _, err := os.Stat(filepath.Join(outDir, name)); err != nil {
				t.Fatalf("%s should exist: %v", name, err)
			}
		}
		if _, err := os.Stat(filepath.Join(outDir, "DATABASE_ddl.sql")); !os.IsNotExist(err) {
			t.Fatalf("default ddl should not be created when custom prefix is used, stat err=%v", err)
		}
	})
}

func TestCharsetParameterFromDictionary(t *testing.T) {
	tests := []struct {
		input string
		want  string
		ok    bool
	}{
		{input: "GB18030 (UNICODE_FLAG=0)", want: "gb18030", ok: true},
		{input: "UTF-8 (UNICODE_FLAG=1)", want: "utf-8", ok: true},
		{input: "EUC-KR (UNICODE_FLAG=2)", want: "euc-kr", ok: true},
		{input: "unknown (UNICODE_FLAG=9)", ok: false},
	}
	for _, tt := range tests {
		got, ok := charsetParameterFromDictionary(tt.input)
		if ok != tt.ok || got != tt.want {
			t.Fatalf("charsetParameterFromDictionary(%q) = %q,%v want %q,%v", tt.input, got, ok, tt.want, tt.ok)
		}
	}
}

func setupUnloadDatabaseFixture(t *testing.T) (string, string) {
	t.Helper()
	cwd := t.TempDir()
	dataDir := t.TempDir()
	outDir := t.TempDir()
	systemPath := filepath.Join(dataDir, "SYSTEM.DBF")
	mainPath := filepath.Join(dataDir, "MAIN.DBF")
	writeMinimalSystemDBF(t, systemPath)
	writeMinimalMainDBFWithOneIntRow(t, mainPath, 1001, 100)
	_, err := dm.WriteDictionaryFiles(filepath.Join(outDir, dm.DefaultDictionaryDirName), &dm.DictionaryInfo{
		SystemPath:       systemPath,
		Source:           "SYSTEM.DBF",
		ExtentSize:       16,
		ExtentSizeSource: "u32 @ 0x80",
		PageSize:         8192,
		PageCount:        5,
		Charset:          "UTF-8 (UNICODE_FLAG=1)",
		CharsetSource:    "SYSTEM.DBF page 4 + 0x2D",
		Users: []dm.DictionaryUser{
			{ID: 10, Name: "APP"},
			{ID: 11, Name: "NO_TABLE"},
		},
		Tables: []dm.DictionaryTable{
			{ID: 1001, Owner: "APP", Name: "WITH_ROWS", ColumnCount: 1, Tablespace: "MAIN", GroupID: 4, Storage: "CLUSTERBTR"},
			{ID: 1002, Owner: "APP", Name: "EMPTY_TABLE", ColumnCount: 1, Tablespace: "MAIN", GroupID: 4, Storage: "CLUSTERBTR"},
		},
		Columns: []dm.DictionaryColumn{
			{TableID: 1001, TableOwner: "APP", TableName: "WITH_ROWS", ColID: 1, Name: "ID", DataType: "INT", Nullable: "N"},
			{TableID: 1002, TableOwner: "APP", TableName: "EMPTY_TABLE", ColID: 1, Name: "ID", DataType: "INT", Nullable: "N"},
		},
		Views: []dm.DictionaryView{
			{ID: 2001, Owner: "APP", Name: "V_WITH_ROWS", Valid: "Y", SQL: "CREATE OR REPLACE VIEW APP.V_WITH_ROWS AS SELECT ID FROM APP.WITH_ROWS"},
		},
		Sequences: []dm.DictionarySequence{
			{ID: 2101, Owner: "APP", Name: "SEQ_WITH_ROWS", Valid: "Y", StartWith: 1, MinValue: 1, MaxValue: 999999999999, IncrementBy: 1, CycleFlag: "N", OrderFlag: "N", CacheSize: 20},
		},
		Routines: []dm.DictionaryRoutine{
			{ID: 2151, Owner: "APP", Name: "F_WITH_ROWS", ObjectType: "FUNCTION", SeqNo: 0, Valid: "Y", SQL: "CREATE OR REPLACE FUNCTION APP.F_WITH_ROWS RETURN INT AS BEGIN RETURN 1; END;"},
		},
		Triggers: []dm.DictionaryTrigger{
			{ID: 2201, Owner: "APP", Name: "TRG_WITH_ROWS", TableOwner: "APP", TableName: "WITH_ROWS", Valid: "Y", SQL: "CREATE OR REPLACE TRIGGER APP.TRG_WITH_ROWS BEFORE INSERT ON APP.WITH_ROWS BEGIN NULL; END;"},
		},
		Synonyms: []dm.DictionarySynonym{
			{ID: 3001, Owner: "NO_TABLE", Name: "SYN_WITH_ROWS", TableOwner: "APP", TableName: "WITH_ROWS"},
			{ID: 3002, Owner: "NO_TABLE", Name: "SYN_SEQ_WITH_ROWS", TableOwner: "APP", TableName: "SEQ_WITH_ROWS"},
		},
		TabPrivileges: []dm.DictionaryTabPrivilege{
			{Grantee: "NO_TABLE", Owner: "APP", ObjectName: "WITH_ROWS", ObjectType: "TABLE", Privilege: "SELECT", Grantable: "N"},
			{Grantee: "NO_TABLE", Owner: "APP", ObjectName: "V_WITH_ROWS", ObjectType: "VIEW", Privilege: "SELECT", Grantable: "N"},
			{Grantee: "NO_TABLE", Owner: "APP", ObjectName: "SEQ_WITH_ROWS", ObjectType: "SEQUENCE", Privilege: "SELECT", Grantable: "N"},
		},
	})
	if err != nil {
		t.Fatalf("WriteDictionaryFiles failed: %v", err)
	}
	return cwd, outDir
}

func writeMinimalSystemDBF(t *testing.T, path string) {
	t.Helper()
	const pageSize = 8192
	pageCount := 5
	raw := make([]byte, pageSize*pageCount)
	binary.LittleEndian.PutUint32(raw[0x80:], 16)
	binary.LittleEndian.PutUint32(raw[0x84:], pageSize)
	binary.LittleEndian.PutUint32(raw[0x8C:], uint32(pageCount))
	raw[pageSize*4+0x2D] = 1
	if err := os.WriteFile(path, raw, 0644); err != nil {
		t.Fatalf("WriteFile SYSTEM.DBF failed: %v", err)
	}
}

func writeMinimalMainDBFWithOneIntRow(t *testing.T, path string, tableID uint32, idValue int32) {
	t.Helper()
	const (
		pageSize               = 8192
		pageSlotTrailerLen     = 8
		dataRowAreaStart       = 0x62
		dataPageSlotCountOff   = 0x24
		dataPageFreeEndOff     = 0x26
		dataPageRecordCountOff = 0x2C
		dataPageTreeLevelOff   = 0x38
		dataPageAssistIndexOff = 0x3A
		dataFilePageGroupIDOff = 0
		dataFilePageFileIDOff  = 2
		dataFilePagePageNoOff  = 4
		dmPageKindOff          = 0x14
		dmPageKindRowData      = 0x14
	)
	page := make([]byte, pageSize)
	binary.LittleEndian.PutUint16(page[dataFilePageGroupIDOff:], 4)
	binary.LittleEndian.PutUint16(page[dataFilePageFileIDOff:], 0)
	binary.LittleEndian.PutUint32(page[dataFilePagePageNoOff:], 0)
	binary.LittleEndian.PutUint32(page[dmPageKindOff:], dmPageKindRowData)
	binary.LittleEndian.PutUint16(page[dataPageSlotCountOff:], 1)
	binary.LittleEndian.PutUint16(page[dataPageFreeEndOff:], dataRowAreaStart+7)
	binary.LittleEndian.PutUint16(page[dataPageRecordCountOff:], 1)
	binary.LittleEndian.PutUint16(page[dataPageTreeLevelOff:], 0)
	binary.LittleEndian.PutUint32(page[dataPageAssistIndexOff:], 0x02000000|(tableID&0x00FFFFFF))
	binary.BigEndian.PutUint16(page[dataRowAreaStart:], 7)
	page[dataRowAreaStart+2] = 0
	binary.LittleEndian.PutUint32(page[dataRowAreaStart+3:], uint32(idValue))
	slotArrayStart := pageSize - pageSlotTrailerLen - 2
	binary.LittleEndian.PutUint16(page[slotArrayStart:], dataRowAreaStart)
	if err := os.WriteFile(path, page, 0644); err != nil {
		t.Fatalf("WriteFile MAIN.DBF failed: %v", err)
	}
}

func runInDir(t *testing.T, dir string, fn func()) {
	t.Helper()
	previousDir, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd failed: %v", err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("Chdir failed: %v", err)
	}
	defer func() {
		if err := os.Chdir(previousDir); err != nil {
			t.Fatalf("restore Chdir failed: %v", err)
		}
	}()
	fn()
}

func readTestFile(t *testing.T, path string) string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile %s failed: %v", path, err)
	}
	return string(raw)
}

func TestTimestampedLogLine(t *testing.T) {
	at := time.Date(2026, 6, 28, 10, 23, 45, 0, time.Local)
	got := timestampedLogLine("DMDUL> list user", at)
	want := "2026-06-28 10:23:45 DMDUL> list user"
	if got != want {
		t.Fatalf("timestampedLogLine = %q, want %q", got, want)
	}
}

func TestInspectRequiresFile(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	err := Run([]string{"inspect"}, &stdout, &stderr)
	if err == nil {
		t.Fatal("Run should fail when inspect has no -file")
	}
	if !strings.Contains(err.Error(), "requires -file") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestInspectCtlRequiresCtl(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	err := Run([]string{"inspect-ctl"}, &stdout, &stderr)
	if err == nil {
		t.Fatal("Run should fail when inspect-ctl has no -ctl")
	}
	if !strings.Contains(err.Error(), "requires -ctl") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestScanSystemDoesNotRequireControlFile(t *testing.T) {
	dir := t.TempDir()
	systemPath := dir + string(os.PathSeparator) + "SYSTEM.DBF"
	if err := os.WriteFile(systemPath, []byte{0}, 0644); err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}

	if err := validateOptionalControlInputFiles("scan-system", systemPath, "", false); err != nil {
		t.Fatalf("scan-system should not require dm.ctl, got %v", err)
	}
}

func TestExportDDLDefaultsRequireOnlySystemFile(t *testing.T) {
	previousDir, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd failed: %v", err)
	}
	if err := os.Chdir(t.TempDir()); err != nil {
		t.Fatalf("Chdir failed: %v", err)
	}
	defer func() {
		if err := os.Chdir(previousDir); err != nil {
			t.Fatalf("restore Chdir failed: %v", err)
		}
	}()

	var stdout bytes.Buffer
	var stderr bytes.Buffer

	err = Run([]string{"export-ddl"}, &stdout, &stderr)
	if err == nil {
		t.Fatal("Run should fail when default export input files are absent")
	}
	for _, want := range []string{"requires -file", "SYSTEM.DBF"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("expected error to contain %q, got %v", want, err)
		}
	}
	if strings.Contains(err.Error(), "dm.ctl") {
		t.Fatalf("export-ddl should not require dm.ctl, got %v", err)
	}
}

func TestExportDataDefaultsRequireOnlySystemFile(t *testing.T) {
	previousDir, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd failed: %v", err)
	}
	if err := os.Chdir(t.TempDir()); err != nil {
		t.Fatalf("Chdir failed: %v", err)
	}
	defer func() {
		if err := os.Chdir(previousDir); err != nil {
			t.Fatalf("restore Chdir failed: %v", err)
		}
	}()

	var stdout bytes.Buffer
	var stderr bytes.Buffer

	err = Run([]string{"export-data"}, &stdout, &stderr)
	if err == nil {
		t.Fatal("Run should fail when default export-data input files are absent")
	}
	for _, want := range []string{"requires -file", "SYSTEM.DBF"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("expected error to contain %q, got %v", want, err)
		}
	}
	if strings.Contains(err.Error(), "dm.ctl") {
		t.Fatalf("export-data should not require dm.ctl, got %v", err)
	}
}
