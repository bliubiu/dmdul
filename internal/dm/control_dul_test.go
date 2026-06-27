package dm

import (
	"path/filepath"
	"testing"
)

func TestInferTablespaceNameFromDataFile(t *testing.T) {
	got := inferTablespaceNameFromDataFile(`D:\dm\TBS_BIN_TEST01.DBF`, 5)
	if got != "TBS_BIN_TEST" {
		t.Fatalf("inferTablespaceNameFromDataFile = %q", got)
	}
	if got := inferTablespaceNameFromDataFile(`D:\dm\MAIN.DBF`, 4); got != "MAIN" {
		t.Fatalf("default MAIN tablespace = %q", got)
	}
}

func TestWriteAndReadControlDUL(t *testing.T) {
	path := filepath.Join(t.TempDir(), "control.dul")
	files := []OfflineDataFile{{
		GroupID:    5,
		FileID:     0,
		Tablespace: "TBS_BIN_TEST",
		Path:       `D:\temp\oldpro\TBS_BIN_TEST01.DBF`,
	}}
	if err := WriteControlDUL(path, files); err != nil {
		t.Fatalf("WriteControlDUL failed: %v", err)
	}
	got, ok := readControlDUL(path)
	if !ok || len(got) != 1 {
		t.Fatalf("readControlDUL returned ok=%v len=%d", ok, len(got))
	}
	if got[0].GroupID != 5 || got[0].FileID != 0 || got[0].Tablespace != "TBS_BIN_TEST" {
		t.Fatalf("unexpected control.dul entry: %+v", got[0])
	}
}
