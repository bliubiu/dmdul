package dm

import (
	"encoding/binary"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestHugeMultiplePathsUseFullFileIdentity(t *testing.T) {
	root := t.TempDir()
	a := filepath.Join(root, "z-first", "SCH000000123", "TAB1122")
	b := filepath.Join(root, "a-second", "SCH000000123", "TAB1122")
	for _, dir := range []string{a, b} {
		if err := os.MkdirAll(dir, 0755); err != nil {
			t.Fatal(err)
		}
	}
	table := dictionaryObject{ID: 1122, SchemaID: 123, Info2: 5, Owner: "APP", Name: "PATHS"}
	write := func(dir string, fullID uint32) string {
		var header [4096]byte
		binary.LittleEndian.PutUint16(header[:], 5)
		binary.LittleEndian.PutUint32(header[2:], 123)
		binary.LittleEndian.PutUint32(header[6:], 1122)
		binary.LittleEndian.PutUint16(header[10:], 1)
		binary.LittleEndian.PutUint32(header[12:], fullID)
		path := filepath.Join(dir, "COL0001_0000000001.dta")
		if err := os.WriteFile(path, header[:], 0600); err != nil {
			t.Fatal(err)
		}
		return path
	}
	first := write(a, 1)
	second := write(b, 0x01000001)
	sections := []hugeColumnSection{{colID: 1, fileID: 1, count: 1024}, {colID: 1, fileID: 0x01000001, count: 1024}}
	dirs, err := findHugeTableDirs(root, 123, 1122)
	if err != nil {
		t.Fatal(err)
	}
	files, err := resolveHugeColumnFiles(dirs, table, sections)
	if err != nil || files[hugeColumnFileKey{1, 1}] != first || files[hugeColumnFileKey{1, 0x01000001}] != second {
		t.Fatalf("files=%v err=%v", files, err)
	}
	if _, err := resolveHugeColumnFiles([]string{a}, table, sections); err == nil || !strings.Contains(err.Error(), "path_index=1") {
		t.Fatalf("missing path: %v", err)
	}
	write(a, 0x01000001)
	if _, err := resolveHugeColumnFiles(dirs, table, sections[1:]); err == nil || !strings.Contains(err.Error(), "ambiguous") {
		t.Fatalf("duplicate: %v", err)
	}
	table.ID++
	if _, err := resolveHugeColumnFiles(dirs, table, sections[1:]); err == nil {
		t.Fatal("foreign table accepted")
	}
}
