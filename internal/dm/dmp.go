package dm

import (
	"bufio"
	"compress/zlib"
	"crypto/md5"
	"encoding/binary"
	"errors"
	"fmt"
	"hash"
	"io"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"golang.org/x/text/encoding/korean"
	"golang.org/x/text/encoding/simplifiedchinese"
	"golang.org/x/text/transform"
)

const (
	DMPHeaderSize            = 4096
	dmpPayloadEndOffset      = 0x10D
	dmpEncodingCodeOffset    = 0x115
	dmpDescriptionLenOffset  = 0x116
	dmpDescriptionOffset     = 0x118
	dmpCompressionOffset     = 0x318
	dmpRowFormatFlagOffset   = 0x320
	dmpCaseSensitiveOffset   = 0x435
	dmpExtentSizeOffset      = 0x436
	dmpPageSizeOffset        = 0x43A
	dmpCharsetFlagOffset     = 0x43E
	dmpPageCheckOffset       = 0x440
	dmpEncryptionNameOffset  = 0x745
	dmpBuildStringOffset     = 0x846
	dmpObjectCountOffset     = 0xA4A
	dmpPayloadMD5Offset      = 0xA56
	dmpCompressionLevelOff   = 0xA66
	dmpHeaderChecksumOffset  = 0xFFF
	dmpTableIndexMarker      = 0x9CD81630
	dmpSchemaIndexMarker     = 0x85F20CD5
	dmpFieldNull             = 0xFFFD
	dmpFieldLong             = 0xFFFE
	dmpMaxShortFieldLength   = dmpFieldNull - 1
	dmpCurrentLogicalVersion = 26
	dmpDataPhaseSizeLimit    = 8 << 20
	dmpStreamBufferSize      = 64 << 10
)

var dmpFooterMagic = [8]byte{0x9B, 0xA0, 0x78, 0xC6, 0xD5, 0x0C, 0xF2, 0x85}

type DMPExportMode uint32

const (
	DMPModeFull    DMPExportMode = 1
	DMPModeSchemas DMPExportMode = 2
	DMPModeTables  DMPExportMode = 3
	DMPModeOwner   DMPExportMode = 4
)

func (m DMPExportMode) String() string {
	switch m {
	case DMPModeFull:
		return "FULL"
	case DMPModeSchemas:
		return "SCHEMAS"
	case DMPModeTables:
		return "TABLES"
	case DMPModeOwner:
		return "OWNER"
	default:
		return fmt.Sprintf("UNKNOWN(%d)", uint32(m))
	}
}

type DMPInfo struct {
	Path                string
	FileSize            int64
	InternalVersion     uint32
	LogicalVersion      uint32
	Mode                DMPExportMode
	PayloadEnd          uint64
	Description         string
	Charset             string
	CharsetFlag         uint16
	EncodingCode        uint8
	CharsetHeaderValid  bool
	CaseSensitive       bool
	ExtentSize          uint32
	PageSize            uint32
	PageCheck           uint32
	Compressed          bool
	CompressionLevel    uint8
	RowFormatFlag       uint32
	ObjectCount         uint32
	EncryptionName      string
	BuildString         string
	StoredPayloadMD5    [md5.Size]byte
	ActualPayloadMD5    [md5.Size]byte
	PayloadMD5Valid     bool
	HeaderXOR           byte
	HeaderChecksumValid bool
	FooterMagicValid    bool
	SchemaRecordOffset  uint64
	Schema              string
	Owner               string
	Schemas             []DMPFooterSchema
	Tables              []DMPTableIndex
	FooterParseError    string
}

type DMPFooterSchema struct {
	RecordOffset uint64
	Name         string
	Owner        string
	Tables       []DMPTableIndex
}

type DMPTableIndex struct {
	ObjectID       uint32
	Schema         string
	Owner          string
	Name           string
	MetadataOffset uint64
	PhaseOffsets   []uint64
	RowCount       uint64
}

type dmpCharsetHeader struct {
	Name         string
	Flag         uint16
	EncodingCode uint8
}

var dmpCharsetHeaders = []dmpCharsetHeader{
	{Name: "GB18030", Flag: 0, EncodingCode: 10},
	{Name: "UTF-8", Flag: 1, EncodingCode: 1},
	{Name: "EUC-KR", Flag: 2, EncodingCode: 6},
}

func InspectDMP(path string) (*DMPInfo, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open dmp: %w", err)
	}
	defer file.Close()

	stat, err := file.Stat()
	if err != nil {
		return nil, fmt.Errorf("stat dmp: %w", err)
	}
	if stat.Size() < DMPHeaderSize {
		return nil, fmt.Errorf("dmp is shorter than the %d-byte header", DMPHeaderSize)
	}

	header := make([]byte, DMPHeaderSize)
	if _, err := io.ReadFull(file, header); err != nil {
		return nil, fmt.Errorf("read dmp header: %w", err)
	}
	charsetFlag := binary.LittleEndian.Uint16(header[dmpCharsetFlagOffset : dmpCharsetFlagOffset+2])
	encodingCode := header[dmpEncodingCodeOffset]
	charset, charsetValid := dmpCharsetFromHeader(charsetFlag, encodingCode)
	info := &DMPInfo{
		Path:               path,
		FileSize:           stat.Size(),
		InternalVersion:    binary.LittleEndian.Uint32(header[0:4]),
		Mode:               DMPExportMode(binary.LittleEndian.Uint32(header[8:12])),
		PayloadEnd:         binary.LittleEndian.Uint64(header[dmpPayloadEndOffset : dmpPayloadEndOffset+8]),
		Charset:            charset.Name,
		CharsetFlag:        charsetFlag,
		EncodingCode:       encodingCode,
		CharsetHeaderValid: charsetValid,
		CaseSensitive:      header[dmpCaseSensitiveOffset] != 0,
		ExtentSize:         binary.LittleEndian.Uint32(header[dmpExtentSizeOffset : dmpExtentSizeOffset+4]),
		PageSize:           binary.LittleEndian.Uint32(header[dmpPageSizeOffset : dmpPageSizeOffset+4]),
		PageCheck:          binary.LittleEndian.Uint32(header[dmpPageCheckOffset : dmpPageCheckOffset+4]),
		Compressed:         header[dmpCompressionOffset] != 0,
		CompressionLevel:   header[dmpCompressionLevelOff],
		RowFormatFlag:      binary.LittleEndian.Uint32(header[dmpRowFormatFlagOffset : dmpRowFormatFlagOffset+4]),
		ObjectCount:        binary.LittleEndian.Uint32(header[dmpObjectCountOffset : dmpObjectCountOffset+4]),
		EncryptionName:     dmpCString(header, dmpEncryptionNameOffset, 64),
		BuildString:        dmpCString(header, dmpBuildStringOffset, 128),
		HeaderXOR:          dmpXOR(header),
	}
	info.Description = decodeDMPText(dmpHeaderDescription(header), info.Charset)
	if info.InternalVersion >= 8 {
		info.LogicalVersion = info.InternalVersion - 8
	}
	copy(info.StoredPayloadMD5[:], header[dmpPayloadMD5Offset:dmpPayloadMD5Offset+md5.Size])
	info.HeaderChecksumValid = info.HeaderXOR == 0

	if info.PayloadEnd < DMPHeaderSize || info.PayloadEnd > uint64(stat.Size()) {
		return nil, fmt.Errorf("invalid dmp payload end 0x%X for file size %d", info.PayloadEnd, stat.Size())
	}
	if _, err := file.Seek(DMPHeaderSize, io.SeekStart); err != nil {
		return nil, fmt.Errorf("seek dmp payload: %w", err)
	}
	payloadHash := md5.New()
	if _, err := io.Copy(payloadHash, file); err != nil {
		return nil, fmt.Errorf("hash dmp payload: %w", err)
	}
	copy(info.ActualPayloadMD5[:], payloadHash.Sum(nil))
	info.PayloadMD5Valid = info.StoredPayloadMD5 == info.ActualPayloadMD5

	if err := inspectDMPFooter(file, info); err != nil {
		info.FooterParseError = err.Error()
	}
	return info, nil
}

func dmpHeaderDescription(header []byte) []byte {
	length := int(binary.LittleEndian.Uint16(header[dmpDescriptionLenOffset : dmpDescriptionLenOffset+2]))
	if length <= 0 || dmpDescriptionOffset+length > len(header) {
		return nil
	}
	return header[dmpDescriptionOffset : dmpDescriptionOffset+length]
}

func dmpCharsetFromHeader(flag uint16, encodingCode uint8) (dmpCharsetHeader, bool) {
	for _, charset := range dmpCharsetHeaders {
		if charset.Flag == flag && charset.EncodingCode == encodingCode {
			return charset, true
		}
	}
	return dmpCharsetHeader{Name: fmt.Sprintf("unknown(flag=%d,code=%d)", flag, encodingCode), Flag: flag, EncodingCode: encodingCode}, false
}

func dmpCharsetFromName(value string) (dmpCharsetHeader, error) {
	if strings.TrimSpace(value) == "" {
		value = "UTF-8"
	}
	normalized := normalizeCharsetToken(value)
	for _, charset := range dmpCharsetHeaders {
		if normalized == charset.Name || normalized == fmt.Sprintf("%d", charset.Flag) {
			return charset, nil
		}
	}
	return dmpCharsetHeader{}, fmt.Errorf("unsupported dmp charset %q; use utf-8, gb18030, or euc-kr", value)
}

func decodeDMPText(raw []byte, charset string) string {
	if len(raw) == 0 {
		return ""
	}
	if value, ok := decodeWithCharset(raw, charset); ok {
		return value
	}
	return string(raw)
}

func encodeDMPText(value string, charset dmpCharsetHeader) ([]byte, error) {
	if !utf8.ValidString(value) {
		return nil, fmt.Errorf("dmp text is not valid UTF-8")
	}
	if value == "" {
		return nil, nil
	}
	if charset.Name == "UTF-8" {
		return []byte(value), nil
	}
	var transformer transform.Transformer
	switch charset.Name {
	case "GB18030":
		transformer = simplifiedchinese.GB18030.NewEncoder()
	case "EUC-KR":
		transformer = korean.EUCKR.NewEncoder()
	default:
		return nil, fmt.Errorf("unsupported dmp charset %q", charset.Name)
	}
	encoded, _, err := transform.String(transformer, value)
	if err != nil {
		return nil, fmt.Errorf("encode dmp text as %s: %w", charset.Name, err)
	}
	return []byte(encoded), nil
}

func dmpCString(raw []byte, offset int, maxLength int) string {
	if offset < 0 || offset >= len(raw) || maxLength <= 0 {
		return ""
	}
	end := offset + maxLength
	if end > len(raw) {
		end = len(raw)
	}
	value := raw[offset:end]
	if nul := strings.IndexByte(string(value), 0); nul >= 0 {
		value = value[:nul]
	}
	return string(value)
}

func dmpXOR(raw []byte) byte {
	var result byte
	for _, value := range raw {
		result ^= value
	}
	return result
}

func inspectDMPFooter(file *os.File, info *DMPInfo) error {
	footerLength := info.FileSize - int64(info.PayloadEnd)
	if footerLength < int64(len(dmpFooterMagic)) {
		return fmt.Errorf("dmp footer is too short")
	}
	if footerLength > 64<<20 {
		return fmt.Errorf("dmp footer exceeds 64 MiB inspection limit")
	}
	footer := make([]byte, footerLength)
	if _, err := file.ReadAt(footer, int64(info.PayloadEnd)); err != nil {
		return fmt.Errorf("read dmp footer: %w", err)
	}
	info.FooterMagicValid = string(footer[:8]) == string(dmpFooterMagic[:])
	if !info.FooterMagicValid {
		return fmt.Errorf("dmp footer magic mismatch")
	}

	reader := dmpFooterReader{raw: footer, offset: 8, compressed: info.Compressed}
	firstSchema := true
	for reader.offset < len(reader.raw) {
		if !firstSchema {
			marker, err := reader.uint32()
			if err != nil {
				return err
			}
			if marker != dmpSchemaIndexMarker {
				return fmt.Errorf("unexpected footer schema marker 0x%08X at +0x%X", marker, reader.offset-4)
			}
		}
		firstSchema = false
		if _, err := reader.uint16(); err != nil {
			return err
		}
		schemaRecordOffset, err := reader.uint64()
		if err != nil {
			return err
		}
		schemaRaw, err := reader.bytes16()
		if err != nil {
			return err
		}
		ownerRaw, err := reader.bytes16()
		if err != nil {
			return err
		}
		schema := DMPFooterSchema{
			RecordOffset: schemaRecordOffset,
			Name:         decodeDMPText(schemaRaw, info.Charset),
			Owner:        decodeDMPText(ownerRaw, info.Charset),
		}
		for reader.offset < len(reader.raw) {
			if len(reader.raw)-reader.offset >= 4 && binary.LittleEndian.Uint32(reader.raw[reader.offset:]) == dmpSchemaIndexMarker {
				break
			}
			table, err := inspectDMPFooterTable(file, info, &reader, schema.Name, schema.Owner)
			if err != nil {
				return err
			}
			schema.Tables = append(schema.Tables, table)
			info.Tables = append(info.Tables, table)
		}
		info.Schemas = append(info.Schemas, schema)
	}
	if len(info.Schemas) > 0 {
		info.SchemaRecordOffset = info.Schemas[0].RecordOffset
		info.Schema = info.Schemas[0].Name
		info.Owner = info.Schemas[0].Owner
	}
	return nil
}

func inspectDMPFooterTable(file *os.File, info *DMPInfo, reader *dmpFooterReader, schema string, owner string) (DMPTableIndex, error) {
	marker, err := reader.uint32()
	if err != nil {
		return DMPTableIndex{}, err
	}
	if marker != dmpTableIndexMarker {
		return DMPTableIndex{}, fmt.Errorf("unexpected footer table marker 0x%08X at +0x%X", marker, reader.offset-4)
	}
	if _, err := reader.uint16(); err != nil {
		return DMPTableIndex{}, err
	}
	metadataOffset, err := reader.uint64()
	if err != nil {
		return DMPTableIndex{}, err
	}
	objectID, err := reader.uint32()
	if err != nil {
		return DMPTableIndex{}, err
	}
	nameRaw, err := reader.bytes16()
	if err != nil {
		return DMPTableIndex{}, err
	}
	phaseCount, err := reader.uint32()
	if err != nil {
		return DMPTableIndex{}, err
	}
	if phaseCount > 1<<20 {
		return DMPTableIndex{}, fmt.Errorf("unreasonable dmp phase count %d", phaseCount)
	}
	table := DMPTableIndex{
		ObjectID: objectID, Schema: schema, Owner: owner,
		Name: decodeDMPText(nameRaw, info.Charset), MetadataOffset: metadataOffset,
	}
	for i := uint32(0); i < phaseCount; i++ {
		if _, err := reader.uint16(); err != nil {
			return DMPTableIndex{}, err
		}
		phaseOffset, err := reader.uint64()
		if err != nil {
			return DMPTableIndex{}, err
		}
		table.PhaseOffsets = append(table.PhaseOffsets, phaseOffset)
		if rows, ok := dmpPhaseRowCount(file, phaseOffset, info.FileSize); ok {
			table.RowCount += uint64(rows)
		}
	}
	return table, nil
}

func dmpPhaseRowCount(file *os.File, offset uint64, fileSize int64) (uint32, bool) {
	if offset > uint64(fileSize-20) {
		return 0, false
	}
	header := make([]byte, 20)
	if _, err := file.ReadAt(header, int64(offset)); err != nil {
		return 0, false
	}
	if binary.LittleEndian.Uint16(header[0:2]) != 2 || binary.LittleEndian.Uint16(header[2:4]) != 0xFFFF {
		return 0, false
	}
	if binary.LittleEndian.Uint32(header[8:12]) < 2 {
		return 0, false
	}
	rows := binary.LittleEndian.Uint32(header[16:20])
	if rows == ^uint32(0) {
		return 0, false
	}
	return rows, true
}

type dmpFooterReader struct {
	raw        []byte
	offset     int
	compressed bool
}

func (r *dmpFooterReader) take(length int) ([]byte, error) {
	if length < 0 || r.offset+length > len(r.raw) {
		return nil, io.ErrUnexpectedEOF
	}
	value := r.raw[r.offset : r.offset+length]
	r.offset += length
	return value, nil
}

func (r *dmpFooterReader) uint16() (uint16, error) {
	raw, err := r.take(2)
	if err != nil {
		return 0, err
	}
	return binary.LittleEndian.Uint16(raw), nil
}

func (r *dmpFooterReader) uint32() (uint32, error) {
	raw, err := r.take(4)
	if err != nil {
		return 0, err
	}
	return binary.LittleEndian.Uint32(raw), nil
}

func (r *dmpFooterReader) uint64() (uint64, error) {
	raw, err := r.take(8)
	if err != nil {
		return 0, err
	}
	return binary.LittleEndian.Uint64(raw), nil
}

func (r *dmpFooterReader) bytes16() ([]byte, error) {
	length, err := r.uint16()
	if err != nil {
		return nil, err
	}
	raw, err := r.take(int(length))
	if err != nil {
		return nil, err
	}
	if !r.compressed {
		return raw, nil
	}
	reader, err := zlib.NewReader(strings.NewReader(string(raw)))
	if err != nil {
		return nil, fmt.Errorf("open compressed dmp string: %w", err)
	}
	decompressed, readErr := io.ReadAll(reader)
	closeErr := reader.Close()
	if readErr != nil {
		return nil, fmt.Errorf("read compressed dmp string: %w", readErr)
	}
	if closeErr != nil {
		return nil, fmt.Errorf("close compressed dmp string: %w", closeErr)
	}
	return decompressed, nil
}

type DMPDataOptions struct {
	OutputPath     string
	LogicalVersion uint32
	RowFormatFlag  uint32
	Charset        string
	CaseSensitive  *bool
	ExtentSize     uint32
	PageSize       uint32
	PageCheck      *uint32
	Schema         string
	Table          string
	TableID        uint32
	ColumnCount    uint16
}

type DMPField struct {
	Data   []byte
	Reader io.Reader
	Length uint64
	Null   bool
	Long   bool
}

func DMPNullField() DMPField {
	return DMPField{Null: true}
}

func DMPShortField(data []byte) DMPField {
	return DMPField{Data: data}
}

func DMPLongField(data []byte) DMPField {
	return DMPField{Data: data, Long: true}
}

func DMPLongReaderField(reader io.Reader, length uint64) DMPField {
	return DMPField{Reader: reader, Length: length, Long: true}
}

type dmpDataPhase struct {
	offset        uint64
	number        uint32
	rowsCompleted uint32
	active        bool
}

type DMPDataWriter struct {
	file *os.File
	// bw buffers sequential appends; offset tracks the logical end of the
	// appended stream so phase accounting needs no Seek syscalls. Every
	// sequential write must go through (*DMPDataWriter).Write; WriteAt
	// patches and payload hashing flush the buffer first.
	bw             *bufio.Writer
	offset         uint64
	opts           DMPDataOptions
	phase1Offset   uint64
	phase2Offset   uint64
	rowCount       uint32
	charset        dmpCharsetHeader
	schemaBytes    []byte
	tableBytes     []byte
	phaseOffsets   []uint64
	currentPhase   dmpDataPhase
	phaseSizeLimit uint64
	inRow          bool
	closed         bool
}

const dmpWriterBufferSize = 512 << 10

// Write appends to the dump through the buffer and keeps the logical offset
// in sync. It is the only path sequential dump bytes may take.
func (w *DMPDataWriter) Write(p []byte) (int, error) {
	n, err := w.bw.Write(p)
	w.offset += uint64(n)
	return n, err
}

func (w *DMPDataWriter) flush() error {
	if w.bw == nil {
		return nil
	}
	return w.bw.Flush()
}

func NewDMPDataWriter(opts DMPDataOptions) (*DMPDataWriter, error) {
	if strings.TrimSpace(opts.OutputPath) == "" {
		return nil, fmt.Errorf("dmp output path is required")
	}
	if opts.LogicalVersion == 0 {
		opts.LogicalVersion = dmpCurrentLogicalVersion
	}
	if opts.LogicalVersion < 9 || opts.LogicalVersion > dmpCurrentLogicalVersion {
		return nil, fmt.Errorf("unsupported dmp logical version %d", opts.LogicalVersion)
	}
	if opts.RowFormatFlag == 0 {
		opts.RowFormatFlag = 1
	}
	if opts.RowFormatFlag != 1 {
		return nil, fmt.Errorf("unsupported dmp row-format flag %d; only the verified value 1 is currently writable", opts.RowFormatFlag)
	}
	if opts.ExtentSize == 0 {
		opts.ExtentSize = 16
	}
	if opts.PageSize == 0 {
		opts.PageSize = 8192
	}
	charset, err := dmpCharsetFromName(opts.Charset)
	if err != nil {
		return nil, err
	}
	opts.Charset = charset.Name
	if opts.Schema == "" || opts.Table == "" {
		return nil, fmt.Errorf("dmp schema and table names are required")
	}
	schemaBytes, err := encodeDMPText(opts.Schema, charset)
	if err != nil {
		return nil, fmt.Errorf("encode dmp schema: %w", err)
	}
	tableBytes, err := encodeDMPText(opts.Table, charset)
	if err != nil {
		return nil, fmt.Errorf("encode dmp table: %w", err)
	}
	if len(schemaBytes) > 0xFFFF || len(tableBytes) > 0xFFFF {
		return nil, fmt.Errorf("dmp schema or table name is too long")
	}
	if opts.ColumnCount == 0 {
		return nil, fmt.Errorf("dmp column count must be positive")
	}
	if dir := filepath.Dir(opts.OutputPath); dir != "." && dir != "" {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return nil, fmt.Errorf("create dmp output directory: %w", err)
		}
	}
	file, err := os.Create(opts.OutputPath)
	if err != nil {
		return nil, fmt.Errorf("create dmp output: %w", err)
	}
	writer := &DMPDataWriter{
		file: file, bw: bufio.NewWriterSize(file, dmpWriterBufferSize),
		opts: opts, phase1Offset: DMPHeaderSize,
		charset: charset, schemaBytes: schemaBytes, tableBytes: tableBytes,
		phaseSizeLimit: dmpDataPhaseSizeLimit,
	}
	if err := writer.writePreamble(); err != nil {
		_ = writer.Abort()
		return nil, err
	}
	return writer, nil
}

func (w *DMPDataWriter) writePreamble() error {
	if _, err := w.Write(make([]byte, DMPHeaderSize)); err != nil {
		return fmt.Errorf("write dmp header placeholder: %w", err)
	}
	if err := writeDMPUint16(w, 2); err != nil {
		return err
	}
	if err := writeDMPUint16(w, 0xFFFF); err != nil {
		return err
	}
	for _, value := range []uint32{w.opts.TableID, 1, 0, 0} {
		if err := writeDMPUint32(w, value); err != nil {
			return err
		}
	}
	if _, err := w.Write([]byte{0}); err != nil {
		return err
	}
	if err := writeDMPString32(w, w.tableBytes); err != nil {
		return err
	}
	if err := writeDMPUint16(w, 14); err != nil {
		return err
	}
	if err := writeDMPUint16(w, 0xFFFF); err != nil {
		return err
	}
	if err := writeDMPUint16(w, w.opts.ColumnCount); err != nil {
		return err
	}
	w.phase2Offset = w.offset
	w.phaseOffsets = append(w.phaseOffsets, w.phase1Offset)
	return w.startDataPhase()
}

func (w *DMPDataWriter) WriteRow(fields []DMPField) error {
	if w.closed {
		return fmt.Errorf("dmp writer is closed")
	}
	if w.file == nil {
		if err := w.resume(); err != nil {
			return err
		}
	}
	if len(fields) != int(w.opts.ColumnCount) {
		return fmt.Errorf("dmp row has %d fields, want %d", len(fields), w.opts.ColumnCount)
	}
	if w.rowCount == ^uint32(0) {
		return fmt.Errorf("dmp row count exceeds uint32")
	}
	if err := w.beginRowPhase(); err != nil {
		return err
	}
	w.inRow = true
	for _, field := range fields {
		if field.Null {
			if field.Reader != nil || len(field.Data) != 0 || field.Length != 0 {
				return fmt.Errorf("null dmp field cannot contain data")
			}
			if err := writeDMPUint16(w, dmpFieldNull); err != nil {
				return err
			}
			continue
		}
		if field.Reader != nil && field.Data != nil {
			return fmt.Errorf("dmp field cannot use Data and Reader together")
		}
		length := uint64(len(field.Data))
		if field.Reader != nil {
			length = field.Length
		} else if field.Length != 0 && field.Length != length {
			return fmt.Errorf("dmp field length %d does not match data length %d", field.Length, length)
		}
		if field.Long || length > dmpMaxShortFieldLength {
			if err := writeDMPUint16(w, dmpFieldLong); err != nil {
				return err
			}
			if err := writeDMPUint64(w, length); err != nil {
				return err
			}
		} else {
			if err := writeDMPUint16(w, uint16(length)); err != nil {
				return err
			}
		}
		if field.Reader != nil {
			if err := w.writeStreamedField(field.Reader, length); err != nil {
				return fmt.Errorf("write streamed dmp field: %w", err)
			}
		} else if _, err := w.Write(field.Data); err != nil {
			return fmt.Errorf("write dmp field: %w", err)
		}
	}
	w.inRow = false
	w.currentPhase.rowsCompleted++
	w.rowCount++
	return nil
}

// dmpRowSegment is one wire chunk of a pre-encoded row. Atomic segments
// (null markers, length prefixes) must not straddle a phase boundary, exactly
// like WriteRow's writePhaseUint16 / long-field header handling; data
// segments may be split across phases freely.
type dmpRowSegment struct {
	atomic bool
	data   []byte
}

var dmpNullFieldMarker = []byte{0xFD, 0xFF}

// encodeDMPRowSegments pre-encodes a row into wire segments so decode workers
// carry the field-encoding cost instead of the single writer goroutine. Rows
// with streaming (Reader) fields return ok=false and take the WriteRow path,
// keeping huge LOBs out of memory. The segment stream replayed by
// WriteEncodedRow is byte-identical to WriteRow's output.
func encodeDMPRowSegments(fields []DMPField, columnCount uint16) ([]dmpRowSegment, bool, error) {
	if len(fields) != int(columnCount) {
		return nil, false, fmt.Errorf("dmp row has %d fields, want %d", len(fields), columnCount)
	}
	segments := make([]dmpRowSegment, 0, len(fields)*2)
	for _, field := range fields {
		if field.Reader != nil {
			return nil, false, nil
		}
		if field.Null {
			if len(field.Data) != 0 || field.Length != 0 {
				return nil, false, fmt.Errorf("null dmp field cannot contain data")
			}
			segments = append(segments, dmpRowSegment{atomic: true, data: dmpNullFieldMarker})
			continue
		}
		length := uint64(len(field.Data))
		if field.Length != 0 && field.Length != length {
			return nil, false, fmt.Errorf("dmp field length %d does not match data length %d", field.Length, length)
		}
		if field.Long || length > dmpMaxShortFieldLength {
			header := make([]byte, 10)
			binary.LittleEndian.PutUint16(header[0:2], dmpFieldLong)
			binary.LittleEndian.PutUint64(header[2:10], length)
			segments = append(segments, dmpRowSegment{atomic: true, data: header})
		} else {
			header := make([]byte, 2)
			binary.LittleEndian.PutUint16(header, uint16(length))
			segments = append(segments, dmpRowSegment{atomic: true, data: header})
		}
		if len(field.Data) > 0 {
			segments = append(segments, dmpRowSegment{atomic: false, data: field.Data})
		}
	}
	return segments, true, nil
}

// WriteEncodedRow appends a row pre-encoded by encodeDMPRowSegments. Field
// validation already happened at encode time; this only replays the segments
// with the same phase-boundary rules as WriteRow.
func (w *DMPDataWriter) WriteEncodedRow(segments []dmpRowSegment) error {
	if w.closed {
		return fmt.Errorf("dmp writer is closed")
	}
	if w.file == nil {
		if err := w.resume(); err != nil {
			return err
		}
	}
	if w.rowCount == ^uint32(0) {
		return fmt.Errorf("dmp row count exceeds uint32")
	}
	if err := w.beginRowPhase(); err != nil {
		return err
	}
	w.inRow = true
	for _, segment := range segments {
		if _, err := w.Write(segment.data); err != nil {
			return fmt.Errorf("write dmp field: %w", err)
		}
	}
	w.inRow = false
	w.currentPhase.rowsCompleted++
	w.rowCount++
	return nil
}

func (w *DMPDataWriter) startDataPhase() error {
	if uint64(len(w.phaseOffsets)) >= uint64(^uint32(0)) {
		return fmt.Errorf("dmp phase count exceeds uint32")
	}
	offset := w.offset
	number := uint32(len(w.phaseOffsets) + 1)
	w.phaseOffsets = append(w.phaseOffsets, offset)
	w.currentPhase = dmpDataPhase{
		offset: offset, number: number, active: true,
	}
	if err := writeDMPUint16(w, 2); err != nil {
		return err
	}
	if err := writeDMPUint16(w, 0xFFFF); err != nil {
		return err
	}
	for _, value := range []uint32{w.opts.TableID, number, 0, 0} {
		if err := writeDMPUint32(w, value); err != nil {
			return err
		}
	}
	if _, err := w.Write([]byte{0}); err != nil {
		return err
	}
	if err := writeDMPString32(w, w.tableBytes); err != nil {
		return err
	}
	if w.offset-offset >= w.phaseSizeLimit {
		return fmt.Errorf("dmp phase size limit %d is too small for the phase header", w.phaseSizeLimit)
	}
	return nil
}

func (w *DMPDataWriter) finishDataPhase() error {
	if !w.currentPhase.active {
		return nil
	}
	length := w.offset - w.currentPhase.offset
	if length > uint64(^uint32(0)) {
		return fmt.Errorf("dmp table phase exceeds uint32 length")
	}
	rows := w.currentPhase.rowsCompleted
	if err := w.flush(); err != nil {
		return err
	}
	if err := patchDMPUint32(w.file, int64(w.currentPhase.offset+12), uint32(length)); err != nil {
		return err
	}
	if err := patchDMPUint32(w.file, int64(w.currentPhase.offset+16), rows); err != nil {
		return err
	}
	w.currentPhase.active = false
	return nil
}

// beginRowPhase closes the current phase and opens the next one when the size
// threshold is already reached. It is called only between rows, never inside
// one: official dexp dumps overshoot the 8 MiB limit to finish the row in
// progress (observed phase lengths 8493595 / 8388757 / 8540709 against a
// 8388608 limit), and dimp rejects the whole table with "data abnormal" if a
// phase starts mid-row. Cutting at exactly 8 MiB made every table larger than
// one phase unimportable.
func (w *DMPDataWriter) beginRowPhase() error {
	if !w.currentPhase.active {
		return nil
	}
	if w.offset-w.currentPhase.offset < w.phaseSizeLimit {
		return nil
	}
	if err := w.finishDataPhase(); err != nil {
		return err
	}
	return w.startDataPhase()
}

// writeStreamedField copies a LOB straight through the output buffer. Like
// every other field write it stays inside the current phase, however large the
// value is.
func (w *DMPDataWriter) writeStreamedField(reader io.Reader, length uint64) error {
	buffer := make([]byte, dmpStreamBufferSize)
	for length > 0 {
		chunk := length
		if chunk > uint64(len(buffer)) {
			chunk = uint64(len(buffer))
		}
		if _, err := io.ReadFull(reader, buffer[:int(chunk)]); err != nil {
			return err
		}
		if _, err := w.Write(buffer[:int(chunk)]); err != nil {
			return err
		}
		length -= chunk
	}
	return nil
}

func (w *DMPDataWriter) Close() (*DMPInfo, error) {
	if w.closed {
		return nil, fmt.Errorf("dmp writer is closed")
	}
	if w.file == nil {
		if err := w.resume(); err != nil {
			return nil, err
		}
	}
	w.closed = true
	if w.inRow {
		_ = w.file.Close()
		return nil, fmt.Errorf("cannot close dmp writer with an incomplete row")
	}
	if err := w.finishDataPhase(); err != nil {
		_ = w.file.Close()
		return nil, err
	}
	payloadEnd := w.offset
	phase1Length := w.phase2Offset - w.phase1Offset
	if phase1Length > uint64(^uint32(0)) {
		_ = w.file.Close()
		return nil, fmt.Errorf("dmp table phase exceeds uint32 length")
	}
	if err := w.writeFooter(); err != nil {
		_ = w.file.Close()
		return nil, err
	}
	if err := w.flush(); err != nil {
		_ = w.file.Close()
		return nil, err
	}
	if err := patchDMPUint32(w.file, int64(w.phase1Offset+12), uint32(phase1Length)); err != nil {
		_ = w.file.Close()
		return nil, err
	}
	payloadHash, err := hashDMPPayload(w.file)
	if err != nil {
		_ = w.file.Close()
		return nil, err
	}
	header := make([]byte, DMPHeaderSize)
	binary.LittleEndian.PutUint32(header[0:4], w.opts.LogicalVersion+8)
	binary.LittleEndian.PutUint32(header[4:8], 1)
	binary.LittleEndian.PutUint32(header[8:12], uint32(DMPModeTables))
	binary.LittleEndian.PutUint64(header[dmpPayloadEndOffset:dmpPayloadEndOffset+8], payloadEnd)
	header[dmpEncodingCodeOffset] = w.charset.EncodingCode
	binary.LittleEndian.PutUint32(header[dmpRowFormatFlagOffset:dmpRowFormatFlagOffset+4], w.opts.RowFormatFlag)
	caseSensitive := true
	if w.opts.CaseSensitive != nil {
		caseSensitive = *w.opts.CaseSensitive
	}
	if caseSensitive {
		header[dmpCaseSensitiveOffset] = 1
	}
	binary.LittleEndian.PutUint32(header[dmpExtentSizeOffset:dmpExtentSizeOffset+4], w.opts.ExtentSize)
	binary.LittleEndian.PutUint32(header[dmpPageSizeOffset:dmpPageSizeOffset+4], w.opts.PageSize)
	binary.LittleEndian.PutUint16(header[dmpCharsetFlagOffset:dmpCharsetFlagOffset+2], w.charset.Flag)
	pageCheck := uint32(3)
	if w.opts.PageCheck != nil {
		pageCheck = *w.opts.PageCheck
	}
	binary.LittleEndian.PutUint32(header[dmpPageCheckOffset:dmpPageCheckOffset+4], pageCheck)
	binary.LittleEndian.PutUint32(header[dmpObjectCountOffset:dmpObjectCountOffset+4], 0)
	copy(header[dmpPayloadMD5Offset:dmpPayloadMD5Offset+md5.Size], payloadHash.Sum(nil))
	header[dmpHeaderChecksumOffset] = dmpXOR(header)
	if _, err := w.file.WriteAt(header, 0); err != nil {
		_ = w.file.Close()
		return nil, fmt.Errorf("write final dmp header: %w", err)
	}
	if err := w.file.Sync(); err != nil {
		_ = w.file.Close()
		return nil, fmt.Errorf("sync dmp output: %w", err)
	}
	if err := w.file.Close(); err != nil {
		return nil, fmt.Errorf("close dmp output: %w", err)
	}
	w.file = nil
	return InspectDMP(w.opts.OutputPath)
}

// suspend releases the operating-system file handle without finalizing the
// dump. The writer can be resumed later, which keeps per-table DMP exports
// bounded when a database contains thousands of tables.
func (w *DMPDataWriter) suspend() error {
	if w == nil || w.closed || w.file == nil {
		return nil
	}
	if w.inRow {
		return fmt.Errorf("cannot suspend dmp writer with an incomplete row")
	}
	if err := w.flush(); err != nil {
		return fmt.Errorf("flush dmp output before suspend: %w", err)
	}
	if err := w.file.Sync(); err != nil {
		return fmt.Errorf("sync dmp output before suspend: %w", err)
	}
	if err := w.file.Close(); err != nil {
		return fmt.Errorf("suspend dmp output: %w", err)
	}
	w.file = nil
	return nil
}

func (w *DMPDataWriter) resume() error {
	if w == nil || w.closed {
		return fmt.Errorf("dmp writer is closed")
	}
	if w.file != nil {
		return nil
	}
	file, err := os.OpenFile(w.opts.OutputPath, os.O_RDWR, 0644)
	if err != nil {
		return fmt.Errorf("resume dmp output: %w", err)
	}
	end, err := file.Seek(0, io.SeekEnd)
	if err != nil {
		_ = file.Close()
		return fmt.Errorf("seek resumed dmp output: %w", err)
	}
	if uint64(end) != w.offset {
		_ = file.Close()
		return fmt.Errorf("resumed dmp output size %d does not match writer offset %d", end, w.offset)
	}
	w.file = file
	w.bw = bufio.NewWriterSize(file, dmpWriterBufferSize)
	return nil
}

func (w *DMPDataWriter) writeFooter() error {
	if _, err := w.Write(dmpFooterMagic[:]); err != nil {
		return err
	}
	if err := writeDMPUint16(w, 0); err != nil {
		return err
	}
	if err := writeDMPUint64(w, w.phase1Offset); err != nil {
		return err
	}
	if err := writeDMPString16(w, w.schemaBytes); err != nil {
		return err
	}
	if err := writeDMPString16(w, w.schemaBytes); err != nil {
		return err
	}
	if err := writeDMPUint32(w, dmpTableIndexMarker); err != nil {
		return err
	}
	if err := writeDMPUint16(w, 0); err != nil {
		return err
	}
	if err := writeDMPUint64(w, w.phase1Offset); err != nil {
		return err
	}
	if err := writeDMPUint32(w, w.opts.TableID); err != nil {
		return err
	}
	if err := writeDMPString16(w, w.tableBytes); err != nil {
		return err
	}
	if err := writeDMPUint32(w, uint32(len(w.phaseOffsets))); err != nil {
		return err
	}
	for _, offset := range w.phaseOffsets {
		if err := writeDMPUint16(w, 0); err != nil {
			return err
		}
		if err := writeDMPUint64(w, offset); err != nil {
			return err
		}
	}
	return nil
}

func (w *DMPDataWriter) Abort() error {
	if w.closed {
		return nil
	}
	w.closed = true
	var closeErr error
	if w.file != nil {
		closeErr = w.file.Close()
	}
	w.file = nil
	removeErr := os.Remove(w.opts.OutputPath)
	return errors.Join(closeErr, removeErr)
}

func hashDMPPayload(file *os.File) (hash.Hash, error) {
	if _, err := file.Seek(DMPHeaderSize, io.SeekStart); err != nil {
		return nil, err
	}
	payloadHash := md5.New()
	if _, err := io.Copy(payloadHash, file); err != nil {
		return nil, err
	}
	return payloadHash, nil
}

func writeDMPUint16(writer io.Writer, value uint16) error {
	var raw [2]byte
	binary.LittleEndian.PutUint16(raw[:], value)
	_, err := writer.Write(raw[:])
	return err
}

func writeDMPUint32(writer io.Writer, value uint32) error {
	var raw [4]byte
	binary.LittleEndian.PutUint32(raw[:], value)
	_, err := writer.Write(raw[:])
	return err
}

func writeDMPUint64(writer io.Writer, value uint64) error {
	var raw [8]byte
	binary.LittleEndian.PutUint64(raw[:], value)
	_, err := writer.Write(raw[:])
	return err
}

func writeDMPString16(writer io.Writer, value []byte) error {
	if len(value) > 0xFFFF {
		return fmt.Errorf("dmp string exceeds uint16 length")
	}
	if err := writeDMPUint16(writer, uint16(len(value))); err != nil {
		return err
	}
	_, err := writer.Write(value)
	return err
}

func writeDMPString32(writer io.Writer, value []byte) error {
	if uint64(len(value)) > uint64(^uint32(0)) {
		return fmt.Errorf("dmp string exceeds uint32 length")
	}
	if err := writeDMPUint32(writer, uint32(len(value))); err != nil {
		return err
	}
	_, err := writer.Write(value)
	return err
}

func patchDMPUint32(file *os.File, offset int64, value uint32) error {
	var raw [4]byte
	binary.LittleEndian.PutUint32(raw[:], value)
	if _, err := file.WriteAt(raw[:], offset); err != nil {
		return fmt.Errorf("patch dmp uint32 at 0x%X: %w", offset, err)
	}
	return nil
}
