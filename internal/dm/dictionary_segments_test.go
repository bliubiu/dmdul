package dm

import (
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"
)

func TestInferDictionaryTableSegmentsFromAssistPages(t *testing.T) {
	dir := t.TempDir()
	dataPath := filepath.Join(dir, "MAIN.DBF")
	const pageSize = 8192
	raw := make([]byte, pageSize*192)
	putSegmentTestPage(raw, pageSize, 32, tableDataAssistID(1001))
	putSegmentTestPage(raw, pageSize, 160, tableDataAssistID(1001))
	if err := os.WriteFile(dataPath, raw, 0644); err != nil {
		t.Fatal(err)
	}
	controlDUL := filepath.Join(dir, "control.dul")
	if err := WriteControlDUL(controlDUL, []OfflineDataFile{{GroupID: 4, FileID: 0, Tablespace: "MAIN", Path: dataPath}}); err != nil {
		t.Fatal(err)
	}

	segments := inferDictionaryTableSegments(
		"",
		controlDUL,
		dir,
		pageSize,
		16,
		map[uint32]dictionaryObject{1001: {ID: 1001, Owner: "APP", Name: "T"}},
		nil,
		nil,
		nil,
		[]DictionaryTable{{ID: 1001, Owner: "APP", Name: "T", GroupID: 4, Tablespace: "MAIN"}},
	)

	seg, ok := segments[1001]
	if !ok {
		t.Fatal("segment was not inferred")
	}
	if seg.fileID != 0 || seg.headerPage != 32 || seg.blocks != 32 || seg.extents != 2 || seg.bytes != 32*pageSize || seg.tablespaceID != 4 {
		t.Fatalf("unexpected segment: %+v", seg)
	}
}

func putSegmentTestPage(raw []byte, pageSize int, pageNo int, assistID uint32) {
	start := pageNo * pageSize
	page := raw[start : start+pageSize]
	binary.LittleEndian.PutUint16(page[dataPageSlotCountOff:], 1)
	binary.LittleEndian.PutUint16(page[dataPageFreeEndOff:], 0x70)
	binary.LittleEndian.PutUint16(page[dataPageRecordCountOff:], 1)
	binary.LittleEndian.PutUint32(page[dataPageAssistIndexOff:], assistID)
}
