package dm

import (
	"bufio"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"os"
	pathpkg "path"
	"path/filepath"
	"sort"
	"strings"
)

const (
	dataRowAreaStart       = 0x62
	dataPageSlotCountOff   = 0x24
	dataPageFreeEndOff     = 0x26
	dataPageRecordCountOff = 0x2C
	dataPageTreeLevelOff   = 0x38
	dataPageAssistIndexOff = 0x3A
)

type DataExportOptions struct {
	SystemPath          string
	ControlPath         string
	DataDir             string
	OutputPath          string
	OwnerFilter         string
	TableFilter         string
	ExcludeTables       string
	Charset             string
	MaxRows             int
	WriteFailedComments bool
}

type DataExportResult struct {
	SystemPath        string
	OutputPath        string
	DataDir           string
	PageSize          uint32
	ObjectCount       int
	TableCount        int
	ColumnCount       int
	AssistIndexCount  int
	DataFileCount     int
	PagesScanned      int
	RowsLocated       int
	RowsExported      int
	RowsFailed        int
	TablesWithRows    int
	TablesWithoutRows int
	TableRowCounts    []DataTableRowCount
}

type DataTableRowCount struct {
	Owner        string
	Name         string
	RowsLocated  int
	RowsExported int
	RowsFailed   int
}

type dataFileKey struct {
	groupID uint32
	fileID  int16
}

type dataFileRef struct {
	key            dataFileKey
	path           string
	tablespaceName string
}

type dataTableInfo struct {
	table   dictionaryObject
	columns []columnDef
	storage indexDef
}

type dataValue struct {
	value any
}

type dmNumber string
type dmBinary []byte

func ExportData(opts DataExportOptions) (*DataExportResult, error) {
	if opts.SystemPath == "" {
		return nil, fmt.Errorf("export-data requires SYSTEM.DBF path")
	}
	if opts.OutputPath == "" {
		return nil, fmt.Errorf("export-data requires output path")
	}

	dataDir := strings.TrimSpace(opts.DataDir)
	if dataDir == "" {
		dataDir = filepath.Dir(opts.SystemPath)
		if dataDir == "" {
			dataDir = "."
		}
	}

	systemData, err := os.ReadFile(opts.SystemPath)
	if err != nil {
		return nil, fmt.Errorf("read SYSTEM.DBF: %w", err)
	}
	if len(systemData) < systemHeaderReadSize {
		return nil, fmt.Errorf("SYSTEM.DBF is too small")
	}
	pageSize, _ := detectSystemPageSize(systemData[:systemHeaderReadSize], int64(len(systemData)))
	if pageSize == 0 {
		return nil, fmt.Errorf("cannot detect SYSTEM.DBF page size")
	}
	restoreSystemPages(systemData, pageSize)

	preferredCharset := strings.ToLower(strings.TrimSpace(opts.Charset))
	if preferredCharset == "" || preferredCharset == "auto" {
		if charset, ok := detectSystemCharsetFromData(systemData, pageSize); ok && charset.DecoderName != "" {
			preferredCharset = charset.DecoderName
		}
	}
	decoder := textDecoder{preferred: preferredCharset}
	ownerMatcher := newOwnerMatcher(opts.OwnerFilter)
	tableFilter := strings.TrimSpace(opts.TableFilter)
	if tableFilter == "" {
		tableFilter = "all"
	}
	tableMatcher := newTableNameMatcher(tableFilter)
	excludeMatcher := newTableNameMatcher(opts.ExcludeTables)
	tablespaces := loadTablespaceNames(opts.ControlPath)

	objects := scanDictionaryObjects(systemData, pageSize, decoder)
	schemaNames := schemaNamesFromDictionaryObjects(objects)
	for id, obj := range objects {
		obj.Owner = resolveSchemaName(obj.SchemaID, schemaNames)
		objects[id] = obj
	}

	tables := make(map[uint32]dictionaryObject)
	indexObjects := make(map[uint32]dictionaryObject)
	for _, obj := range objects {
		switch {
		case obj.Type == "SCHOBJ" && obj.Subtype == "UTAB":
			tables[obj.ID] = obj
		case obj.Type == "TABOBJ" && obj.Subtype == "INDEX":
			indexObjects[obj.ID] = obj
		}
	}

	columnsByTable := make(map[uint32][]columnDef)
	columnCount := 0
	iterDictionaryRows(systemData, pageSize, func(page []byte, pageNo uint32, slotNo uint16, slotOff uint16) {
		col, ok := parseDDLColumnRow(page, int(slotOff), pageNo, slotNo, slotOff, pageSize, decoder)
		if !ok {
			return
		}
		table, ok := tables[col.TableID]
		if !ok || !ownerMatcher.allowed(table.Owner) {
			return
		}
		if !tableMatcher.allowed(table.Owner, table.Name) || excludeMatcher.allowed(table.Owner, table.Name) {
			return
		}
		columnsByTable[col.TableID] = append(columnsByTable[col.TableID], col)
		columnCount++
	})
	for tableID := range columnsByTable {
		sort.Slice(columnsByTable[tableID], func(i, j int) bool {
			return columnsByTable[tableID][i].ColID < columnsByTable[tableID][j].ColID
		})
	}

	indexes := make(map[uint32]indexDef)
	iterDictionaryRows(systemData, pageSize, func(page []byte, pageNo uint32, slotNo uint16, slotOff uint16) {
		idx, ok := parseDDLIndexRow(page, int(slotOff), pageSize)
		if ok {
			indexes[idx.ID] = idx
		}
	})

	assistByParentID := assistIndexesByParentID(indexObjects, indexes, tablespaces)
	partitionsByTable := scanPartitionsByTable(systemData, pageSize, decoder, tables, ownerMatcher)
	selectedTables := make(map[uint32]dataTableInfo)
	assistByIndexID := make(map[uint32]dataTableInfo)
	neededFiles := make(map[dataFileKey]bool)
	for tableID, table := range tables {
		if !ownerMatcher.allowed(table.Owner) || !tableMatcher.allowed(table.Owner, table.Name) || excludeMatcher.allowed(table.Owner, table.Name) {
			continue
		}
		if table.isTemporaryTable() || len(columnsByTable[tableID]) == 0 {
			continue
		}
		baseInfo := dataTableInfo{
			table:   table,
			columns: columnsByTable[tableID],
		}
		selectedTables[tableID] = baseInfo
		for _, storage := range assistByParentID[tableID] {
			addDataAssistIndex(assistByIndexID, neededFiles, baseInfo, storage)
		}
		for _, part := range partitionsByTable[tableID] {
			for _, storage := range assistByParentID[part.PartTableID] {
				addDataAssistIndex(assistByIndexID, neededFiles, baseInfo, storage)
			}
		}
	}

	dataFiles, err := resolveDataFiles(opts.ControlPath, dataDir)
	if err != nil {
		return nil, err
	}
	dataFiles = filterNeededDataFiles(dataFiles, neededFiles)

	result := &DataExportResult{
		SystemPath:       opts.SystemPath,
		OutputPath:       opts.OutputPath,
		DataDir:          dataDir,
		PageSize:         pageSize,
		ObjectCount:      len(objects),
		TableCount:       len(selectedTables),
		ColumnCount:      columnCount,
		AssistIndexCount: len(assistByIndexID),
		DataFileCount:    len(dataFiles),
	}
	rowStats := initDataTableRowStats(selectedTables)

	out, err := os.Create(opts.OutputPath)
	if err != nil {
		return nil, fmt.Errorf("create data output: %w", err)
	}
	defer out.Close()
	writer := bufio.NewWriter(out)
	defer writer.Flush()

	fmt.Fprintln(writer, "-- Generated by dmdul export-data. Review before running.")
	fmt.Fprintln(writer, "-- Current decoder targets ordinary in-row heap/cluster rows.")
	fmt.Fprintln(writer)

	if len(assistByIndexID) == 0 || len(dataFiles) == 0 {
		result.TableRowCounts = finalizeDataTableRowStats(rowStats)
		result.TablesWithoutRows = len(result.TableRowCounts)
		return result, nil
	}

	stop := false
	for _, file := range dataFiles {
		if stop {
			break
		}
		fileData, err := os.ReadFile(file.path)
		if err != nil {
			return nil, fmt.Errorf("read data file %s: %w", file.path, err)
		}
		pageCount := len(fileData) / int(pageSize)
		result.PagesScanned += pageCount
		for pageNo := 0; pageNo < pageCount; pageNo++ {
			if stop {
				break
			}
			start := pageNo * int(pageSize)
			page := fileData[start : start+int(pageSize)]
			if !isProbableDMDataPage(page, pageSize) {
				continue
			}
			assistIndexID := binary.LittleEndian.Uint32(page[dataPageAssistIndexOff:])
			info, ok := assistByIndexID[assistIndexID]
			if !ok {
				continue
			}
			if uint32(info.storage.GroupID) != file.key.groupID || info.storage.RootFile != file.key.fileID {
				continue
			}
			nRec := int(binary.LittleEndian.Uint16(page[dataPageRecordCountOff:]))
			if nRec <= 0 {
				continue
			}
			rows := locateRowsInDataPage(page, pageSize, nRec)
			for _, row := range rows {
				if opts.MaxRows > 0 && result.RowsLocated >= opts.MaxRows {
					stop = true
					break
				}
				result.RowsLocated++
				rowStart := int(row.offset)
				rowEnd := rowStart + int(row.length)
				rowBytes := append([]byte(nil), page[rowStart:rowEnd]...)
				sql, _, _, err := renderInsertForDataRow(info, rowBytes, decoder)
				stats := rowStats[info.table.ID]
				if stats != nil {
					stats.RowsLocated++
				}
				if err != nil {
					result.RowsFailed++
					if stats != nil {
						stats.RowsFailed++
					}
					if opts.WriteFailedComments {
						fmt.Fprintf(writer, "-- FAILED %s.%s page=%d slot=%d off=0x%X len=%d: %v\n",
							quoteIdent(info.table.Owner), quoteIdent(info.table.Name), pageNo, row.slotNo, row.offset, row.length, err)
					}
					continue
				}
				result.RowsExported++
				if stats != nil {
					stats.RowsExported++
				}
				fmt.Fprintln(writer, sql)
			}
		}
	}

	result.TableRowCounts = finalizeDataTableRowStats(rowStats)
	for _, item := range result.TableRowCounts {
		if item.RowsLocated > 0 {
			result.TablesWithRows++
		} else {
			result.TablesWithoutRows++
		}
	}
	return result, nil
}

func addDataAssistIndex(assistByIndexID map[uint32]dataTableInfo, neededFiles map[dataFileKey]bool, info dataTableInfo, storage indexDef) {
	if storage.RootFile < 0 {
		return
	}
	info.storage = storage
	assistByIndexID[storage.ID] = info
	key := dataFileKey{groupID: uint32(storage.GroupID), fileID: storage.RootFile}
	neededFiles[key] = true
}

func assistIndexesByParentID(indexObjects map[uint32]dictionaryObject, indexes map[uint32]indexDef, tablespaces map[uint32]string) map[uint32][]indexDef {
	result := make(map[uint32][]indexDef)
	for indexID, obj := range indexObjects {
		idx, ok := indexes[indexID]
		if !ok {
			continue
		}
		if idx.Flag&1 == 0 || idx.KeyNum != 0 || idx.RootFile < 0 {
			continue
		}
		if _, ok := tablespaces[uint32(idx.GroupID)]; !ok {
			continue
		}
		parentID := uint32(obj.ParentID)
		result[parentID] = append(result[parentID], idx)
	}
	for parentID := range result {
		sort.Slice(result[parentID], func(i, j int) bool {
			return result[parentID][i].ID < result[parentID][j].ID
		})
	}
	return result
}

func initDataTableRowStats(tables map[uint32]dataTableInfo) map[uint32]*DataTableRowCount {
	result := make(map[uint32]*DataTableRowCount, len(tables))
	for tableID, info := range tables {
		result[tableID] = &DataTableRowCount{
			Owner: info.table.Owner,
			Name:  info.table.Name,
		}
	}
	return result
}

func finalizeDataTableRowStats(stats map[uint32]*DataTableRowCount) []DataTableRowCount {
	var ids []uint32
	for tableID := range stats {
		ids = append(ids, tableID)
	}
	sort.Slice(ids, func(i, j int) bool {
		left := stats[ids[i]]
		right := stats[ids[j]]
		if left.Owner != right.Owner {
			return left.Owner < right.Owner
		}
		if left.Name != right.Name {
			return left.Name < right.Name
		}
		return ids[i] < ids[j]
	})
	result := make([]DataTableRowCount, 0, len(ids))
	for _, tableID := range ids {
		result = append(result, *stats[tableID])
	}
	return result
}

type tableNameMatcher struct {
	all       bool
	hasRules  bool
	names     map[string]bool
	qualified map[string]bool
}

func newTableNameMatcher(filter string) tableNameMatcher {
	filter = strings.TrimSpace(filter)
	if filter == "" {
		return tableNameMatcher{}
	}
	if strings.EqualFold(filter, "all") || strings.EqualFold(filter, "*") {
		return tableNameMatcher{all: true, hasRules: true}
	}
	matcher := tableNameMatcher{
		hasRules:  true,
		names:     make(map[string]bool),
		qualified: make(map[string]bool),
	}
	for _, part := range strings.Split(filter, ",") {
		token := normalizeTableFilterToken(part)
		if token == "" {
			continue
		}
		if strings.Contains(token, ".") {
			matcher.qualified[token] = true
			continue
		}
		matcher.names[token] = true
	}
	if len(matcher.names) == 0 && len(matcher.qualified) == 0 {
		return tableNameMatcher{}
	}
	return matcher
}

func (m tableNameMatcher) allowed(owner string, table string) bool {
	if !m.hasRules {
		return false
	}
	if m.all {
		return true
	}
	owner = normalizeNameFilter(owner)
	table = normalizeNameFilter(table)
	return m.names[table] || m.qualified[owner+"."+table]
}

func normalizeNameFilter(value string) string {
	parts := splitQualifiedNameFilter(value)
	if len(parts) > 1 {
		return normalizeTableFilterToken(value)
	}
	return normalizeNameFilterPart(value)
}

func normalizeTableFilterToken(value string) string {
	parts := splitQualifiedNameFilter(value)
	if len(parts) == 0 {
		return ""
	}
	for i := range parts {
		parts[i] = normalizeNameFilterPart(parts[i])
	}
	return strings.Join(parts, ".")
}

func normalizeNameFilterPart(value string) string {
	value = strings.TrimSpace(value)
	if len(value) >= 2 && value[0] == '"' && value[len(value)-1] == '"' {
		value = value[1 : len(value)-1]
	} else {
		value = strings.Trim(value, `"`)
	}
	value = strings.ReplaceAll(value, `""`, `"`)
	return strings.ToUpper(value)
}

func splitQualifiedNameFilter(value string) []string {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	var parts []string
	var current strings.Builder
	inQuote := false
	for i := 0; i < len(value); i++ {
		ch := value[i]
		if ch == '"' {
			current.WriteByte(ch)
			if inQuote && i+1 < len(value) && value[i+1] == '"' {
				i++
				current.WriteByte(value[i])
				continue
			}
			inQuote = !inQuote
			continue
		}
		if ch == '.' && !inQuote {
			parts = append(parts, current.String())
			current.Reset()
			continue
		}
		current.WriteByte(ch)
	}
	parts = append(parts, current.String())
	out := parts[:0]
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}

func resolveDataFiles(controlPath string, dataDir string) ([]dataFileRef, error) {
	if controlPath == "" {
		return nil, fmt.Errorf("export-data requires dm.ctl path")
	}
	ctl, err := InspectControlFile(controlPath)
	if err != nil {
		return nil, fmt.Errorf("inspect dm.ctl: %w", err)
	}
	var refs []dataFileRef
	for _, entry := range ctl.Entries {
		if entry.ID < 4 {
			continue
		}
		fileID := int16(0)
		for _, controlPath := range entry.Paths {
			if !strings.EqualFold(pathpkg.Ext(strings.ReplaceAll(controlPath.Value, "\\", "/")), ".DBF") {
				continue
			}
			resolved, ok := resolveDataFilePath(controlPath.Value, dataDir)
			if !ok {
				fileID++
				continue
			}
			refs = append(refs, dataFileRef{
				key:            dataFileKey{groupID: entry.ID, fileID: fileID},
				path:           resolved,
				tablespaceName: entry.Name,
			})
			fileID++
		}
	}
	sort.Slice(refs, func(i, j int) bool {
		if refs[i].key.groupID != refs[j].key.groupID {
			return refs[i].key.groupID < refs[j].key.groupID
		}
		return refs[i].key.fileID < refs[j].key.fileID
	})
	return refs, nil
}

func resolveDataFilePath(controlValue string, dataDir string) (string, bool) {
	if info, err := os.Stat(controlValue); err == nil && !info.IsDir() {
		return controlValue, true
	}
	base := pathpkg.Base(strings.ReplaceAll(controlValue, "\\", "/"))
	if base == "." || base == "/" || base == "" {
		return "", false
	}
	candidate := filepath.Join(dataDir, base)
	if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
		return candidate, true
	}
	return "", false
}

func filterNeededDataFiles(files []dataFileRef, needed map[dataFileKey]bool) []dataFileRef {
	if len(needed) == 0 {
		return nil
	}
	var result []dataFileRef
	for _, file := range files {
		if needed[file.key] {
			result = append(result, file)
		}
	}
	return result
}

type locatedDataRow struct {
	slotNo uint16
	offset uint16
	length uint16
}

func isProbableDMDataPage(page []byte, pageSize uint32) bool {
	if len(page) < 0x80 || len(page) < int(pageSize) {
		return false
	}
	nSlot := binary.LittleEndian.Uint16(page[dataPageSlotCountOff:])
	freeEnd := binary.LittleEndian.Uint16(page[dataPageFreeEndOff:])
	nRec := binary.LittleEndian.Uint16(page[dataPageRecordCountOff:])
	treeLevel := binary.LittleEndian.Uint16(page[dataPageTreeLevelOff:])
	if nSlot >= 2048 {
		return false
	}
	if nRec > nSlot {
		return false
	}
	if treeLevel != 0 {
		return false
	}
	return freeEnd >= dataRowAreaStart && uint32(freeEnd) <= pageSize
}

func locateRowsInDataPage(page []byte, pageSize uint32, expectedRecords int) []locatedDataRow {
	freeEnd := binary.LittleEndian.Uint16(page[dataPageFreeEndOff:])
	var rows []locatedDataRow
	nSlot := binary.LittleEndian.Uint16(page[dataPageSlotCountOff:])
	slotArrayStart := int(pageSize) - pageSlotTrailerLen - int(nSlot)*2
	if nSlot > 0 && nSlot < 2048 && slotArrayStart >= 0x40 && slotArrayStart+int(nSlot)*2 <= int(pageSize) {
		for slotNo := uint16(1); slotNo <= nSlot; slotNo++ {
			pos := slotArrayStart + int(slotNo-1)*2
			rowOff := binary.LittleEndian.Uint16(page[pos:])
			rowLen, ok := dataRowLength(page, rowOff, pageSize, freeEnd)
			if !ok || !isLiveDataRow(page, rowOff) {
				continue
			}
			rows = append(rows, locatedDataRow{slotNo: slotNo, offset: rowOff, length: rowLen})
		}
		sort.Slice(rows, func(i, j int) bool {
			if rows[i].offset == rows[j].offset {
				return rows[i].slotNo < rows[j].slotNo
			}
			return rows[i].offset < rows[j].offset
		})
		if expectedRecords >= 0 && len(rows) > expectedRecords {
			rows = rows[:expectedRecords]
		}
		if len(rows) > 0 {
			return rows
		}
	}

	pos := uint16(dataRowAreaStart)
	slotNo := uint16(1)
	for int(pos)+3 <= int(freeEnd) && uint32(pos) < pageSize {
		rowLen, ok := dataRowLength(page, pos, pageSize, freeEnd)
		if !ok || rowLen == 0 {
			break
		}
		if isLiveDataRow(page, pos) {
			rows = append(rows, locatedDataRow{slotNo: slotNo, offset: pos, length: rowLen})
		}
		slotNo++
		pos += rowLen
	}
	if expectedRecords >= 0 && len(rows) > expectedRecords {
		rows = rows[:expectedRecords]
	}
	return rows
}

func dataRowLength(page []byte, rowOff uint16, pageSize uint32, freeEnd uint16) (uint16, bool) {
	if int(rowOff)+3 > len(page) || uint32(rowOff)+3 > pageSize {
		return 0, false
	}
	rowLen := binary.BigEndian.Uint16(page[rowOff:])
	if rowLen < 3 {
		return 0, false
	}
	if uint32(rowOff)+uint32(rowLen) > uint32(freeEnd) || uint32(rowOff)+uint32(rowLen) > pageSize {
		return 0, false
	}
	return rowLen, true
}

func isLiveDataRow(page []byte, rowOff uint16) bool {
	return int(rowOff)+3 <= len(page) && page[rowOff+2] == 0x00
}

func renderInsertForDataRow(info dataTableInfo, row []byte, decoder textDecoder) (string, int, int, error) {
	values, dataStart, dataEnd, err := parseDataRowValues(row, info.columns, decoder)
	if err != nil {
		return "", 0, 0, err
	}
	cols := make([]string, 0, len(info.columns))
	vals := make([]string, 0, len(info.columns))
	for _, col := range info.columns {
		cols = append(cols, quoteIdent(col.Name))
		value, ok := values[col.ColID]
		if !ok {
			vals = append(vals, "NULL")
			continue
		}
		sqlValue, err := sqlValueForDataColumn(col, value.value)
		if err != nil {
			return "", 0, 0, err
		}
		vals = append(vals, sqlValue)
	}
	sql := fmt.Sprintf("INSERT INTO %s.%s (%s) VALUES (%s);",
		quoteIdent(info.table.Owner),
		quoteIdent(info.table.Name),
		strings.Join(cols, ", "),
		strings.Join(vals, ", "),
	)
	return sql, dataStart, dataEnd, nil
}

func parseDataRowValues(row []byte, columns []columnDef, decoder textDecoder) (map[uint16]dataValue, int, int, error) {
	var fixedCols []columnDef
	var varCols []columnDef
	for _, col := range columns {
		switch {
		case isVariableDataType(col.DataType):
			varCols = append(varCols, col)
		case fixedDataSize(col.DataType) > 0:
			fixedCols = append(fixedCols, col)
		}
	}

	type candidate struct {
		score                    int
		values                   map[uint16]dataValue
		start                    int
		end                      int
		omittedTrailingNullValue bool
	}
	var best *candidate
	var errors []string
	limit := len(row)
	if limit > 16 {
		limit = 16
	}
	for start := 3; start < limit; start++ {
		pos := start
		values := make(map[uint16]dataValue)
		ok := true
		for _, col := range fixedCols {
			value, next, err := parseFixedDataValue(col.DataType, row, pos)
			if err != nil {
				errors = append(errors, fmt.Sprintf("start=%d %v", start, err))
				ok = false
				break
			}
			values[col.ColID] = dataValue{value: value}
			pos = next
		}
		if !ok {
			continue
		}
		omittedTrailingNullValue := false
		for i, col := range varCols {
			value, next, err := readVariableDataValue(col, row, pos, decoder)
			if err != nil {
				if canOmitTrailingNullVars(row, pos, varCols[i:]) {
					for _, nullCol := range varCols[i:] {
						values[nullCol.ColID] = dataValue{value: nil}
					}
					omittedTrailingNullValue = true
					break
				}
				errors = append(errors, fmt.Sprintf("start=%d %v", start, err))
				ok = false
				break
			}
			values[col.ColID] = dataValue{value: value}
			pos = next
		}
		if !ok {
			continue
		}
		trailing := len(row) - pos
		if trailing < 0 || trailing > 64 {
			errors = append(errors, fmt.Sprintf("start=%d bad trailing length %d", start, trailing))
			continue
		}
		score := 100 - trailing
		if best == nil || score > best.score {
			best = &candidate{score: score, values: values, start: start, end: pos, omittedTrailingNullValue: omittedTrailingNullValue}
		}
	}
	if best == nil {
		if len(errors) > 5 {
			errors = errors[:5]
		}
		return nil, 0, 0, fmt.Errorf("cannot parse row; candidates errors=%v", errors)
	}
	if best.omittedTrailingNullValue {
		markTrailingNullableZeroFixedValues(best.values, fixedCols)
	}
	return best.values, best.start, best.end, nil
}

func canOmitTrailingNullVars(row []byte, pos int, cols []columnDef) bool {
	if pos < 0 || pos >= len(row) || len(cols) == 0 {
		return false
	}
	for _, col := range cols {
		if !isNullableColumn(col) {
			return false
		}
	}
	remaining := len(row) - pos
	if remaining < 8 || remaining > 64 {
		return false
	}
	marker := row[pos]
	return marker < 0x80
}

func markTrailingNullableZeroFixedValues(values map[uint16]dataValue, fixedCols []columnDef) {
	for i := len(fixedCols) - 1; i >= 0; i-- {
		col := fixedCols[i]
		if !isNullableColumn(col) {
			return
		}
		value, ok := values[col.ColID]
		if !ok || !isZeroFixedValue(value.value) {
			return
		}
		values[col.ColID] = dataValue{value: nil}
	}
}

func isNullableColumn(col columnDef) bool {
	return !strings.EqualFold(strings.TrimSpace(col.Nullable), "N")
}

func isZeroFixedValue(value any) bool {
	switch v := value.(type) {
	case int8:
		return v == 0
	case int16:
		return v == 0
	case int32:
		return v == 0
	case int64:
		return v == 0
	default:
		return false
	}
}

func isVariableDataType(dataType string) bool {
	return isCharacterDataType(dataType) || isBinaryDataType(dataType) || isNumberDataType(dataType)
}

func isCharacterDataType(dataType string) bool {
	upper := normalizeDataType(dataType)
	return strings.Contains(upper, "CHAR") || strings.Contains(upper, "VARCHAR") || strings.Contains(upper, "TEXT") || strings.Contains(upper, "CLOB") || upper == "LONG"
}

func isCharacterLOBDataType(dataType string) bool {
	upper := normalizeDataType(dataType)
	return strings.Contains(upper, "CLOB") || strings.Contains(upper, "TEXT") || upper == "LONG"
}

func isBinaryDataType(dataType string) bool {
	switch normalizeDataType(dataType) {
	case "VARBINARY", "LONGVARBINARY", "BLOB", "IMAGE":
		return true
	default:
		return false
	}
}

func isNumberDataType(dataType string) bool {
	switch normalizeDataType(dataType) {
	case "NUMBER", "NUMERIC", "DEC", "DECIMAL":
		return true
	default:
		return false
	}
}

func fixedDataSize(dataType string) int {
	switch normalizeDataType(dataType) {
	case "TINYINT":
		return 1
	case "SMALLINT":
		return 2
	case "INT", "INTEGER":
		return 4
	case "BIGINT":
		return 8
	case "DATE":
		return 3
	case "DATETIME", "TIMESTAMP", "TIME":
		return 8
	default:
		return 0
	}
}

func normalizeDataType(dataType string) string {
	upper := strings.ToUpper(strings.TrimSpace(dataType))
	if idx := strings.IndexByte(upper, '('); idx >= 0 {
		upper = strings.TrimSpace(upper[:idx])
	}
	return upper
}

func parseFixedDataValue(dataType string, row []byte, pos int) (any, int, error) {
	switch normalizeDataType(dataType) {
	case "TINYINT":
		if pos+1 > len(row) {
			return nil, pos, fmt.Errorf("TINYINT out of range")
		}
		return int8(row[pos]), pos + 1, nil
	case "SMALLINT":
		if pos+2 > len(row) {
			return nil, pos, fmt.Errorf("SMALLINT out of range")
		}
		return int16(binary.LittleEndian.Uint16(row[pos:])), pos + 2, nil
	case "INT", "INTEGER":
		if pos+4 > len(row) {
			return nil, pos, fmt.Errorf("INT out of range")
		}
		return int32(binary.LittleEndian.Uint32(row[pos:])), pos + 4, nil
	case "BIGINT":
		if pos+8 > len(row) {
			return nil, pos, fmt.Errorf("BIGINT out of range")
		}
		return int64(binary.LittleEndian.Uint64(row[pos:])), pos + 8, nil
	case "DATE":
		if pos+3 > len(row) {
			return nil, pos, fmt.Errorf("DATE out of range")
		}
		value, err := decodeDMDate(row[pos : pos+3])
		if err != nil {
			return nil, pos, err
		}
		return value, pos + 3, nil
	case "DATETIME", "TIMESTAMP", "TIME":
		if pos+8 > len(row) {
			return nil, pos, fmt.Errorf("%s out of range", normalizeDataType(dataType))
		}
		value, err := decodeDMDateTime(row[pos : pos+8])
		if err != nil {
			return nil, pos, err
		}
		return value, pos + 8, nil
	default:
		return nil, pos, fmt.Errorf("unsupported fixed type: %s", dataType)
	}
}

func readVariableDataValue(col columnDef, row []byte, pos int, decoder textDecoder) (any, int, error) {
	if isNumberDataType(col.DataType) {
		value, next, err := readDMNumber(row, pos)
		if err != nil {
			return nil, pos, fmt.Errorf("%s: %w", col.Name, err)
		}
		return value, next, nil
	}
	if isBinaryDataType(col.DataType) {
		value, next, err := readShortDataBytes(row, pos)
		if err != nil {
			return nil, pos, fmt.Errorf("%s: %w", col.Name, err)
		}
		if payload, ok := unwrapInlineLOBPayload(value); ok {
			value = payload
		}
		return dmBinary(value), next, nil
	}
	if isCharacterLOBDataType(col.DataType) {
		value, next, err := readInlineTextLOB(row, pos, decoder)
		if err != nil {
			return nil, pos, fmt.Errorf("%s: %w", col.Name, err)
		}
		return value, next, nil
	}
	value, next, err := readShortDataVarchar(row, pos, decoder)
	if err != nil {
		return nil, pos, fmt.Errorf("%s: %w", col.Name, err)
	}
	return value, next, nil
}

func readInlineTextLOB(row []byte, pos int, decoder textDecoder) (string, int, error) {
	raw, next, marker, err := readShortDataBytesWithMarker(row, pos)
	if err != nil {
		return "", pos, fmt.Errorf("%s", strings.Replace(err.Error(), "raw value", "text LOB", 1))
	}
	if payload, ok := unwrapInlineLOBPayload(raw); ok {
		raw = payload
	}
	value, ok := decoder.decode(raw)
	if !ok {
		return "", pos, fmt.Errorf("cannot decode text LOB marker=0x%02X pos=%d raw=%s", marker, pos, strings.ToUpper(hex.EncodeToString(raw)))
	}
	if strings.ContainsRune(value, '\uFFFD') || containsBadControl(value) {
		return "", pos, fmt.Errorf("decoded text LOB contains invalid characters marker=0x%02X pos=%d raw=%s", marker, pos, strings.ToUpper(hex.EncodeToString(raw)))
	}
	return value, next, nil
}

func readShortDataVarchar(row []byte, pos int, decoder textDecoder) (string, int, error) {
	raw, next, marker, err := readShortDataBytesWithMarker(row, pos)
	if err != nil {
		return "", pos, fmt.Errorf("%s", strings.Replace(err.Error(), "raw value", "varchar", 1))
	}
	value, ok := decoder.decode(raw)
	if !ok {
		return "", pos, fmt.Errorf("cannot decode varchar marker=0x%02X pos=%d raw=%s", marker, pos, strings.ToUpper(hex.EncodeToString(raw)))
	}
	if strings.ContainsRune(value, '\uFFFD') || containsBadControl(value) {
		return "", pos, fmt.Errorf("decoded varchar contains invalid characters marker=0x%02X pos=%d raw=%s", marker, pos, strings.ToUpper(hex.EncodeToString(raw)))
	}
	return value, next, nil
}

func readShortDataBytes(row []byte, pos int) ([]byte, int, error) {
	raw, next, _, err := readShortDataBytesWithMarker(row, pos)
	return raw, next, err
}

func readShortDataBytesWithMarker(row []byte, pos int) ([]byte, int, byte, error) {
	if pos >= len(row) {
		return nil, pos, 0, fmt.Errorf("raw value marker out of range")
	}
	marker := row[pos]
	if marker == 0x80 {
		return []byte{}, pos + 1, marker, nil
	}
	if marker < 0x80 {
		if pos+2 > len(row) {
			return nil, pos, marker, fmt.Errorf("raw value extended length out of range")
		}
		n := int(binary.BigEndian.Uint16(row[pos:]))
		if n <= 0 {
			return nil, pos, marker, fmt.Errorf("unsupported raw value marker 0x%02X at %d", marker, pos)
		}
		start := pos + 2
		end := start + n
		if end > len(row) {
			return nil, pos, marker, fmt.Errorf("raw value content out of range")
		}
		return append([]byte(nil), row[start:end]...), end, marker, nil
	}
	if marker < 0x81 || marker > 0xFE {
		return nil, pos, marker, fmt.Errorf("unsupported raw value marker 0x%02X at %d", marker, pos)
	}
	n := int(marker - 0x80)
	start := pos + 1
	end := start + n
	if end > len(row) {
		return nil, pos, marker, fmt.Errorf("raw value content out of range")
	}
	return append([]byte(nil), row[start:end]...), end, marker, nil
}

func unwrapInlineLOBPayload(raw []byte) ([]byte, bool) {
	if len(raw) < 13 {
		return nil, false
	}
	if raw[0] != 0x01 || raw[1] != 0x27 || raw[2] != 0x04 {
		return nil, false
	}
	payloadLen := int(binary.LittleEndian.Uint32(raw[9:13]))
	if payloadLen < 0 || payloadLen != len(raw)-13 {
		return nil, false
	}
	return append([]byte(nil), raw[13:]...), true
}

func readDMNumber(row []byte, pos int) (any, int, error) {
	if pos >= len(row) {
		return nil, pos, fmt.Errorf("NUMBER marker out of range")
	}
	marker := row[pos]
	if marker == 0x80 {
		return nil, pos + 1, nil
	}
	if marker < 0x81 || marker > 0xFE {
		return nil, pos, fmt.Errorf("unsupported NUMBER marker 0x%02X at %d", marker, pos)
	}
	n := int(marker - 0x80)
	start := pos + 1
	end := start + n
	if end > len(row) {
		return nil, pos, fmt.Errorf("NUMBER content out of range")
	}
	value, ok := decodeDMNumber(row[start:end])
	if !ok {
		return nil, pos, fmt.Errorf("cannot decode NUMBER")
	}
	return dmNumber(value), end, nil
}

func decodeDMNumber(raw []byte) (string, bool) {
	if len(raw) == 0 {
		return "", false
	}
	if len(raw) == 1 && raw[0] == 0x80 {
		return "0", true
	}
	if raw[0] >= 0x80 {
		exp := int(raw[0]) - 0xC1
		digits := make([]int, 0, len(raw)-1)
		for _, b := range raw[1:] {
			digit := int(b) - 1
			if digit < 0 || digit > 99 {
				return "", false
			}
			digits = append(digits, digit)
		}
		return formatBase100Number(false, exp, digits), true
	}

	exp := 0x3F - int(raw[0])
	digits := make([]int, 0, len(raw)-1)
	for _, b := range raw[1:] {
		if b == 0x66 {
			break
		}
		digit := 101 - int(b)
		if digit < 0 || digit > 99 {
			return "", false
		}
		digits = append(digits, digit)
	}
	return formatBase100Number(true, exp, digits), true
}

func decodeDMDate(raw []byte) (string, error) {
	if len(raw) != 3 {
		return "", fmt.Errorf("date needs 3 bytes")
	}
	v := uint32(raw[0]) | uint32(raw[1])<<8 | uint32(raw[2])<<16
	year := int(v & ((1 << 15) - 1))
	month := int((v >> 15) & 0xF)
	day := int((v >> 19) & 0x1F)
	if year < 1 || year > 9999 || month < 1 || month > 12 || day < 1 || day > daysInMonth(year, month) {
		return "", fmt.Errorf("invalid date bits: %04d-%02d-%02d", year, month, day)
	}
	return fmt.Sprintf("%04d-%02d-%02d", year, month, day), nil
}

func formatBase100Number(negative bool, exp int, digits []int) string {
	if len(digits) == 0 {
		return "0"
	}
	beforeGroups := exp + 1
	var out strings.Builder
	if negative {
		out.WriteByte('-')
	}
	switch {
	case beforeGroups <= 0:
		out.WriteString("0.")
		for i := 0; i < -beforeGroups; i++ {
			out.WriteString("00")
		}
		for _, digit := range digits {
			out.WriteString(fmt.Sprintf("%02d", digit))
		}
	case beforeGroups >= len(digits):
		out.WriteString(fmt.Sprintf("%d", digits[0]))
		for _, digit := range digits[1:] {
			out.WriteString(fmt.Sprintf("%02d", digit))
		}
		for i := len(digits); i < beforeGroups; i++ {
			out.WriteString("00")
		}
	default:
		out.WriteString(fmt.Sprintf("%d", digits[0]))
		for i := 1; i < beforeGroups; i++ {
			out.WriteString(fmt.Sprintf("%02d", digits[i]))
		}
		out.WriteByte('.')
		for i := beforeGroups; i < len(digits); i++ {
			out.WriteString(fmt.Sprintf("%02d", digits[i]))
		}
	}
	value := out.String()
	if strings.Contains(value, ".") {
		value = strings.TrimRight(value, "0")
		value = strings.TrimRight(value, ".")
	}
	if value == "" || value == "-" {
		return "0"
	}
	return value
}

func decodeDMDateTime(raw []byte) (string, error) {
	if len(raw) != 8 {
		return "", fmt.Errorf("datetime needs 8 bytes")
	}
	v := binary.LittleEndian.Uint64(raw)
	year := int(v & ((1 << 15) - 1))
	month := int((v >> 15) & 0xF)
	day := int((v >> 19) & 0x1F)
	hour := int((v >> 24) & 0x1F)
	minute := int((v >> 29) & 0x3F)
	second := int((v >> 35) & 0x3F)
	micro := int((v >> 41) & ((1 << 23) - 1))
	if year < 1 || year > 9999 || month < 1 || month > 12 || day < 1 || day > daysInMonth(year, month) {
		return "", fmt.Errorf("invalid datetime date bits: %04d-%02d-%02d", year, month, day)
	}
	if hour > 23 || minute > 59 || second > 59 || micro > 999999 {
		return "", fmt.Errorf("invalid datetime time bits: %02d:%02d:%02d.%06d", hour, minute, second, micro)
	}
	return fmt.Sprintf("%04d-%02d-%02d %02d:%02d:%02d.%06d", year, month, day, hour, minute, second, micro), nil
}

func sqlValueForDataColumn(col columnDef, value any) (string, error) {
	if value == nil {
		return "NULL", nil
	}
	typ := normalizeDataType(col.DataType)
	switch typ {
	case "TINYINT", "SMALLINT", "INT", "INTEGER", "BIGINT":
		return fmt.Sprintf("%v", value), nil
	case "NUMBER", "NUMERIC", "DEC", "DECIMAL":
		return fmt.Sprintf("%v", value), nil
	case "DATETIME", "TIMESTAMP":
		return "DATETIME " + sqlLiteral(fmt.Sprintf("%v", value)), nil
	case "DATE":
		text := fmt.Sprintf("%v", value)
		if len(text) >= 10 {
			text = text[:10]
		}
		return "DATE " + sqlLiteral(text), nil
	case "TIME":
		text := fmt.Sprintf("%v", value)
		if len(text) >= 19 {
			text = text[11:]
		}
		return "TIME " + sqlLiteral(text), nil
	default:
		if raw, ok := value.(dmBinary); ok {
			return "HEXTORAW('" + strings.ToUpper(hex.EncodeToString(raw)) + "')", nil
		}
		text := fmt.Sprintf("%v", value)
		if strings.ContainsRune(text, '\uFFFD') || containsBadControl(text) {
			return "", fmt.Errorf("invalid text value for %s", col.Name)
		}
		return sqlLiteral(text), nil
	}
}
