package storage

import (
	"os"
	"strings"
	"testing"
)

func TestInspectFile(t *testing.T) {
	tmp, err := os.CreateTemp(t.TempDir(), "dmdul-*.dbf")
	if err != nil {
		t.Fatalf("CreateTemp returned error: %v", err)
	}
	defer tmp.Close()

	if _, err := tmp.Write([]byte{0x44, 0x4d, 0x01, 0x02}); err != nil {
		t.Fatalf("Write returned error: %v", err)
	}

	result, err := InspectFile(tmp.Name(), 16)
	if err != nil {
		t.Fatalf("InspectFile returned error: %v", err)
	}
	if result.Size != 4 {
		t.Fatalf("size = %d, want 4", result.Size)
	}
	if len(result.Sample) != 4 {
		t.Fatalf("sample length = %d, want 4", len(result.Sample))
	}
	if !strings.Contains(result.HexDump(), "44 4d") {
		t.Fatalf("hex dump should contain sample bytes, got %q", result.HexDump())
	}
}
