package dm

import (
	"encoding/binary"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSystemPageStreamScansPagesWithoutWholeFileBuffer(t *testing.T) {
	const pageSize = 8192
	const pageCount = 5
	raw := make([]byte, pageSize*pageCount)
	binary.LittleEndian.PutUint32(raw[systemExtentSizeOffset:], 16)
	binary.LittleEndian.PutUint32(raw[systemPageSizeOffset:], pageSize)
	binary.LittleEndian.PutUint32(raw[systemPageCountOffset:], pageCount)
	raw[pageSize*int(systemControlPage4No)+systemUnicodeFlagOffset] = 1
	path := filepath.Join(t.TempDir(), "SYSTEM.DBF")
	if err := os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}

	stream, err := openSystemPageStream(path)
	if err != nil {
		t.Fatal(err)
	}
	defer stream.close()
	visited := 0
	var firstPageAddress *byte
	err = stream.forEachPage(func(page []byte, pageNo uint32) {
		visited++
		if pageNo == 0 {
			firstPageAddress = &page[0]
		} else if firstPageAddress != &page[0] {
			t.Fatalf("page buffer was not reused")
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	if visited != pageCount {
		t.Fatalf("visited %d pages, want %d", visited, pageCount)
	}
	charset, ok := stream.charset()
	if !ok || charset.DecoderName != "utf-8" {
		t.Fatalf("charset = %#v, %v", charset, ok)
	}
}

func TestSystemPageStreamFindsRoutineAcrossChunkBoundary(t *testing.T) {
	const pageSize = 8192
	pageCount := systemStreamChunkTarget/pageSize + 8
	raw := make([]byte, pageSize*pageCount)
	binary.LittleEndian.PutUint32(raw[systemExtentSizeOffset:], 16)
	binary.LittleEndian.PutUint32(raw[systemPageSizeOffset:], pageSize)
	binary.LittleEndian.PutUint32(raw[systemPageCountOffset:], uint32(pageCount))
	start := systemStreamChunkTarget - 12
	sql := "CREATE OR REPLACE FUNCTION SYSDBA.F_STREAM RETURN INT AS BEGIN RETURN 1; END;\x00"
	copy(raw[start:], sql)
	path := filepath.Join(t.TempDir(), "SYSTEM.DBF")
	if err := os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}

	stream, err := openSystemPageStream(path)
	if err != nil {
		t.Fatal(err)
	}
	defer stream.close()
	routines, err := stream.rawRoutineTexts(textDecoder{preferred: "utf-8"})
	if err != nil {
		t.Fatal(err)
	}
	got := routines[routineKey("SYSDBA", "F_STREAM", "FUNCTION")]
	if !strings.Contains(got, "RETURN 1") {
		t.Fatalf("routine was not recovered across chunk boundary: %q", got)
	}
}

func TestForEachDataFilePageUsesBoundedPageBuffer(t *testing.T) {
	const pageSize = 4096
	raw := make([]byte, pageSize*3)
	for i := 0; i < 3; i++ {
		raw[i*pageSize] = byte(i + 1)
	}
	path := filepath.Join(t.TempDir(), "MAIN.DBF")
	if err := os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	var values []byte
	var firstPageAddress *byte
	count, err := forEachDataFilePage(path, pageSize, func(page []byte, pageNo uint32) error {
		values = append(values, page[0])
		if pageNo == 0 {
			firstPageAddress = &page[0]
		} else if firstPageAddress != &page[0] {
			t.Fatalf("page buffer was not reused")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if count != 3 || string(values) != string([]byte{1, 2, 3}) {
		t.Fatalf("count=%d values=%v", count, values)
	}
}
