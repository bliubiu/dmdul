package dm

import (
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"os"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"
)

const (
	sysHPartBaseTableIDOffset = 0x05
	sysHPartPartTableIDOffset = 0x09
	sysHPartTypeOffset        = 0x1A
)

type PartitionScanOptions struct {
	SystemPath  string
	ControlPath string
	OwnerFilter string
	Charset     string
}

type PartitionScanResult struct {
	SystemPath     string
	ExtentSize     uint32
	PageSize       uint32
	PageCount      uint32
	ObjectCount    int
	TableCount     int
	PartitionCount int
	Tables         []PartitionedTable
}

type PartitionedTable struct {
	Owner      string
	Name       string
	TableID    uint32
	Partitions []PartitionInfo
}

type PartitionInfo struct {
	BaseTableID      uint32
	PartTableID      uint32
	Type             string
	Name             string
	HighValue        []byte
	HighValuePreview string
	HighValueHex     string
	PageNo           uint32
	SlotNo           uint16
	SlotOffset       uint16
	RowOffset        uint64
}

type partitionDef struct {
	BaseTableID uint32
	PartTableID uint32
	Type        string
	Name        string
	HighValue   []byte
	Location    ddlLocation
}

func ScanPartitions(opts PartitionScanOptions) (*PartitionScanResult, error) {
	if opts.SystemPath == "" {
		return nil, fmt.Errorf("scan-partitions requires SYSTEM.DBF path")
	}

	data, err := os.ReadFile(opts.SystemPath)
	if err != nil {
		return nil, fmt.Errorf("read SYSTEM.DBF: %w", err)
	}
	if len(data) < systemHeaderReadSize {
		return nil, fmt.Errorf("SYSTEM.DBF is too small")
	}

	pageSize, _ := detectSystemPageSize(data[:systemHeaderReadSize], int64(len(data)))
	pageCount, _ := detectSystemPageCount(data[:systemHeaderReadSize], int64(len(data)), pageSize)
	extentSize, _ := detectSystemExtentSize(data[:systemHeaderReadSize])
	restoreSystemPages(data, pageSize)
	preferredCharset := strings.ToLower(strings.TrimSpace(opts.Charset))
	if preferredCharset == "" || preferredCharset == "auto" {
		if charset, ok := detectSystemCharsetFromData(data, pageSize); ok && charset.DecoderName != "" {
			preferredCharset = charset.DecoderName
		}
	}
	decoder := textDecoder{preferred: preferredCharset}
	matcher := newOwnerMatcher(opts.OwnerFilter)

	objects := scanDictionaryObjects(data, pageSize, decoder)
	schemaNames := schemaNamesFromDictionaryObjects(objects)
	for id, obj := range objects {
		obj.Owner = resolveSchemaName(obj.SchemaID, schemaNames)
		objects[id] = obj
	}

	tables := make(map[uint32]dictionaryObject)
	for _, obj := range objects {
		if obj.Type == "SCHOBJ" && obj.Subtype == "UTAB" {
			tables[obj.ID] = obj
		}
	}

	partitionsByTable := scanPartitionsByTable(data, pageSize, decoder, tables, matcher)

	result := &PartitionScanResult{
		SystemPath:     opts.SystemPath,
		ExtentSize:     extentSize,
		PageSize:       pageSize,
		PageCount:      pageCount,
		ObjectCount:    len(objects),
		TableCount:     len(partitionsByTable),
		PartitionCount: countPartitions(partitionsByTable),
		Tables:         partitionedTablesFromMap(tables, partitionsByTable),
	}
	return result, nil
}

func scanPartitionsByTable(data []byte, pageSize uint32, decoder textDecoder, tables map[uint32]dictionaryObject, matcher ownerMatcher) map[uint32][]PartitionInfo {
	partitionsByTable := make(map[uint32][]PartitionInfo)
	iterDictionarySlotRanges(data, pageSize, func(page []byte, pageNo uint32, slotNo uint16, slotOff uint16, nextOff uint16) {
		for rowOff := int(slotOff); rowOff+sysHPartTypeOffset+2 < int(nextOff); rowOff++ {
			part, ok := parseDDLPartitionRowAt(page, rowOff, pageNo, slotNo, slotOff, pageSize, decoder)
			if !ok {
				continue
			}
			table, ok := tables[part.BaseTableID]
			if !ok || !matcher.allowed(table.Owner) {
				continue
			}
			partitionsByTable[part.BaseTableID] = append(partitionsByTable[part.BaseTableID], PartitionInfo{
				BaseTableID:      part.BaseTableID,
				PartTableID:      part.PartTableID,
				Type:             part.Type,
				Name:             part.Name,
				HighValue:        append([]byte(nil), part.HighValue...),
				HighValuePreview: partitionHighValuePreview(part.HighValue),
				HighValueHex:     partitionHighValueHex(part.HighValue, 64),
				PageNo:           part.Location.PageNo,
				SlotNo:           part.Location.SlotNo,
				SlotOffset:       part.Location.SlotOffset,
				RowOffset:        part.Location.RowOffset,
			})
			rowLen := int(binary.LittleEndian.Uint16(page[rowOff+0x01:]))
			if rowLen > 0 {
				rowOff += rowLen - 1
			}
		}
	})
	for tableID, parts := range partitionsByTable {
		sort.Slice(parts, func(i, j int) bool {
			if parts[i].PartTableID == parts[j].PartTableID {
				return parts[i].Name < parts[j].Name
			}
			return parts[i].PartTableID < parts[j].PartTableID
		})
		partitionsByTable[tableID] = parts
	}
	return partitionsByTable
}

func partitionedTablesFromMap(tables map[uint32]dictionaryObject, partitionsByTable map[uint32][]PartitionInfo) []PartitionedTable {
	var result []PartitionedTable
	for tableID, parts := range partitionsByTable {
		table := tables[tableID]
		result = append(result, PartitionedTable{
			Owner:      table.Owner,
			Name:       table.Name,
			TableID:    table.ID,
			Partitions: parts,
		})
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].Owner == result[j].Owner {
			if result[i].Name == result[j].Name {
				return result[i].TableID < result[j].TableID
			}
			return result[i].Name < result[j].Name
		}
		return result[i].Owner < result[j].Owner
	})
	return result
}

func countPartitions(partitionsByTable map[uint32][]PartitionInfo) int {
	count := 0
	for _, parts := range partitionsByTable {
		count += len(parts)
	}
	return count
}

func scanPartitionKeysByTable(data []byte, pageSize uint32, tables map[uint32]dictionaryObject, matcher ownerMatcher) map[uint32][]uint16 {
	keysByTable := make(map[uint32][]uint16)
	iterDictionarySlotRanges(data, pageSize, func(page []byte, pageNo uint32, slotNo uint16, slotOff uint16, nextOff uint16) {
		_ = pageNo
		_ = slotNo
		for pos := int(slotOff); pos+16 < int(nextOff); pos++ {
			tableID, colIDs, ok := parseTabPartInfoAt(page, pos, int(slotOff), int(nextOff))
			if !ok {
				continue
			}
			table, ok := tables[tableID]
			if !ok || !matcher.allowed(table.Owner) {
				continue
			}
			if len(colIDs) > 0 {
				keysByTable[tableID] = colIDs
			}
		}
	})
	return keysByTable
}

func parseTabPartInfoAt(page []byte, pos int, slotOff int, nextOff int) (uint32, []uint16, bool) {
	const tabPartType = "TABPART"
	typeMarker := pos
	if typeMarker < slotOff+8 || typeMarker+1+len(tabPartType)+1 >= nextOff {
		return 0, nil, false
	}
	if page[typeMarker] != 0x87 || string(page[typeMarker+1:typeMarker+1+len(tabPartType)]) != tabPartType {
		return 0, nil, false
	}

	tableID := binary.LittleEndian.Uint32(page[typeMarker-8:])
	binMarkerOff := typeMarker + 1 + len(tabPartType)
	if binMarkerOff >= nextOff {
		return 0, nil, false
	}
	binMarker := page[binMarkerOff]
	if binMarker < 0x80 || binMarker > 0xBF {
		return 0, nil, false
	}
	binLen := int(binMarker - 0x80)
	binStart := binMarkerOff + 1
	binEnd := binStart + binLen
	if binLen < 4 || binEnd > nextOff || binEnd > len(page) {
		return 0, nil, false
	}
	binValue := page[binStart:binEnd]
	keyCount := int(binary.LittleEndian.Uint16(binValue[0:]))
	if keyCount <= 0 || keyCount > 16 {
		return 0, nil, false
	}
	colIDs := make([]uint16, 0, keyCount)
	for i := 0; i < keyCount; i++ {
		off := 2 + i*4
		if off+2 > len(binValue) {
			break
		}
		colIDs = append(colIDs, binary.LittleEndian.Uint16(binValue[off:]))
	}
	return tableID, colIDs, true
}

func scanDictionaryObjects(data []byte, pageSize uint32, decoder textDecoder) map[uint32]dictionaryObject {
	objects := make(map[uint32]dictionaryObject)
	iterDictionaryRows(data, pageSize, func(page []byte, pageNo uint32, slotNo uint16, slotOff uint16) {
		obj, ok := parseDDLObjectRow(page, int(slotOff), pageNo, slotNo, slotOff, pageSize, decoder)
		if !ok {
			return
		}
		if _, exists := objects[obj.ID]; exists {
			return
		}
		objects[obj.ID] = obj
	})
	return objects
}

func iterDictionarySlotRanges(data []byte, pageSize uint32, visit func(page []byte, pageNo uint32, slotNo uint16, slotOff uint16, nextOff uint16)) {
	if pageSize == 0 {
		return
	}
	totalPages := len(data) / int(pageSize)
	for pageNo := 0; pageNo < totalPages; pageNo++ {
		start := pageNo * int(pageSize)
		page := data[start : start+int(pageSize)]
		if len(page) < sysObjectsSlotCountOff+2 {
			continue
		}
		slotCount := binary.LittleEndian.Uint16(page[sysObjectsSlotCountOff:])
		if slotCount == 0 || slotCount >= 2048 {
			continue
		}
		slotArrayStart := int(pageSize) - pageSlotTrailerLen - int(slotCount)*2
		if slotArrayStart < 0x40 || slotArrayStart >= int(pageSize) {
			continue
		}

		slots := make([]dictionarySlotRange, 0, slotCount)
		for slotNo := uint16(0); slotNo < slotCount; slotNo++ {
			pos := slotArrayStart + int(slotNo)*2
			slotOff := binary.LittleEndian.Uint16(page[pos:])
			if slotOff == 0 || int(slotOff) >= slotArrayStart {
				continue
			}
			slots = append(slots, dictionarySlotRange{slotNo: slotNo, slotOff: slotOff})
		}
		sort.Slice(slots, func(i, j int) bool {
			if slots[i].slotOff == slots[j].slotOff {
				return slots[i].slotNo < slots[j].slotNo
			}
			return slots[i].slotOff < slots[j].slotOff
		})
		for i, slot := range slots {
			nextOff := uint16(slotArrayStart)
			if i+1 < len(slots) {
				nextOff = slots[i+1].slotOff
			}
			if nextOff <= slot.slotOff {
				continue
			}
			visit(page, uint32(pageNo), slot.slotNo, slot.slotOff, nextOff)
		}
	}
}

type dictionarySlotRange struct {
	slotNo  uint16
	slotOff uint16
}

func parseDDLPartitionRow(page []byte, rowOff int, pageNo uint32, slotNo uint16, slotOff uint16, pageSize uint32, decoder textDecoder) (partitionDef, bool) {
	for delta := 0; delta < 8; delta++ {
		part, ok := parseDDLPartitionRowAt(page, rowOff+delta, pageNo, slotNo, slotOff, pageSize, decoder)
		if ok {
			return part, true
		}
	}
	return partitionDef{}, false
}

func parseDDLPartitionRowAt(page []byte, rowOff int, pageNo uint32, slotNo uint16, slotOff uint16, pageSize uint32, decoder textDecoder) (partitionDef, bool) {
	if rowOff+sysHPartTypeOffset+2 >= int(pageSize) {
		return partitionDef{}, false
	}
	rowLen := int(binary.LittleEndian.Uint16(page[rowOff+0x01:]))
	if rowLen < 0x24 || rowLen > 0x2400 || rowOff+rowLen > len(page) {
		return partitionDef{}, false
	}

	partType, next, ok := readDDLShortString(page, rowOff+sysHPartTypeOffset, decoder, false)
	if !ok {
		return partitionDef{}, false
	}
	partType = strings.ToUpper(strings.TrimSpace(partType))
	switch partType {
	case "RANGE", "LIST", "HASH":
	default:
		return partitionDef{}, false
	}

	partName, next, ok := readDDLShortString(page, next, decoder, false)
	if !ok || !isSafeShortText(partName) {
		return partitionDef{}, false
	}

	baseTableID := binary.LittleEndian.Uint32(page[rowOff+sysHPartBaseTableIDOffset:])
	partTableID := binary.LittleEndian.Uint32(page[rowOff+sysHPartPartTableIDOffset:])
	if baseTableID == 0 || partTableID == 0 || baseTableID == partTableID {
		return partitionDef{}, false
	}

	rowAbs := uint64(pageNo)*uint64(pageSize) + uint64(rowOff)
	highValueEnd := rowOff + rowLen
	highValue := append([]byte(nil), page[next:highValueEnd]...)
	highValue = trimPartitionHighValue(highValue)
	return partitionDef{
		BaseTableID: baseTableID,
		PartTableID: partTableID,
		Type:        partType,
		Name:        partName,
		HighValue:   highValue,
		Location:    ddlLocation{PageNo: pageNo, SlotNo: slotNo, SlotOffset: slotOff, RowOffset: rowAbs},
	}, true
}

func trimPartitionHighValue(raw []byte) []byte {
	end := len(raw)
	for end > 0 && raw[end-1] == 0 {
		end--
	}
	return raw[:end]
}

func partitionHighValueHex(raw []byte, maxBytes int) string {
	if len(raw) == 0 {
		return ""
	}
	if maxBytes > 0 && len(raw) > maxBytes {
		return strings.ToUpper(hex.EncodeToString(raw[:maxBytes])) + "..."
	}
	return strings.ToUpper(hex.EncodeToString(raw))
}

func partitionHighValuePreview(raw []byte) string {
	tokens := partitionHighValueTokens(raw)
	if len(tokens) == 0 {
		return ""
	}
	return strings.Join(tokens, ",")
}

func partitionHighValueTokens(raw []byte) []string {
	if len(raw) == 0 {
		return nil
	}
	var tokens []string
	var current strings.Builder
	flush := func() {
		value := strings.TrimSpace(current.String())
		current.Reset()
		if len([]rune(value)) < 2 || !hasPartitionPreviewSignal(value) {
			return
		}
		for _, existing := range tokens {
			if existing == value {
				return
			}
		}
		tokens = append(tokens, value)
	}

	for i := 0; i < len(raw); {
		r, size := utf8.DecodeRune(raw[i:])
		if r == utf8.RuneError && size == 1 {
			flush()
			i++
			continue
		}
		if isPartitionPreviewRune(r) {
			current.WriteRune(r)
		} else {
			flush()
		}
		i += size
	}
	flush()
	if len(tokens) == 0 {
		return nil
	}
	if len(tokens) > 8 {
		tokens = tokens[:8]
	}
	return tokens
}

func isPartitionPreviewRune(r rune) bool {
	if r == ' ' || r == '_' || r == '-' || r == '.' || r == ':' || r == '/' || r == '\'' {
		return true
	}
	return unicode.IsLetter(r) || unicode.IsNumber(r)
}

func hasPartitionPreviewSignal(value string) bool {
	for _, r := range value {
		if unicode.IsLetter(r) || unicode.IsNumber(r) {
			return true
		}
	}
	return false
}
