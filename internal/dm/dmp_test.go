package dm

import (
	"bytes"
	"compress/zlib"
	"crypto/md5"
	"encoding/binary"
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

func TestInspectDMPCompressedFooter(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "plain.dmp")
	writer, err := NewDMPDataWriter(DMPDataOptions{
		OutputPath: path, RowFormatFlag: 1, Schema: "APP", Table: "T1", TableID: 77, ColumnCount: 1,
	})
	if err != nil {
		t.Fatalf("NewDMPDataWriter failed: %v", err)
	}
	if err := writer.WriteRow([]DMPField{DMPShortField([]byte("1"))}); err != nil {
		t.Fatalf("WriteRow failed: %v", err)
	}
	plainInfo, err := writer.Close()
	if err != nil {
		t.Fatalf("Close failed: %v", err)
	}

	var footer bytes.Buffer
	footer.Write(dmpFooterMagic[:])
	_ = writeDMPUint16(&footer, 0)
	_ = writeDMPUint64(&footer, plainInfo.SchemaRecordOffset)
	writeCompressedDMPString16(t, &footer, []byte("APP"))
	writeCompressedDMPString16(t, &footer, []byte("APP"))
	_ = writeDMPUint32(&footer, dmpTableIndexMarker)
	_ = writeDMPUint16(&footer, 0)
	_ = writeDMPUint64(&footer, plainInfo.Tables[0].MetadataOffset)
	_ = writeDMPUint32(&footer, 77)
	writeCompressedDMPString16(t, &footer, []byte("T1"))
	_ = writeDMPUint32(&footer, uint32(len(plainInfo.Tables[0].PhaseOffsets)))
	for _, offset := range plainInfo.Tables[0].PhaseOffsets {
		_ = writeDMPUint16(&footer, 0)
		_ = writeDMPUint64(&footer, offset)
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile failed: %v", err)
	}
	raw = append(raw[:plainInfo.PayloadEnd], footer.Bytes()...)
	raw[dmpCompressionOffset] = 1
	raw[dmpCompressionLevelOff] = 1
	payloadMD5 := md5.Sum(raw[DMPHeaderSize:])
	copy(raw[dmpPayloadMD5Offset:dmpPayloadMD5Offset+md5.Size], payloadMD5[:])
	raw[dmpHeaderChecksumOffset] = 0
	raw[dmpHeaderChecksumOffset] = dmpXOR(raw[:DMPHeaderSize])
	compressedPath := filepath.Join(dir, "compressed.dmp")
	if err := os.WriteFile(compressedPath, raw, 0644); err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}
	info, err := InspectDMP(compressedPath)
	if err != nil {
		t.Fatalf("InspectDMP failed: %v", err)
	}
	if !info.Compressed || info.CompressionLevel != 1 || !info.PayloadMD5Valid || info.FooterParseError != "" {
		t.Fatalf("unexpected compressed info %+v", info)
	}
	if info.Schema != "APP" || len(info.Tables) != 1 || info.Tables[0].Name != "T1" || info.Tables[0].RowCount != 1 {
		t.Fatalf("unexpected compressed footer %+v", info)
	}
}

func writeCompressedDMPString16(t *testing.T, out *bytes.Buffer, value []byte) {
	t.Helper()
	var compressed bytes.Buffer
	writer, err := zlib.NewWriterLevel(&compressed, 1)
	if err != nil {
		t.Fatalf("NewWriterLevel failed: %v", err)
	}
	if _, err := writer.Write(value); err != nil {
		t.Fatalf("zlib Write failed: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("zlib Close failed: %v", err)
	}
	if compressed.Len() > 0xFFFF {
		t.Fatal("compressed test string is too long")
	}
	var length [2]byte
	binary.LittleEndian.PutUint16(length[:], uint16(compressed.Len()))
	out.Write(length[:])
	out.Write(compressed.Bytes())
}

func TestDMPDataWriterAndInspector(t *testing.T) {
	path := filepath.Join(t.TempDir(), "table_data.dmp")
	writer, err := NewDMPDataWriter(DMPDataOptions{
		OutputPath:    path,
		RowFormatFlag: 1,
		Schema:        "APP",
		Table:         "T_DMP",
		TableID:       1234,
		ColumnCount:   3,
	})
	if err != nil {
		t.Fatalf("NewDMPDataWriter failed: %v", err)
	}
	if err := writer.WriteRow([]DMPField{
		DMPShortField([]byte("1")),
		DMPNullField(),
		DMPShortField([]byte("alpha")),
	}); err != nil {
		t.Fatalf("WriteRow 1 failed: %v", err)
	}
	longValue := bytes.Repeat([]byte{'Z'}, 70000)
	if err := writer.WriteRow([]DMPField{
		DMPShortField([]byte("2")),
		DMPShortField(nil),
		DMPLongField(longValue),
	}); err != nil {
		t.Fatalf("WriteRow 2 failed: %v", err)
	}
	info, err := writer.Close()
	if err != nil {
		t.Fatalf("Close failed: %v", err)
	}
	if info.InternalVersion != 34 || info.LogicalVersion != 26 {
		t.Fatalf("unexpected versions internal=%d logical=%d", info.InternalVersion, info.LogicalVersion)
	}
	if info.Charset != "UTF-8" || info.CharsetFlag != 1 || info.EncodingCode != 1 || !info.CharsetHeaderValid {
		t.Fatalf("unexpected default charset header %+v", info)
	}
	if !info.CaseSensitive || info.ExtentSize != 16 || info.PageSize != 8192 || info.PageCheck != 3 {
		t.Fatalf("unexpected default database header values %+v", info)
	}
	if info.Mode != DMPModeTables || info.Mode.String() != "TABLES" {
		t.Fatalf("unexpected mode %v", info.Mode)
	}
	if info.RowFormatFlag != 1 || info.ObjectCount != 0 {
		t.Fatalf("unexpected header flags row_format=%d objects=%d", info.RowFormatFlag, info.ObjectCount)
	}
	if !info.PayloadMD5Valid || !info.HeaderChecksumValid || !info.FooterMagicValid {
		t.Fatalf("invalid checks md5=%v header=%v footer=%v", info.PayloadMD5Valid, info.HeaderChecksumValid, info.FooterMagicValid)
	}
	if info.FooterParseError != "" {
		t.Fatalf("footer parse failed: %s", info.FooterParseError)
	}
	if info.Schema != "APP" || info.Owner != "APP" || len(info.Tables) != 1 {
		t.Fatalf("unexpected footer schema=%q owner=%q tables=%+v", info.Schema, info.Owner, info.Tables)
	}
	table := info.Tables[0]
	if table.ObjectID != 1234 || table.Name != "T_DMP" || table.RowCount != 2 || len(table.PhaseOffsets) != 2 {
		t.Fatalf("unexpected table index %+v", table)
	}
}

func TestDMPDataWriterCharsetHeadersAndEncodedNames(t *testing.T) {
	tests := []struct {
		name         string
		charset      string
		schema       string
		table        string
		wantCharset  string
		wantFlag     uint16
		wantEncoding uint8
	}{
		{name: "utf8-default", schema: "APP", table: "T_UTF8", wantCharset: "UTF-8", wantFlag: 1, wantEncoding: 1},
		{name: "gb18030", charset: "gb18030", schema: "恢复", table: "数据表", wantCharset: "GB18030", wantFlag: 0, wantEncoding: 10},
		{name: "euc-kr", charset: "euc-kr", schema: "복구", table: "자료표", wantCharset: "EUC-KR", wantFlag: 2, wantEncoding: 6},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "charset.dmp")
			writer, err := NewDMPDataWriter(DMPDataOptions{
				OutputPath: path, Charset: test.charset, Schema: test.schema, Table: test.table,
				TableID: 88, ColumnCount: 1,
			})
			if err != nil {
				t.Fatalf("NewDMPDataWriter failed: %v", err)
			}
			if err := writer.WriteRow([]DMPField{DMPShortField([]byte("1"))}); err != nil {
				t.Fatalf("WriteRow failed: %v", err)
			}
			info, err := writer.Close()
			if err != nil {
				t.Fatalf("Close failed: %v", err)
			}
			if info.Charset != test.wantCharset || info.CharsetFlag != test.wantFlag ||
				info.EncodingCode != test.wantEncoding || !info.CharsetHeaderValid {
				t.Fatalf("unexpected charset header %+v", info)
			}
			if info.Schema != test.schema || info.Owner != test.schema || len(info.Tables) != 1 || info.Tables[0].Name != test.table {
				t.Fatalf("encoded footer names were not recovered: %+v", info)
			}
		})
	}
}

func TestDMPDataWriterRejectsUnknownCharset(t *testing.T) {
	_, err := NewDMPDataWriter(DMPDataOptions{
		OutputPath: filepath.Join(t.TempDir(), "bad.dmp"), Charset: "big5",
		Schema: "APP", Table: "T1", TableID: 1, ColumnCount: 1,
	})
	if err == nil {
		t.Fatal("unknown dmp charset should fail")
	}
}

func TestDMPDataWriterDatabaseHeaderOptions(t *testing.T) {
	caseSensitive := false
	pageCheck := uint32(0)
	writer, err := NewDMPDataWriter(DMPDataOptions{
		OutputPath: filepath.Join(t.TempDir(), "options.dmp"),
		Schema:     "APP", Table: "T1", TableID: 1, ColumnCount: 1,
		CaseSensitive: &caseSensitive, ExtentSize: 64, PageSize: 32768, PageCheck: &pageCheck,
	})
	if err != nil {
		t.Fatalf("NewDMPDataWriter failed: %v", err)
	}
	info, err := writer.Close()
	if err != nil {
		t.Fatalf("Close failed: %v", err)
	}
	if info.CaseSensitive || info.ExtentSize != 64 || info.PageSize != 32768 || info.PageCheck != 0 {
		t.Fatalf("unexpected database header options %+v", info)
	}
}

func TestDMPPhaseRowCountSupportsContinuationPhases(t *testing.T) {
	path := filepath.Join(t.TempDir(), "phases.bin")
	raw := make([]byte, 60)
	for i, phase := range []uint32{2, 3, 14} {
		offset := i * 20
		binary.LittleEndian.PutUint16(raw[offset:offset+2], 2)
		binary.LittleEndian.PutUint16(raw[offset+2:offset+4], 0xFFFF)
		binary.LittleEndian.PutUint32(raw[offset+8:offset+12], phase)
	}
	binary.LittleEndian.PutUint32(raw[16:20], 2)
	binary.LittleEndian.PutUint32(raw[36:40], ^uint32(0))
	binary.LittleEndian.PutUint32(raw[56:60], 1)
	if err := os.WriteFile(path, raw, 0644); err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}
	file, err := os.Open(path)
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	defer file.Close()

	if rows, ok := dmpPhaseRowCount(file, 0, int64(len(raw))); !ok || rows != 2 {
		t.Fatalf("phase 2 rows = %d, %v", rows, ok)
	}
	if rows, ok := dmpPhaseRowCount(file, 20, int64(len(raw))); ok || rows != 0 {
		t.Fatalf("continuation phase rows = %d, %v", rows, ok)
	}
	if rows, ok := dmpPhaseRowCount(file, 40, int64(len(raw))); !ok || rows != 1 {
		t.Fatalf("phase 14 rows = %d, %v", rows, ok)
	}
}

func TestInspectDMPReportsPayloadAndHeaderCorruption(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "valid.dmp")
	writer, err := NewDMPDataWriter(DMPDataOptions{
		OutputPath: path, RowFormatFlag: 1, Schema: "APP", Table: "T1", TableID: 9, ColumnCount: 1,
	})
	if err != nil {
		t.Fatalf("NewDMPDataWriter failed: %v", err)
	}
	if err := writer.WriteRow([]DMPField{DMPShortField([]byte("1"))}); err != nil {
		t.Fatalf("WriteRow failed: %v", err)
	}
	if _, err := writer.Close(); err != nil {
		t.Fatalf("Close failed: %v", err)
	}

	payloadCorrupt := filepath.Join(dir, "payload_corrupt.dmp")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile failed: %v", err)
	}
	raw[DMPHeaderSize+30] ^= 0x01
	if err := os.WriteFile(payloadCorrupt, raw, 0644); err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}
	info, err := InspectDMP(payloadCorrupt)
	if err != nil {
		t.Fatalf("InspectDMP payload corruption failed: %v", err)
	}
	if info.PayloadMD5Valid {
		t.Fatal("payload corruption should invalidate MD5")
	}
	if !info.HeaderChecksumValid {
		t.Fatal("payload corruption should not change header XOR")
	}

	headerCorrupt := filepath.Join(dir, "header_corrupt.dmp")
	raw, err = os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile failed: %v", err)
	}
	raw[100] ^= 0x01
	if err := os.WriteFile(headerCorrupt, raw, 0644); err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}
	info, err = InspectDMP(headerCorrupt)
	if err != nil {
		t.Fatalf("InspectDMP header corruption failed: %v", err)
	}
	if !info.PayloadMD5Valid || info.HeaderChecksumValid {
		t.Fatalf("unexpected checks md5=%v header=%v", info.PayloadMD5Valid, info.HeaderChecksumValid)
	}
}

func TestDMPDataWriterRejectsWrongFieldCountAndAbortRemovesFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "abort.dmp")
	writer, err := NewDMPDataWriter(DMPDataOptions{
		OutputPath: path, RowFormatFlag: 1, Schema: "APP", Table: "T1", TableID: 9, ColumnCount: 2,
	})
	if err != nil {
		t.Fatalf("NewDMPDataWriter failed: %v", err)
	}
	if err := writer.WriteRow([]DMPField{DMPShortField([]byte("1"))}); err == nil {
		t.Fatal("wrong field count should fail")
	}
	if err := writer.Abort(); err != nil {
		t.Fatalf("Abort failed: %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("aborted dmp should be removed, stat err=%v", err)
	}
}

// A row must never straddle a phase boundary. Official dexp dumps overshoot
// the phase size limit to finish the row in progress, and dimp rejects the
// whole table with "data abnormal" when a phase starts mid-row.
func TestDMPDataWriterKeepsRowsWholeInsidePhases(t *testing.T) {
	path := filepath.Join(t.TempDir(), "streamed.dmp")
	writer, err := NewDMPDataWriter(DMPDataOptions{
		OutputPath: path, Schema: "APP", Table: "T_STREAM", TableID: 91, ColumnCount: 2,
	})
	if err != nil {
		t.Fatalf("NewDMPDataWriter failed: %v", err)
	}
	writer.phaseSizeLimit = 256
	longValue := bytes.Repeat([]byte("0123456789ABCDEF"), 256) // far larger than the limit
	const rows = 6
	for i := 0; i < rows; i++ {
		if err := writer.WriteRow([]DMPField{
			DMPShortField([]byte(strconv.Itoa(i))),
			DMPLongReaderField(bytes.NewReader(longValue), uint64(len(longValue))),
		}); err != nil {
			t.Fatalf("WriteRow %d failed: %v", i, err)
		}
	}
	info, err := writer.Close()
	if err != nil {
		t.Fatalf("Close failed: %v", err)
	}
	if len(info.Tables) != 1 || info.Tables[0].RowCount != rows {
		t.Fatalf("unexpected streamed table info %+v", info.Tables)
	}
	// Each row is bigger than the limit, so every row opens its own phase:
	// one preamble phase plus one per row.
	if got := len(info.Tables[0].PhaseOffsets); got != rows+1 {
		t.Fatalf("got %d phases, want %d (preamble + one per oversized row)", got, rows+1)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile failed: %v", err)
	}
	for i, offset := range info.Tables[0].PhaseOffsets {
		declared := binary.LittleEndian.Uint32(raw[offset+16 : offset+20])
		if declared == ^uint32(0) {
			t.Fatalf("phase %d is marked as continuing a split row; dimp rejects that", i+1)
		}
		if i == 0 {
			continue // preamble phase carries no rows
		}
		if declared != 1 {
			t.Fatalf("phase %d declared %d rows, want 1", i+1, declared)
		}
		length := binary.LittleEndian.Uint32(raw[offset+12 : offset+16])
		if uint64(length) <= writer.phaseSizeLimit {
			t.Fatalf("phase %d length %d should overshoot the %d limit to finish its row",
				i+1, length, writer.phaseSizeLimit)
		}
	}
	if !info.PayloadMD5Valid || !info.HeaderChecksumValid || !info.FooterMagicValid {
		t.Fatalf("invalid streamed dmp checks %+v", info)
	}
}

func TestDMPDataWriterRejectsShortFieldReader(t *testing.T) {
	path := filepath.Join(t.TempDir(), "short-reader.dmp")
	writer, err := NewDMPDataWriter(DMPDataOptions{
		OutputPath: path, Schema: "APP", Table: "T_STREAM", TableID: 92, ColumnCount: 1,
	})
	if err != nil {
		t.Fatalf("NewDMPDataWriter failed: %v", err)
	}
	err = writer.WriteRow([]DMPField{DMPLongReaderField(bytes.NewReader([]byte("short")), 100)})
	if err == nil {
		t.Fatal("short streamed field should fail")
	}
	if abortErr := writer.Abort(); abortErr != nil {
		t.Fatalf("Abort failed: %v", abortErr)
	}
}

func TestDMPDataWriterSuspendAndResume(t *testing.T) {
	path := filepath.Join(t.TempDir(), "resume.dmp")
	writer, err := NewDMPDataWriter(DMPDataOptions{
		OutputPath: path, Schema: "APP", Table: "T_RESUME", TableID: 93, ColumnCount: 1,
	})
	if err != nil {
		t.Fatalf("NewDMPDataWriter failed: %v", err)
	}
	if err := writer.WriteRow([]DMPField{DMPShortField([]byte("1"))}); err != nil {
		t.Fatalf("WriteRow 1 failed: %v", err)
	}
	if err := writer.suspend(); err != nil {
		t.Fatalf("suspend failed: %v", err)
	}
	if writer.file != nil {
		t.Fatal("suspended writer should release its file handle")
	}
	if err := writer.WriteRow([]DMPField{DMPShortField([]byte("2"))}); err != nil {
		t.Fatalf("WriteRow 2 after resume failed: %v", err)
	}
	info, err := writer.Close()
	if err != nil {
		t.Fatalf("Close failed: %v", err)
	}
	if len(info.Tables) != 1 || info.Tables[0].RowCount != 2 || !info.PayloadMD5Valid || !info.HeaderChecksumValid {
		t.Fatalf("unexpected resumed dmp info %+v", info)
	}
}

func TestDMPDataWriterUsesUint64ForFieldsOver4GiB(t *testing.T) {
	path := filepath.Join(t.TempDir(), "over4g.dmp")
	writer, err := NewDMPDataWriter(DMPDataOptions{
		OutputPath: path, Schema: "APP", Table: "T_OVER4G", TableID: 94, ColumnCount: 1,
	})
	if err != nil {
		t.Fatalf("NewDMPDataWriter failed: %v", err)
	}
	start := int64(writer.offset)
	length := uint64(1)<<32 + 123
	err = writer.WriteRow([]DMPField{DMPLongReaderField(bytes.NewReader(nil), length)})
	if err == nil {
		t.Fatal("empty reader should fail before writing a 4 GiB payload")
	}
	if err := writer.flush(); err != nil {
		t.Fatalf("flush failed: %v", err)
	}
	var prefix [10]byte
	if _, readErr := writer.file.ReadAt(prefix[:], start); readErr != nil {
		t.Fatalf("read long-field prefix failed: %v", readErr)
	}
	if marker := binary.LittleEndian.Uint16(prefix[0:2]); marker != dmpFieldLong {
		t.Fatalf("long-field marker=0x%X want=0x%X", marker, dmpFieldLong)
	}
	if got := binary.LittleEndian.Uint64(prefix[2:10]); got != length {
		t.Fatalf("long-field length=%d want=%d", got, length)
	}
	if err := writer.Abort(); err != nil {
		t.Fatalf("Abort failed: %v", err)
	}
}

func TestWriteLogicalDMPCombinesMetadataDataAndMultipleSchemas(t *testing.T) {
	dir := t.TempDir()
	spoolPath := filepath.Join(dir, "rows.spool.dmp")
	spool, err := NewDMPDataWriter(DMPDataOptions{
		OutputPath: spoolPath, Charset: "UTF-8", Schema: "APP", Table: "T_ROWS", TableID: 101, ColumnCount: 2,
	})
	if err != nil {
		t.Fatalf("NewDMPDataWriter failed: %v", err)
	}
	if err := spool.WriteRow([]DMPField{DMPShortField([]byte{1}), DMPShortField([]byte("alpha"))}); err != nil {
		t.Fatalf("WriteRow failed: %v", err)
	}
	if _, err := spool.Close(); err != nil {
		t.Fatalf("close spool failed: %v", err)
	}

	outputPath := filepath.Join(dir, "owner.dmp")
	info, err := WriteLogicalDMP(DMPLogicalOptions{
		OutputPath: outputPath,
		Catalog: &DMPMetadataCatalog{
			Mode: DMPModeOwner,
			GlobalRecords: []DMPMetadataRecord{
				{RecordType: dmpRecordUser, Name: "APP", SQL: `CREATE USER "APP" IDENTIFIED BY "Dmdul_2026#Reset";`},
			},
			Schemas: []DMPSchemaMetadata{
				{
					Name: "APP", Owner: "APP",
					Tables: []DMPTableMetadata{{
						ID: 101, Schema: "APP", Owner: "APP", Name: "T_ROWS", ColumnCount: 2,
						Records: []DMPMetadataRecord{{RecordType: dmpRecordTable, Name: "T_ROWS", SQL: `CREATE TABLE "APP"."T_ROWS" ("ID" INT, "NAME" VARCHAR(20));`}},
					}},
				},
				{
					Name: "APP_EXTRA", Owner: "APP",
					Tables: []DMPTableMetadata{{
						ID: 102, Schema: "APP_EXTRA", Owner: "APP", Name: "T_EMPTY", ColumnCount: 1,
						Records: []DMPMetadataRecord{{RecordType: dmpRecordTable, Name: "T_EMPTY", SQL: `CREATE TABLE "APP_EXTRA"."T_EMPTY" ("ID" INT);`}},
					}},
				},
			},
		},
		TableDataPaths: map[uint32]string{101: spoolPath},
		Charset:        "UTF-8",
	})
	if err != nil {
		t.Fatalf("WriteLogicalDMP failed: %v", err)
	}
	if info.Mode != DMPModeOwner || info.ObjectCount != 3 {
		t.Fatalf("unexpected logical header mode=%s objects=%d", info.Mode, info.ObjectCount)
	}
	if !info.PayloadMD5Valid || !info.HeaderChecksumValid || !info.FooterMagicValid || info.FooterParseError != "" {
		t.Fatalf("invalid logical dmp integrity: %+v", info)
	}
	if len(info.Schemas) != 2 || len(info.Tables) != 2 {
		t.Fatalf("logical footer schemas=%d tables=%d", len(info.Schemas), len(info.Tables))
	}
	if info.Schemas[0].Name != "APP" || info.Schemas[0].Owner != "APP" || info.Schemas[1].Name != "APP_EXTRA" || info.Schemas[1].Owner != "APP" {
		t.Fatalf("unexpected schema directory: %+v", info.Schemas)
	}
	if info.Tables[0].Name != "T_ROWS" || info.Tables[0].RowCount != 1 || len(info.Tables[0].PhaseOffsets) != 2 {
		t.Fatalf("unexpected data table index: %+v", info.Tables[0])
	}
	if info.Tables[1].Name != "T_EMPTY" || info.Tables[1].RowCount != 0 || len(info.Tables[1].PhaseOffsets) != 1 {
		t.Fatalf("unexpected empty table index: %+v", info.Tables[1])
	}
}

func TestDMPGrantMetadataUsesNativeTableAndSchemaRecordLayouts(t *testing.T) {
	tableGrant := dmpObjectGrantRecord(DictionaryTabPrivilege{
		Grantee: "DST", Owner: "SRC", ObjectName: "T1", ObjectType: "TABLE", Privilege: "SELECT", Grantable: "N",
	})
	viewGrant := dmpObjectGrantRecord(DictionaryTabPrivilege{
		Grantee: "DST", Owner: "SRC", ObjectName: "V1", ObjectType: "VIEW", Privilege: "SELECT", Grantable: "Y",
	})
	sequenceGrant := dmpObjectGrantRecord(DictionaryTabPrivilege{
		Grantee: "DST", Owner: "SRC", ObjectName: "S1", ObjectType: "SEQUENCE", Privilege: "SELECT", Grantable: "N",
	})
	routineGrant := dmpObjectGrantRecord(DictionaryTabPrivilege{
		Grantee: "DST", Owner: "SRC", ObjectName: "F1", ObjectType: "FUNCTION", Privilege: "EXECUTE", Grantable: "N",
	})
	packageGrant := dmpObjectGrantRecord(DictionaryTabPrivilege{
		Grantee: "DST", Owner: "SRC", ObjectName: "PKG1", ObjectType: "PACKAGE", Privilege: "EXECUTE", Grantable: "N",
	})
	if tableGrant.RecordType != dmpRecordObjectGrant || tableGrant.Grant.ObjectType != "TABLE" {
		t.Fatalf("unexpected table grant metadata %+v", tableGrant)
	}
	if viewGrant.RecordType != dmpRecordSchemaGrant || viewGrant.Grant.ObjectType != "VIEW" {
		t.Fatalf("unexpected view grant metadata %+v", viewGrant)
	}
	if sequenceGrant.RecordType != dmpRecordSchemaGrant || sequenceGrant.Grant.ObjectType != "SEQ" {
		t.Fatalf("unexpected sequence grant metadata %+v", sequenceGrant)
	}
	if routineGrant.RecordType != dmpRecordSchemaGrant || routineGrant.Grant.ObjectType != "PROC" {
		t.Fatalf("unexpected routine grant metadata %+v", routineGrant)
	}
	if packageGrant.RecordType != dmpRecordSchemaGrant || packageGrant.Grant.ObjectType != "PKG" {
		t.Fatalf("unexpected package grant metadata %+v", packageGrant)
	}

	path := filepath.Join(t.TempDir(), "grant-records.bin")
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	charset, err := dmpCharsetFromName("UTF-8")
	if err != nil {
		t.Fatal(err)
	}
	if err := writeDMPMetadataRecord(file, charset, tableGrant); err != nil {
		t.Fatal(err)
	}
	tableEnd, err := file.Seek(0, 1)
	if err != nil {
		t.Fatal(err)
	}
	if err := writeDMPMetadataRecord(file, charset, viewGrant); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := binary.LittleEndian.Uint16(raw[0:2]); got != dmpRecordObjectGrant {
		t.Fatalf("table grant record type=%d", got)
	}
	if got := binary.LittleEndian.Uint16(raw[int(tableEnd) : int(tableEnd)+2]); got != dmpRecordSchemaGrant {
		t.Fatalf("schema grant record type=%d", got)
	}
	if !bytes.HasSuffix(raw, []byte{4, 0, 0, 0, 'V', 'I', 'E', 'W'}) {
		t.Fatalf("schema grant is missing its native object-type suffix: %x", raw[int(tableEnd):])
	}
}

func TestWriteEncodedRowMatchesWriteRowBytes(t *testing.T) {
	dir := t.TempDir()
	longData := bytes.Repeat([]byte{0xAB}, int(dmpMaxShortFieldLength)+500)
	rows := [][]DMPField{
		{DMPShortField([]byte("hello")), DMPNullField(), DMPShortField(nil)},
		{DMPShortField(bytes.Repeat([]byte("x"), 4000)), DMPShortField([]byte("y")), DMPNullField()},
		{DMPLongField(longData), DMPShortField([]byte("tail")), DMPShortField([]byte("z"))},
	}
	build := func(name string, encoded bool) string {
		path := filepath.Join(dir, name)
		writer, err := NewDMPDataWriter(DMPDataOptions{
			OutputPath: path, Schema: "APP", Table: "T_EQ", TableID: 71, ColumnCount: 3,
		})
		if err != nil {
			t.Fatalf("NewDMPDataWriter failed: %v", err)
		}
		// Force frequent phase crossings so atomic-segment handling is hit.
		writer.phaseSizeLimit = 900
		for round := 0; round < 40; round++ {
			for _, fields := range rows {
				if encoded {
					segments, ok, err := encodeDMPRowSegments(fields, 3)
					if err != nil || !ok {
						t.Fatalf("encodeDMPRowSegments failed: ok=%v err=%v", ok, err)
					}
					if err := writer.WriteEncodedRow(segments); err != nil {
						t.Fatalf("WriteEncodedRow failed: %v", err)
					}
				} else if err := writer.WriteRow(fields); err != nil {
					t.Fatalf("WriteRow failed: %v", err)
				}
			}
		}
		info, err := writer.Close()
		if err != nil {
			t.Fatalf("Close failed: %v", err)
		}
		if !info.PayloadMD5Valid || !info.HeaderChecksumValid {
			t.Fatalf("dmp integrity is broken: %+v", info)
		}
		return path
	}
	plain, err := os.ReadFile(build("plain.dmp", false))
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := os.ReadFile(build("encoded.dmp", true))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(plain, encoded) {
		t.Fatalf("WriteEncodedRow output differs from WriteRow (len %d vs %d)", len(plain), len(encoded))
	}
}

func TestEncodeDMPRowSegmentsFallsBackForReaderFields(t *testing.T) {
	segments, ok, err := encodeDMPRowSegments([]DMPField{DMPLongReaderField(bytes.NewReader([]byte("s")), 1)}, 1)
	if err != nil || ok || segments != nil {
		t.Fatalf("reader fields must fall back to WriteRow: ok=%v err=%v segments=%v", ok, err, segments)
	}
	if _, _, err := encodeDMPRowSegments([]DMPField{DMPShortField([]byte("a"))}, 2); err == nil {
		t.Fatal("column count mismatch must fail at encode time")
	}
}
