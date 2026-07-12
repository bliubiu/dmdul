package dm

import (
	"bytes"
	"compress/zlib"
	"crypto/md5"
	"encoding/binary"
	"os"
	"path/filepath"
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

func TestDMPDataWriterSplitsAndStreamsLongField(t *testing.T) {
	path := filepath.Join(t.TempDir(), "streamed.dmp")
	writer, err := NewDMPDataWriter(DMPDataOptions{
		OutputPath: path, Schema: "APP", Table: "T_STREAM", TableID: 91, ColumnCount: 2,
	})
	if err != nil {
		t.Fatalf("NewDMPDataWriter failed: %v", err)
	}
	writer.phaseSizeLimit = 256
	longValue := bytes.Repeat([]byte("0123456789ABCDEF"), 256)
	if err := writer.WriteRow([]DMPField{
		DMPShortField([]byte("1")),
		DMPLongReaderField(bytes.NewReader(longValue), uint64(len(longValue))),
	}); err != nil {
		t.Fatalf("WriteRow failed: %v", err)
	}
	info, err := writer.Close()
	if err != nil {
		t.Fatalf("Close failed: %v", err)
	}
	if len(info.Tables) != 1 || info.Tables[0].RowCount != 1 {
		t.Fatalf("unexpected streamed table info %+v", info.Tables)
	}
	if len(info.Tables[0].PhaseOffsets) <= 2 {
		t.Fatalf("long streamed field should span several phases: %+v", info.Tables[0].PhaseOffsets)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile failed: %v", err)
	}
	firstDataPhase := info.Tables[0].PhaseOffsets[1]
	if rows := binary.LittleEndian.Uint32(raw[firstDataPhase+16 : firstDataPhase+20]); rows != ^uint32(0) {
		t.Fatalf("incomplete first data phase row marker = %d, want 0xFFFFFFFF", rows)
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
	start, err := writer.file.Seek(0, 1)
	if err != nil {
		t.Fatal(err)
	}
	length := uint64(1)<<32 + 123
	err = writer.WriteRow([]DMPField{DMPLongReaderField(bytes.NewReader(nil), length)})
	if err == nil {
		t.Fatal("empty reader should fail before writing a 4 GiB payload")
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
