package dm

import (
	"encoding/binary"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLocateRowsInDataPage(t *testing.T) {
	page := make([]byte, 8192)
	pos := dataRowAreaStart
	putTestRow(page, pos, 5, 0x00)
	pos += 5
	putTestRow(page, pos, 4, 0x01)
	pos += 4
	putTestRow(page, pos, 6, 0x00)
	pos += 6
	binary.LittleEndian.PutUint16(page[dataPageSlotCountOff:], 3)
	binary.LittleEndian.PutUint16(page[dataPageFreeEndOff:], uint16(pos))
	binary.LittleEndian.PutUint16(page[dataPageRecordCountOff:], 2)
	putTestDataPageSlot(page, 8192, 3, 1, dataRowAreaStart)
	putTestDataPageSlot(page, 8192, 3, 2, dataRowAreaStart+5)
	putTestDataPageSlot(page, 8192, 3, 3, dataRowAreaStart+9)

	if !isProbableDMDataPage(page, 8192) {
		t.Fatal("expected test page to look like a DM data page")
	}
	binary.LittleEndian.PutUint16(page[dataPageTreeLevelOff:], 1)
	if isProbableDMDataPage(page, 8192) {
		t.Fatal("expected non-leaf tree page to be skipped")
	}
	binary.LittleEndian.PutUint16(page[dataPageTreeLevelOff:], 0)
	rows := locateRowsInDataPage(page, 8192, 2)
	if len(rows) != 2 {
		t.Fatalf("expected 2 live rows, got %d", len(rows))
	}
	if rows[0].slotNo != 1 || rows[0].offset != dataRowAreaStart || rows[0].length != 5 {
		t.Fatalf("unexpected first row: %+v", rows[0])
	}
	if rows[1].slotNo != 3 || rows[1].offset != dataRowAreaStart+9 || rows[1].length != 6 {
		t.Fatalf("unexpected second row: %+v", rows[1])
	}
}

func TestLocateRowsInHeapDataPageUsesSlotArray(t *testing.T) {
	page := make([]byte, 8192)
	offsets := []int{0x4FA, 0x3D4, 0x2AE, 0x188, 0x62}
	for i, off := range offsets {
		putTestRow(page, off, 0x126, 0x00)
		page[off+3] = byte(i + 1)
	}
	binary.LittleEndian.PutUint16(page[dataPageSlotCountOff:], 7)
	binary.LittleEndian.PutUint16(page[dataPageFreeEndOff:], 0x620)
	binary.LittleEndian.PutUint16(page[dataPageRecordCountOff:], 5)
	putTestDataPageSlot(page, 8192, 7, 1, 0x5A)
	for i, off := range offsets {
		putTestDataPageSlot(page, 8192, 7, i+2, off)
	}
	putTestDataPageSlot(page, 8192, 7, 7, 0x52)

	rows := locateRowsInDataPage(page, 8192, 5)
	if len(rows) != 5 {
		t.Fatalf("expected 5 live rows, got %d", len(rows))
	}
	if rows[0].offset != 0x62 || rows[4].offset != 0x4FA {
		t.Fatalf("rows were not sorted by physical offset: %+v", rows)
	}
	if rows[0].length != 0x126 {
		t.Fatalf("heap row length = %d, want 0x126", rows[0].length)
	}
}

func TestDecodeDMDateTime(t *testing.T) {
	var raw [8]byte
	v := uint64(2026) |
		uint64(6)<<15 |
		uint64(18)<<19 |
		uint64(9)<<24 |
		uint64(23)<<29 |
		uint64(20)<<35 |
		uint64(123456)<<41
	binary.LittleEndian.PutUint64(raw[:], v)

	got, err := decodeDMDateTime(raw[:])
	if err != nil {
		t.Fatalf("decodeDMDateTime returned error: %v", err)
	}
	want := "2026-06-18 09:23:20.123456"
	if got != want {
		t.Fatalf("decodeDMDateTime = %q, want %q", got, want)
	}
}

func TestDecodeDMDate(t *testing.T) {
	got, err := decodeDMDate([]byte{0xD0, 0x07, 0x35})
	if err != nil {
		t.Fatalf("decodeDMDate returned error: %v", err)
	}
	if got != "2000-10-06" {
		t.Fatalf("decodeDMDate = %q, want %q", got, "2000-10-06")
	}
}

func TestDataFileKeyFromPageHeader(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "MAIN.DBF")
	head := make([]byte, 16)
	binary.LittleEndian.PutUint16(head[0:], 4)
	binary.LittleEndian.PutUint16(head[2:], 0)
	binary.LittleEndian.PutUint32(head[4:], 0)
	if err := os.WriteFile(path, head, 0644); err != nil {
		t.Fatal(err)
	}

	got, ok := dataFileKeyFromPageHeader(path)
	if !ok {
		t.Fatal("dataFileKeyFromPageHeader returned !ok")
	}
	if got.groupID != 4 || got.fileID != 0 {
		t.Fatalf("dataFileKeyFromPageHeader = %+v", got)
	}
}

func TestCandidateMatchesFileUsesDictionarySegmentRange(t *testing.T) {
	info := dataTableInfo{
		table: dictionaryObject{ID: 1024, Owner: "SY", Name: "t"},
		segment: tableSegment{
			fileID:       0,
			headerPage:   176,
			blocks:       16,
			tablespaceID: 4,
		},
		segmentKnown: true,
	}
	file := dataFileRef{key: dataFileKey{groupID: 4, fileID: 0}}
	if !candidateMatchesFile(info, file, 176) || !candidateMatchesFile(info, file, 191) {
		t.Fatal("candidate should match pages inside segment range")
	}
	if candidateMatchesFile(info, file, 175) || candidateMatchesFile(info, file, 192) {
		t.Fatal("candidate should not match pages outside segment range")
	}
	if candidateMatchesFile(info, dataFileRef{key: dataFileKey{groupID: 4, fileID: 1}}, 176) {
		t.Fatal("candidate should not match a different file")
	}
}

func TestCandidateMatchesFileKeepsMultiExtentPagesInSameFile(t *testing.T) {
	info := dataTableInfo{
		table: dictionaryObject{ID: 1033, Owner: "SYSDBA", Name: "T"},
		segment: tableSegment{
			fileID:       0,
			headerPage:   32,
			blocks:       32,
			extents:      2,
			tablespaceID: 4,
		},
		segmentKnown: true,
	}
	file := dataFileRef{key: dataFileKey{groupID: 4, fileID: 0}}
	if !candidateMatchesFile(info, file, 160) {
		t.Fatal("multi-extent table should keep pages in the same file")
	}
	if candidateMatchesFile(info, dataFileRef{key: dataFileKey{groupID: 5, fileID: 0}}, 160) {
		t.Fatal("multi-extent table should still reject a different tablespace")
	}
	if candidateMatchesFile(info, dataFileRef{key: dataFileKey{groupID: 4, fileID: 1}}, 160) {
		t.Fatal("multi-extent table should still reject a different file")
	}
}

func TestBuildStoragePagePlanWalksLeafChainFromRoot(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "MAIN.DBF")
	raw := make([]byte, 32*8192)
	putTestDMPageHeader(raw[16*8192:17*8192], 4, 0, 16, dmPageKindBTreeLeaf, 1038)
	putTestDMPageRef(raw[16*8192:17*8192], dmPageNextRefOff, 0, 17)
	putTestDMPageHeader(raw[17*8192:18*8192], 4, 0, 17, dmPageKindBTreeLeaf, 1038)
	putTestDMNullPageRef(raw[17*8192:18*8192], dmPageNextRefOff)
	if err := os.WriteFile(path, raw, 0644); err != nil {
		t.Fatal(err)
	}

	cache := newDataFilePageCache([]dataFileRef{{key: dataFileKey{groupID: 4, fileID: 0}, path: path}}, 8192)
	plan := buildStoragePagePlan(indexDef{ID: 1038, GroupID: 4, RootFile: 0, RootPage: 16}, cache)
	if len(plan) != 2 {
		t.Fatalf("expected 2 planned pages, got %d", len(plan))
	}
	if !plan[dataPageRef{key: dataFileKey{groupID: 4, fileID: 0}, pageNo: 16}] || !plan[dataPageRef{key: dataFileKey{groupID: 4, fileID: 0}, pageNo: 17}] {
		t.Fatalf("unexpected page plan: %+v", plan)
	}
}

func TestBuildStoragePagePlanDescendsInternalRoot(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "MAIN.DBF")
	raw := make([]byte, 64*8192)
	putTestDMPageHeader(raw[16*8192:17*8192], 4, 0, 16, dmPageKindBTreeRoot, 1041)
	binary.LittleEndian.PutUint32(raw[16*8192+dmBTreeLeftmostChildOff:], 20)
	putTestDMPageHeader(raw[20*8192:21*8192], 4, 0, 20, dmPageKindBTreeLeaf, 1041)
	putTestDMPageRef(raw[20*8192:21*8192], dmPageNextRefOff, 0, 21)
	putTestDMPageHeader(raw[21*8192:22*8192], 4, 0, 21, dmPageKindBTreeLeaf, 1041)
	putTestDMNullPageRef(raw[21*8192:22*8192], dmPageNextRefOff)
	if err := os.WriteFile(path, raw, 0644); err != nil {
		t.Fatal(err)
	}

	cache := newDataFilePageCache([]dataFileRef{{key: dataFileKey{groupID: 4, fileID: 0}, path: path}}, 8192)
	plan := buildStoragePagePlan(indexDef{ID: 1041, GroupID: 4, RootFile: 0, RootPage: 16}, cache)
	if len(plan) != 2 {
		t.Fatalf("expected 2 planned leaf pages, got %d", len(plan))
	}
	if plan[dataPageRef{key: dataFileKey{groupID: 4, fileID: 0}, pageNo: 16}] {
		t.Fatalf("internal root should not be exported as a data page: %+v", plan)
	}
	if !plan[dataPageRef{key: dataFileKey{groupID: 4, fileID: 0}, pageNo: 20}] || !plan[dataPageRef{key: dataFileKey{groupID: 4, fileID: 0}, pageNo: 21}] {
		t.Fatalf("unexpected page plan: %+v", plan)
	}
}

func TestCandidateMatchesFileUsesPagePlanBeforeSegmentRange(t *testing.T) {
	info := dataTableInfo{
		table: dictionaryObject{ID: 1038, Owner: "SYSDBA", Name: "BIN_TEST2"},
		pagePlan: map[dataPageRef]bool{
			{key: dataFileKey{groupID: 5, fileID: 0}, pageNo: 144}: true,
		},
		pagePlanKnown: true,
		segment: tableSegment{
			fileID:       0,
			headerPage:   16,
			blocks:       16,
			tablespaceID: 5,
		},
		segmentKnown: true,
	}
	file := dataFileRef{key: dataFileKey{groupID: 5, fileID: 0}}
	if !candidateMatchesFile(info, file, 144) {
		t.Fatal("exact page plan should take precedence over the old contiguous segment window")
	}
	if candidateMatchesFile(info, file, 145) {
		t.Fatal("page outside the exact page plan should not match")
	}
	if candidateMatchesFile(info, dataFileRef{key: dataFileKey{groupID: 4, fileID: 0}}, 144) {
		t.Fatal("segment identity check should still reject a different tablespace")
	}
}

func TestCandidateMatchesFileRecoveryIgnoresCurrentSegmentWindow(t *testing.T) {
	info := dataTableInfo{
		table:           dictionaryObject{ID: 1066, Owner: "USERS1", Name: "T_TEST"},
		recoveryMode:    true,
		recoveryGroupID: 6,
		segment: tableSegment{
			fileID:       0,
			headerPage:   16,
			blocks:       16,
			extents:      1,
			tablespaceID: 6,
		},
		segmentKnown: true,
	}
	file := dataFileRef{key: dataFileKey{groupID: 6, fileID: 0}}
	if !candidateMatchesFile(info, file, 180) {
		t.Fatal("recovery mode should allow residual pages outside the current post-truncate segment window")
	}
	if candidateMatchesFile(info, dataFileRef{key: dataFileKey{groupID: 7, fileID: 0}}, 180) {
		t.Fatal("recovery mode should still reject a different tablespace when group id is known")
	}
}

func TestResolveDataFilesWithoutControlFileUsesPageHeaders(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "MAIN.DBF")
	head := make([]byte, 16)
	binary.LittleEndian.PutUint16(head[0:], 4)
	binary.LittleEndian.PutUint16(head[2:], 0)
	binary.LittleEndian.PutUint32(head[4:], 0)
	if err := os.WriteFile(path, head, 0644); err != nil {
		t.Fatal(err)
	}

	files, err := resolveDataFiles("", "", dir)
	if err != nil {
		t.Fatalf("resolveDataFiles returned error: %v", err)
	}
	if len(files) != 1 {
		t.Fatalf("resolveDataFiles returned %d files, want 1", len(files))
	}
	if files[0].key.groupID != 4 || files[0].key.fileID != 0 || files[0].path != path {
		t.Fatalf("unexpected data file ref: %+v", files[0])
	}
}

func TestDecodeDMNumber(t *testing.T) {
	tests := []struct {
		raw  []byte
		want string
	}{
		{raw: []byte{0xC3, 0x03, 0x33}, want: "25000"},
		{raw: []byte{0xC3, 0x02, 0x51}, want: "18000"},
		{raw: []byte{0xC2, 0x0D, 0x23}, want: "1234"},
		{raw: []byte{0x80}, want: "0"},
	}
	for _, tt := range tests {
		got, ok := decodeDMNumber(tt.raw)
		if !ok {
			t.Fatalf("decodeDMNumber(% X) returned !ok", tt.raw)
		}
		if got != tt.want {
			t.Fatalf("decodeDMNumber(% X) = %q, want %q", tt.raw, got, tt.want)
		}
	}
}

func TestRenderInsertForMixedDateAndVariableRow(t *testing.T) {
	row := []byte{
		0x00, 0x0D, 0x00,
		0xD0, 0x07, 0x35,
		0x82, 0xC1, 0x02,
		0x83, 'A', 'B', 'C',
	}
	info := dataTableInfo{
		table: dictionaryObject{Owner: "SYSDBA", Name: "T"},
		columns: []columnDef{
			{ColID: 1, Name: "ID", DataType: "NUMBER(11)"},
			{ColID: 2, Name: "NAME", DataType: "VARCHAR2"},
			{ColID: 3, Name: "BIRTHDAY", DataType: "DATE"},
		},
	}
	sql, start, end, err := renderInsertForDataRow(info, row, textDecoder{preferred: "utf-8"})
	if err != nil {
		t.Fatalf("renderInsertForDataRow returned error: %v", err)
	}
	if start != 3 || end != len(row) {
		t.Fatalf("unexpected data bounds: %d..%d", start, end)
	}
	if !strings.Contains(sql, "INSERT INTO SYSDBA.T (ID, NAME, BIRTHDAY) VALUES (1, 'ABC', DATE '2000-10-06');") {
		t.Fatalf("unexpected insert sql: %s", sql)
	}
}

func TestRenderInsertForTrailingNullVariableRow(t *testing.T) {
	row := make([]byte, 141)
	row[0] = 0x00
	binary.LittleEndian.PutUint16(row[1:], uint16(len(row)))
	row[5] = 0x3F
	row[6] = 0xC0
	putInt32 := func(pos int, value int32) {
		binary.LittleEndian.PutUint32(row[pos:], uint32(value))
	}
	putInt64 := func(pos int, value int64) {
		binary.LittleEndian.PutUint64(row[pos:], uint64(value))
	}
	putInt32(7, 1038)
	putInt32(11, 5)
	putInt32(15, 0)
	putInt32(19, 16)
	putInt32(23, 1)
	putInt32(27, 3)
	putInt32(31, 1)
	putInt32(35, 98)
	putInt32(39, 56)
	putInt32(43, 0)
	putInt64(47, 118250)
	putInt64(55, 1)
	pos := 75
	for _, value := range []string{"SYSDBA", "BIN_TEST2", "TBS_BIN_TEST", "SYSDBA.BIN_TEST2"} {
		row[pos] = byte(0x80 + len(value))
		pos++
		copy(row[pos:], value)
		pos += len(value)
	}
	copy(row[pos:], []byte{0x01, 0x00, 0x00, 0x00, 0x00, 0x00, 0xFF, 0xFF})

	info := dataTableInfo{
		table: dictionaryObject{Owner: "SYSDBA", Name: "DM_DATA_ROW_SCAN"},
		columns: []columnDef{
			{ColID: 1, Name: "owner", DataType: "varchar", Nullable: "Y"},
			{ColID: 2, Name: "table_name", DataType: "varchar", Nullable: "Y"},
			{ColID: 3, Name: "table_id", DataType: "INT", Nullable: "Y"},
			{ColID: 4, Name: "tablespace_name", DataType: "varchar", Nullable: "Y"},
			{ColID: 5, Name: "ts_id", DataType: "INT", Nullable: "Y"},
			{ColID: 6, Name: "file_id", DataType: "INT", Nullable: "Y"},
			{ColID: 7, Name: "page_no", DataType: "INT", Nullable: "Y"},
			{ColID: 8, Name: "slot_no", DataType: "INT", Nullable: "Y"},
			{ColID: 9, Name: "n_slot", DataType: "INT", Nullable: "Y"},
			{ColID: 10, Name: "n_rec", DataType: "INT", Nullable: "Y"},
			{ColID: 11, Name: "row_offset", DataType: "INT", Nullable: "Y"},
			{ColID: 12, Name: "row_len", DataType: "INT", Nullable: "Y"},
			{ColID: 13, Name: "is_del", DataType: "INT", Nullable: "Y"},
			{ColID: 14, Name: "trx_id", DataType: "bigint", Nullable: "Y"},
			{ColID: 15, Name: "clu_rowid", DataType: "bigint", Nullable: "Y"},
			{ColID: 16, Name: "roll_addr_file", DataType: "INT", Nullable: "Y"},
			{ColID: 17, Name: "roll_addr_page", DataType: "INT", Nullable: "Y"},
			{ColID: 18, Name: "roll_addr_off", DataType: "INT", Nullable: "Y"},
			{ColID: 19, Name: "page_tname", DataType: "varchar", Nullable: "Y"},
			{ColID: 20, Name: "datafile_path", DataType: "varchar", Nullable: "Y"},
		},
	}
	sql, _, _, err := renderInsertForDataRow(info, row, textDecoder{preferred: "utf-8"})
	if err != nil {
		t.Fatalf("renderInsertForDataRow returned error: %v", err)
	}
	want := "VALUES ('SYSDBA', 'BIN_TEST2', 1038, 'TBS_BIN_TEST', 5, 0, 16, 1, 3, 1, 98, 56, 0, 118250, 1, NULL, NULL, NULL, 'SYSDBA.BIN_TEST2', NULL);"
	if !strings.Contains(sql, want) {
		t.Fatalf("unexpected insert sql: %s", sql)
	}
}

func TestReadExtendedDataVarchar(t *testing.T) {
	raw := append([]byte{0xC2}, []byte(strings.Repeat("A", 66))...)
	got, next, err := readShortDataVarchar(raw, 0, textDecoder{preferred: "utf-8"})
	if err != nil {
		t.Fatalf("readShortDataVarchar returned error: %v", err)
	}
	if got != strings.Repeat("A", 66) {
		t.Fatalf("readShortDataVarchar = %q", got)
	}
	if next != len(raw) {
		t.Fatalf("next = %d, want %d", next, len(raw))
	}
}

func TestReadTwoByteLengthDataVarchar(t *testing.T) {
	raw := append([]byte{0x01, 0x04}, []byte(strings.Repeat("A", 260))...)
	got, next, err := readShortDataVarchar(raw, 0, textDecoder{preferred: "utf-8"})
	if err != nil {
		t.Fatalf("readShortDataVarchar returned error: %v", err)
	}
	if got != strings.Repeat("A", 260) {
		t.Fatalf("readShortDataVarchar len = %d, want 260", len(got))
	}
	if next != len(raw) {
		t.Fatalf("next = %d, want %d", next, len(raw))
	}
}

func TestReadShortDataBytes(t *testing.T) {
	raw := append([]byte{0x8E}, []byte("Hello DaMeng 1")...)
	got, next, err := readShortDataBytes(raw, 0)
	if err != nil {
		t.Fatalf("readShortDataBytes returned error: %v", err)
	}
	if string(got) != "Hello DaMeng 1" {
		t.Fatalf("readShortDataBytes = %q", string(got))
	}
	if next != len(raw) {
		t.Fatalf("next = %d, want %d", next, len(raw))
	}
}

func TestRenderInsertForInlineLOBRow(t *testing.T) {
	row := make([]byte, 0, 128)
	row = append(row, 0x00, 0x00, 0x00)
	var id [4]byte
	binary.LittleEndian.PutUint32(id[:], 1)
	row = append(row, id[:]...)
	var dt [8]byte
	v := uint64(2026) |
		uint64(6)<<15 |
		uint64(26)<<19 |
		uint64(9)<<24 |
		uint64(46)<<29 |
		uint64(2)<<35
	binary.LittleEndian.PutUint64(dt[:], v)
	row = append(row, dt[:]...)
	row = append(row, 0x8C)
	row = append(row, []byte("LOB_TEST_001")...)
	clobText := "这是CLOB测试"
	clobBytes := testInlineLOBEnvelope(1, []byte(clobText))
	row = append(row, byte(0x80+len(clobBytes)))
	row = append(row, clobBytes...)
	blobData := []byte("Hello DaMeng 1")
	blobBytes := testInlineLOBEnvelope(2, blobData)
	row = append(row, byte(0x80+len(blobBytes)))
	row = append(row, blobBytes...)
	binary.BigEndian.PutUint16(row[0:], uint16(len(row)))

	info := dataTableInfo{
		table: dictionaryObject{Owner: "SYSDBA", Name: "T_LOB_TEST"},
		columns: []columnDef{
			{ColID: 1, Name: "ID", DataType: "INT", Nullable: "N"},
			{ColID: 2, Name: "BIZ_NO", DataType: "VARCHAR2", Nullable: "Y"},
			{ColID: 3, Name: "CLOB_TEXT", DataType: "CLOB", Nullable: "Y"},
			{ColID: 4, Name: "BLOB_DATA", DataType: "BLOB", Nullable: "Y"},
			{ColID: 5, Name: "CREATE_TIME", DataType: "DATETIME", Nullable: "Y"},
		},
	}
	sql, _, _, err := renderInsertForDataRow(info, row, textDecoder{preferred: "utf-8"})
	if err != nil {
		t.Fatalf("renderInsertForDataRow returned error: %v", err)
	}
	if !strings.Contains(sql, "CLOB_TEXT, BLOB_DATA") || !strings.Contains(sql, "'这是CLOB测试'") {
		t.Fatalf("unexpected CLOB insert sql: %s", sql)
	}
	if !strings.Contains(sql, "HEXTORAW('48656C6C6F2044614D656E672031')") {
		t.Fatalf("unexpected BLOB insert sql: %s", sql)
	}
}

func testInlineLOBEnvelope(seq byte, payload []byte) []byte {
	raw := make([]byte, 13+len(payload))
	raw[0] = 0x01
	raw[1] = 0x27
	raw[2] = 0x04
	raw[3] = seq
	binary.LittleEndian.PutUint32(raw[9:13], uint32(len(payload)))
	copy(raw[13:], payload)
	return raw
}

func TestRenderInsertForHeapRowWithLongVarchar(t *testing.T) {
	text := strings.Repeat("堆表测试数据-第1条", 10)
	row := make([]byte, 0, 0x126)
	row = append(row, 0x01, 0x26, 0x00)
	var id [8]byte
	binary.LittleEndian.PutUint64(id[:], 1)
	row = append(row, id[:]...)
	var dt [8]byte
	v := uint64(2026) |
		uint64(6)<<15 |
		uint64(24)<<19 |
		uint64(13)<<24 |
		uint64(7)<<29 |
		uint64(28)<<35
	binary.LittleEndian.PutUint64(dt[:], v)
	row = append(row, dt[:]...)
	rawText := []byte(text)
	row = append(row, byte(len(rawText)>>8), byte(len(rawText)))
	row = append(row, rawText...)
	row = append(row, []byte{0xFF, 0xFF, 0xFF, 0xFF, 0x7F, 0xFF, 0xFF, 0x87, 0xDD, 0x01, 0x00, 0x00, 0x00}...)

	info := dataTableInfo{
		table: dictionaryObject{Owner: "HR_TEST", Name: "T_LOG_HEAP"},
		columns: []columnDef{
			{ColID: 1, Name: "ID", DataType: "BIGINT"},
			{ColID: 2, Name: "LOG_TIME", DataType: "DATETIME"},
			{ColID: 3, Name: "LOG_TEXT", DataType: "VARCHAR2"},
		},
	}
	sql, _, _, err := renderInsertForDataRow(info, row, textDecoder{preferred: "utf-8"})
	if err != nil {
		t.Fatalf("renderInsertForDataRow returned error: %v", err)
	}
	if !strings.Contains(sql, "VALUES (1, DATETIME '2026-06-24 13:07:28.000000'") || !strings.Contains(sql, text) {
		t.Fatalf("unexpected insert sql: %s", sql)
	}
}

func TestRenderInsertForIOTRowWithNullableFixedSentinel(t *testing.T) {
	row := []byte{
		0x00, 0x8D, 0x00, 0x03, 0x00,
		0x01, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00,
		0x32, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00,
		0x01,
		0xEA, 0x07, 0xD3, 0xEA, 0xCE, 0x72, 0x4B, 0x0A,
		0x00, 0x00, 0x3B, 0x02, 0x00, 0x00, 0x73, 0x7F,
		0x86, 'u', 's', 'e', 'r', '_', '1',
		0xA0, 'e', '1', '0', 'a', 'd', 'c', '3', '9', '4', '9', 'b', 'a', '5', '9', 'a', 'b', 'b', 'e', '5', '6', 'e', '0', '5', '7', 'f', '2', '0', 'f', '8', '8', '3', 'e',
		0x89, 0xB2, 0xE2, 0xCA, 0xD4, 0xD3, 0xC3, 0xBB, 0xA7, 0x31,
		0x8B, '1', '3', '8', '6', '.', '0', '5', '7', '1', '3', '6',
		0x8B, 'u', '1', '@', 't', 'e', 's', 't', '.', 'c', 'o', 'm',
		0x89, 0xB2, 0xE2, 0xCA, 0xD4, 0xCA, 0xFD, 0xBE, 0xDD, 0x31,
		0x01, 0x00, 0x00, 0x00, 0x00, 0x00, 0xFF, 0xFF,
		0xFF, 0xFF, 0x7F, 0xFF, 0xFF, 0x50, 0x09, 0x04, 0x00, 0x00, 0x00,
	}
	info := dataTableInfo{
		table: dictionaryObject{Owner: "SYSDBA", Name: "SYS_USER"},
		columns: []columnDef{
			{ColID: 1, Name: "ID", DataType: "BIGINT", Nullable: "N"},
			{ColID: 2, Name: "USERNAME", DataType: "VARCHAR", Nullable: "N"},
			{ColID: 3, Name: "PASSWORD", DataType: "VARCHAR", Nullable: "N"},
			{ColID: 4, Name: "REAL_NAME", DataType: "VARCHAR", Nullable: "Y"},
			{ColID: 5, Name: "PHONE", DataType: "VARCHAR", Nullable: "Y"},
			{ColID: 6, Name: "EMAIL", DataType: "VARCHAR", Nullable: "Y"},
			{ColID: 7, Name: "DEPT_ID", DataType: "BIGINT", Nullable: "Y"},
			{ColID: 8, Name: "STATUS", DataType: "TINYINT", Nullable: "Y"},
			{ColID: 9, Name: "CREATE_TIME", DataType: "DATETIME", Nullable: "Y"},
			{ColID: 10, Name: "UPDATE_TIME", DataType: "DATETIME", Nullable: "Y"},
			{ColID: 11, Name: "REMARK", DataType: "VARCHAR", Nullable: "Y"},
		},
	}
	sql, start, _, err := renderInsertForDataRow(info, row, textDecoder{preferred: "gb18030"})
	if err != nil {
		t.Fatalf("renderInsertForDataRow returned error: %v", err)
	}
	if start != 5 {
		t.Fatalf("data start = %d, want 5", start)
	}
	for _, want := range []string{
		"SYSDBA.SYS_USER",
		"VALUES (1, 'user_1'",
		"DATETIME '2026-06-26 10:55:25.337337'",
		"NULL, '测试数据1'",
	} {
		if !strings.Contains(sql, want) {
			t.Fatalf("insert sql missing %q: %s", want, sql)
		}
	}
}

func TestRenderInsertForSingleFixedColumnPrefersRowHeaderStart(t *testing.T) {
	row := []byte{
		0x00, 0x1A, 0x00,
		0x14, 0x00, 0x00, 0x00,
		0x01, 0x00, 0x00, 0x00, 0x00, 0x00,
		0xFF, 0xFF, 0xFF, 0xFF, 0x7F, 0xFF, 0xFF,
		0xE2, 0x19, 0x01, 0x00, 0x00, 0x00,
	}
	info := dataTableInfo{
		table: dictionaryObject{Owner: "SY", Name: "t"},
		columns: []columnDef{
			{ColID: 1, Name: "id", DataType: "INT", Nullable: "Y"},
		},
	}
	sql, start, _, err := renderInsertForDataRow(info, row, textDecoder{preferred: "utf-8"})
	if err != nil {
		t.Fatalf("renderInsertForDataRow returned error: %v", err)
	}
	if start != 3 {
		t.Fatalf("data start = %d, want 3", start)
	}
	if !strings.Contains(sql, `INSERT INTO SY."t" ("id") VALUES (20);`) {
		t.Fatalf("unexpected insert sql: %s", sql)
	}
}

func TestRenderInsertForDataRow(t *testing.T) {
	row := []byte{
		0x00, 0x0E, 0x00,
		0x64, 0x00, 0x00, 0x00,
		0x83, 'A', 'B', 'C',
	}
	info := dataTableInfo{
		table: dictionaryObject{Owner: "SYSDBA", Name: "T1"},
		columns: []columnDef{
			{ColID: 1, Name: "ID", DataType: "INT"},
			{ColID: 2, Name: "NAME", DataType: "VARCHAR"},
		},
	}
	sql, start, end, err := renderInsertForDataRow(info, row, textDecoder{preferred: "utf-8"})
	if err != nil {
		t.Fatalf("renderInsertForDataRow returned error: %v", err)
	}
	if start != 3 || end != len(row) {
		t.Fatalf("unexpected data bounds: %d..%d", start, end)
	}
	if !strings.Contains(sql, "INSERT INTO SYSDBA.T1 (ID, NAME) VALUES (100, 'ABC');") {
		t.Fatalf("unexpected insert sql: %s", sql)
	}
}

func TestTableNameMatcherPrefersQualifiedNames(t *testing.T) {
	matcher := newTableNameMatcher(`SYSDBA.BIN_TEST2,"SYSDBA"."中文测试表"`)
	if !matcher.allowed("SYSDBA", "BIN_TEST2") {
		t.Fatal("expected qualified ASCII table to match")
	}
	if !matcher.allowed("SYSDBA", "中文测试表") {
		t.Fatal("expected quoted qualified Chinese table to match")
	}
	if matcher.allowed("OTHER", "BIN_TEST2") {
		t.Fatal("qualified filter should not match same table name in another owner")
	}
}

func TestTableNameMatcherAllDefaultBehavior(t *testing.T) {
	if !newTableNameMatcher("all").allowed("SYSDBA", "BIN_TEST2") {
		t.Fatal("all table filter should allow any table")
	}
	if newTableNameMatcher("").allowed("SYSDBA", "BIN_TEST2") {
		t.Fatal("empty matcher should not allow tables by itself")
	}
}

func putTestRow(page []byte, pos int, length int, flag byte) {
	binary.BigEndian.PutUint16(page[pos:], uint16(length))
	page[pos+2] = flag
}

func putTestDataPageSlot(page []byte, pageSize int, slotCount int, slotNo int, rowOff int) {
	slotArrayStart := pageSize - pageSlotTrailerLen - slotCount*2
	binary.LittleEndian.PutUint16(page[slotArrayStart+(slotNo-1)*2:], uint16(rowOff))
}

func putTestDMPageHeader(page []byte, groupID uint16, fileID uint16, pageNo uint32, kind uint32, storageID uint32) {
	binary.LittleEndian.PutUint16(page[0:], groupID)
	binary.LittleEndian.PutUint16(page[2:], fileID)
	binary.LittleEndian.PutUint32(page[4:], pageNo)
	binary.LittleEndian.PutUint32(page[dmPageKindOff:], kind)
	binary.LittleEndian.PutUint32(page[dataPageAssistIndexOff:], storageID)
}

func putTestDMPageRef(page []byte, offset int, fileID uint16, pageNo uint32) {
	binary.LittleEndian.PutUint16(page[offset:], fileID)
	binary.LittleEndian.PutUint32(page[offset+2:], pageNo)
}

func putTestDMNullPageRef(page []byte, offset int) {
	for i := 0; i < 6; i++ {
		page[offset+i] = 0xFF
	}
}
