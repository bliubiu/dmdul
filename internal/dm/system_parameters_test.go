package dm

import (
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"
)

func TestDetectSystemInstanceNameFromOpenHistory(t *testing.T) {
	const (
		pageSize  = 8192
		pageCount = 6
		pageNo    = 5
		rowOffset = dataRowAreaStart
		rowLength = 334
	)
	raw := make([]byte, pageSize*pageCount)
	binary.LittleEndian.PutUint32(raw[systemExtentSizeOffset:], 16)
	binary.LittleEndian.PutUint32(raw[systemPageSizeOffset:], pageSize)
	binary.LittleEndian.PutUint32(raw[systemPageCountOffset:], pageCount)
	raw[pageSize*systemControlPage4No+systemUnicodeFlagOffset] = 1

	page := raw[pageNo*pageSize : (pageNo+1)*pageSize]
	binary.LittleEndian.PutUint16(page[0:], 0)
	binary.LittleEndian.PutUint16(page[2:], 0)
	binary.LittleEndian.PutUint32(page[4:], pageNo)
	binary.LittleEndian.PutUint32(page[dmPageKindOff:], dmPageKindRowData)
	binary.LittleEndian.PutUint16(page[dataPageSlotCountOff:], 1)
	binary.LittleEndian.PutUint16(page[dataPageFreeEndOff:], rowOffset+rowLength)
	binary.LittleEndian.PutUint16(page[dataPageRecordCountOff:], 1)
	binary.LittleEndian.PutUint16(page[dataPageTreeLevelOff:], 0)
	binary.LittleEndian.PutUint32(page[dataPageAssistIndexOff:], sysOpenHistoryAssistID)

	row := page[rowOffset : rowOffset+rowLength]
	binary.BigEndian.PutUint16(row, rowLength)
	copy(row[2:12], []byte{0x00, 0xFC, 0xFF, 0xFF, 0xFF, 0xFC, 0xFF, 0xFF, 0xFF, 0x00})
	copy(row[12:20], []byte{0xE9, 0x07, 0xFD, 0xAC, 0x3E, 0xCF, 0xB8, 0x1A})
	pos := 294
	for _, value := range []string{"TST_1", "NORMAL", "TST", "TST"} {
		row[pos] = byte(0x80 + len(value))
		pos++
		copy(row[pos:], value)
		pos += len(value)
	}
	slotArrayStart := pageSize - pageSlotTrailerLen - 2
	binary.LittleEndian.PutUint16(page[slotArrayStart:], rowOffset)

	path := filepath.Join(t.TempDir(), "SYSTEM.DBF")
	if err := os.WriteFile(path, raw, 0644); err != nil {
		t.Fatal(err)
	}
	name, source, ok := detectSystemInstanceNameFromFile(path, pageSize)
	if !ok || name != "TST" || source != "SYSTEM.DBF SYS.SYSOPENHISTORY.CUR_INST_NAME" {
		t.Fatalf("instance name=%q source=%q ok=%v", name, source, ok)
	}
}
