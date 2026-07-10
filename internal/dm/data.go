package dm

import (
	"bufio"
	"encoding/binary"
	"encoding/csv"
	"encoding/hex"
	"fmt"
	"math"
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
	dmPageKindLOBData      = 0x20
	dmPageKindLongRowData  = 0x22
	dmPageKindRowData      = 0x14
	dmPageKindRowOverflow  = 0x16
	dmLOBPagePayloadOff    = 0x38
	dmLOBPageIDOff         = 0x24
	dmLOBPagePayloadLenOff = 0x2C
	dmLOBLocatorSize       = 21
)

type DataExportOptions struct {
	SystemPath          string
	ControlPath         string
	ControlDULPath      string
	DataDir             string
	OutputPath          string
	OwnerFilter         string
	TableFilter         string
	ExcludeTables       string
	Charset             string
	OutputFormat        string
	MaxRows             int
	WriteFailedComments bool
	RecoveryMode        bool
	Dictionary          *DictionaryInfo
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
	OutputFormat      string
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

type dataPageRef struct {
	key    dataFileKey
	pageNo uint32
}

type dataTableInfo struct {
	table           dictionaryObject
	columns         []columnDef
	storage         indexDef
	storageKnown    bool
	dataStorageID   uint32
	historicalRows  bool
	lobReader       *dmLOBReader
	pagePlan        map[dataPageRef]bool
	pagePlanKnown   bool
	recoveryMode    bool
	recoveryGroupID uint32
	segment         tableSegment
	segmentKnown    bool
}

type tableSegment struct {
	fileID       int16
	headerPage   uint32
	blocks       uint32
	extents      uint32
	bytes        uint64
	tablespace   string
	tablespaceID uint32
}

type dataValue struct {
	value any
}

type dmNumber string
type dmBinary []byte
type dmRowID string

type dmLOBReader struct {
	cache *dataFilePageCache
}

type dmLOBLocator struct {
	raw       []byte
	lobID     uint32
	byteLen   uint32
	groupID   uint32
	firstPage uint32
}

type dataRowRenderMeta struct {
	partial       bool
	prefixKey     string
	weakPrefixKey string
	coverageKeys  []string
	presentColIDs []uint16
}

type pendingPartialDataRow struct {
	tableID uint32
	line    string
	record  []string
	stats   *DataTableRowCount
	meta    dataRowRenderMeta
}

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

	stream, err := openSystemPageStream(opts.SystemPath)
	if err != nil {
		return nil, err
	}
	defer stream.close()
	pageSize := stream.pageSize

	preferredCharset := strings.ToLower(strings.TrimSpace(opts.Charset))
	if preferredCharset == "" || preferredCharset == "auto" {
		if charset, ok := stream.charset(); ok && charset.DecoderName != "" {
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
	outputFormat := normalizeDataOutputFormat(opts.OutputFormat)
	if outputFormat == "" {
		return nil, fmt.Errorf("unsupported data output format %q", opts.OutputFormat)
	}
	objects, err := stream.dictionaryObjects(decoder)
	if err != nil {
		return nil, err
	}
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
	dictionaryTables := applyDictionaryTableOverrides(opts.Dictionary, tables, nil)

	columnsByTable := make(map[uint32][]columnDef)
	columnCount := 0
	if err := stream.forEachDictionaryRow(func(page []byte, pageNo uint32, slotNo uint16, slotOff uint16) {
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
	}); err != nil {
		return nil, err
	}
	for tableID := range columnsByTable {
		sort.Slice(columnsByTable[tableID], func(i, j int) bool {
			return columnsByTable[tableID][i].ColID < columnsByTable[tableID][j].ColID
		})
	}
	if dictColumnsByTable, _, dictColumnCount, ok := dictionaryColumnMaps(opts.Dictionary, dictionaryTables, tables, ownerMatcher, tableMatcher, excludeMatcher); ok {
		columnsByTable = dictColumnsByTable
		columnCount = dictColumnCount
	}

	indexes := make(map[uint32]indexDef)
	if err := stream.forEachDictionaryRow(func(page []byte, pageNo uint32, slotNo uint16, slotOff uint16) {
		idx, ok := parseDDLIndexRow(page, int(slotOff), pageSize)
		if ok {
			indexes[idx.ID] = idx
		}
	}); err != nil {
		return nil, err
	}

	assistByParentID := assistIndexesByParentID(tables, indexObjects, indexes)
	partitionsByTable, err := stream.partitionsByTable(decoder, tables, ownerMatcher)
	if err != nil {
		return nil, err
	}
	dataStorageByTable := tableStorageByID(tables, indexObjects, indexes, nil)
	dataFiles, err := resolveDataFiles(opts.ControlPath, opts.ControlDULPath, dataDir)
	if err != nil {
		return nil, err
	}
	dataFilePages := newDataFilePageCache(dataFiles, pageSize)
	lobReader := &dmLOBReader{cache: dataFilePages}
	selectedTables := make(map[uint32]dataTableInfo)
	assistByID := make(map[uint32][]dataTableInfo)
	neededFiles := make(map[dataFileKey]bool)
	scanAllDataFiles := false
	if opts.RecoveryMode {
		scanAllDataFiles = true
	}
	for tableID, table := range tables {
		if !ownerMatcher.allowed(table.Owner) || !tableMatcher.allowed(table.Owner, table.Name) || excludeMatcher.allowed(table.Owner, table.Name) {
			continue
		}
		if table.isTemporaryTable() || len(columnsByTable[tableID]) == 0 {
			continue
		}
		baseInfo := dataTableInfo{
			table:           table,
			columns:         columnsByTable[tableID],
			dataStorageID:   dataStorageIDForTable(dictionaryTables, dataStorageByTable, tableID),
			lobReader:       lobReader,
			recoveryMode:    opts.RecoveryMode,
			recoveryGroupID: dictionaryTableGroupID(dictionaryTables, tableID),
			segment:         segmentByTableID(opts.Dictionary, tableID),
			segmentKnown:    hasSegmentRange(opts.Dictionary, tableID),
		}
		selectedTables[tableID] = baseInfo
		for _, storage := range assistByParentID[tableID] {
			addKnownDataAssistID(assistByID, neededFiles, baseInfo, storage.ID, storage, buildStoragePagePlan(storage, dataFilePages))
		}
		for _, assistID := range dictionaryDataAssistIDs(dictionaryTables, tableID) {
			addHistoricalDataAssistID(assistByID, baseInfo, assistID)
		}
		if opts.RecoveryMode {
			for _, assistID := range dictionaryDataAssistIDs(dictionaryTables, tableID) {
				addRecoveryDataAssistID(assistByID, baseInfo, assistID)
			}
		}
		if addHiddenIndexObjectAssistIDs(assistByID, baseInfo, tableID, indexObjects, indexes) {
			scanAllDataFiles = true
		}
		if addUnknownDataAssistID(assistByID, baseInfo, tableDataAssistID(tableID)) {
			scanAllDataFiles = true
		}
		for _, part := range partitionsByTable[tableID] {
			for _, storage := range assistByParentID[part.PartTableID] {
				addKnownDataAssistID(assistByID, neededFiles, baseInfo, storage.ID, storage, buildStoragePagePlan(storage, dataFilePages))
			}
			if addHiddenIndexObjectAssistIDs(assistByID, baseInfo, part.PartTableID, indexObjects, indexes) {
				scanAllDataFiles = true
			}
			if addUnknownDataAssistID(assistByID, baseInfo, tableDataAssistID(part.PartTableID)) {
				scanAllDataFiles = true
			}
		}
	}

	dataFiles = filterNeededDataFiles(dataFiles, neededFiles, scanAllDataFiles)

	result := &DataExportResult{
		SystemPath:       opts.SystemPath,
		OutputPath:       opts.OutputPath,
		DataDir:          dataDir,
		PageSize:         pageSize,
		OutputFormat:     outputFormat,
		ObjectCount:      len(objects),
		TableCount:       len(selectedTables),
		ColumnCount:      columnCount,
		AssistIndexCount: len(assistByID),
		DataFileCount:    len(dataFiles),
	}
	rowStats := initDataTableRowStats(selectedTables)
	if outputFormat == "csv" && len(selectedTables) > 1 {
		return nil, fmt.Errorf("csv export requires exactly one table; selected %d tables", len(selectedTables))
	}

	out, err := os.Create(opts.OutputPath)
	if err != nil {
		return nil, fmt.Errorf("create data output: %w", err)
	}
	writer := bufio.NewWriter(out)

	var csvWriter *csv.Writer
	var csvTable dataTableInfo
	defer func() {
		if csvWriter != nil {
			csvWriter.Flush()
		}
		_ = writer.Flush()
		_ = out.Close()
		if outputFormat == "csv" && result != nil && result.RowsExported == 0 {
			_ = os.Remove(opts.OutputPath)
			result.OutputPath = ""
		}
	}()
	if outputFormat == "csv" {
		csvWriter = csv.NewWriter(writer)
		if table, ok := singleSelectedDataTable(selectedTables); ok {
			csvTable = table
			if err := csvWriter.Write(csvHeaderForDataTable(table)); err != nil {
				return nil, fmt.Errorf("write csv header: %w", err)
			}
		}
	} else {
		fmt.Fprintln(writer, "-- Generated by dmdul export-data. Review before running.")
		fmt.Fprintln(writer, "-- Current decoder targets ordinary in-row heap/cluster/IOT rows.")
		fmt.Fprintln(writer)
	}

	if len(assistByID) == 0 || len(dataFiles) == 0 {
		result.TableRowCounts = finalizeDataTableRowStats(rowStats)
		result.TablesWithoutRows = len(result.TableRowCounts)
		return result, nil
	}

	stop := false
	coveredRowPrefixes := make(map[uint32]map[string]bool)
	var pendingPartialRows []pendingPartialDataRow
	for _, file := range dataFiles {
		if stop {
			break
		}
		pagesScanned, scanErr := forEachDataFilePage(file.path, pageSize, func(page []byte, pageNo uint32) error {
			if stop {
				return errStopPageScan
			}
			if !isProbableDMDataPage(page, pageSize) {
				return nil
			}
			assistIndexID := binary.LittleEndian.Uint32(page[dataPageAssistIndexOff:])
			candidates := assistByID[assistIndexID]
			if len(candidates) == 0 {
				return nil
			}
			nRec := int(binary.LittleEndian.Uint16(page[dataPageRecordCountOff:]))
			if nRec <= 0 {
				return nil
			}
			rows := locateRowsInDataPage(page, pageSize, nRec)
			info, ok := selectDataPageCandidate(candidates, file, pageNo, page, pageSize, rows, decoder)
			if !ok {
				return nil
			}
			for _, row := range rows {
				if opts.MaxRows > 0 && result.RowsLocated >= opts.MaxRows {
					stop = true
					break
				}
				result.RowsLocated++
				rowStart := int(row.offset)
				rowEnd := rowStart + int(row.length)
				rowBytes := append([]byte(nil), page[rowStart:rowEnd]...)
				var line string
				var record []string
				var meta dataRowRenderMeta
				var err error
				if outputFormat == "csv" {
					if info.table.ID != csvTable.table.ID {
						continue
					}
					record, _, _, meta, err = renderCSVForDataRowWithMeta(info, rowBytes, decoder)
				} else {
					line, _, _, meta, err = renderInsertForDataRowWithMeta(info, rowBytes, decoder)
				}
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
				if meta.partial {
					pendingPartialRows = append(pendingPartialRows, pendingPartialDataRow{
						tableID: info.table.ID,
						line:    line,
						record:  record,
						stats:   stats,
						meta:    meta,
					})
					continue
				}
				markCoveredRowPrefixes(coveredRowPrefixes, info.table.ID, meta.coverageKeys)
				result.RowsExported++
				if stats != nil {
					stats.RowsExported++
				}
				if outputFormat == "csv" {
					if err := csvWriter.Write(record); err != nil {
						return fmt.Errorf("write csv row: %w", err)
					}
				} else {
					fmt.Fprintln(writer, line)
				}
			}
			if stop {
				return errStopPageScan
			}
			return nil
		})
		result.PagesScanned += pagesScanned
		if scanErr != nil && scanErr != errStopPageScan {
			return nil, fmt.Errorf("scan data file %s: %w", file.path, scanErr)
		}
		if scanErr == errStopPageScan {
			stop = true
		}
	}
	for _, pending := range pendingPartialRows {
		if coveredRowPrefixes[pending.tableID][pending.meta.prefixKey] || coveredRowPrefixes[pending.tableID][pending.meta.weakPrefixKey] {
			continue
		}
		markCoveredRowPrefixes(coveredRowPrefixes, pending.tableID, pending.meta.coverageKeys)
		result.RowsExported++
		if pending.stats != nil {
			pending.stats.RowsExported++
		}
		if outputFormat == "csv" {
			if err := csvWriter.Write(pending.record); err != nil {
				return nil, fmt.Errorf("write csv row: %w", err)
			}
		} else {
			fmt.Fprintln(writer, pending.line)
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

func addKnownDataAssistID(assistByID map[uint32][]dataTableInfo, neededFiles map[dataFileKey]bool, info dataTableInfo, assistID uint32, storage indexDef, pagePlan map[dataPageRef]bool) {
	if storage.RootFile < 0 {
		return
	}
	info.storage = storage
	info.storageKnown = true
	allowHistoricalRows := shouldAllowHistoricalRows(info, storage.ID)
	if len(pagePlan) > 0 {
		exactInfo := info
		exactInfo.historicalRows = allowHistoricalRows
		exactInfo.pagePlan = pagePlan
		exactInfo.pagePlanKnown = true
		addDataAssistCandidate(assistByID, assistID, exactInfo)
		for ref := range pagePlan {
			neededFiles[ref.key] = true
		}
	}
	info.pagePlan = nil
	info.pagePlanKnown = false
	info.historicalRows = allowHistoricalRows
	addDataAssistCandidate(assistByID, assistID, info)
	neededFiles[dataFileKey{groupID: uint32(storage.GroupID), fileID: storage.RootFile}] = true
}

func addUnknownDataAssistID(assistByID map[uint32][]dataTableInfo, info dataTableInfo, assistID uint32) bool {
	info.storageKnown = false
	before := len(assistByID[assistID])
	addDataAssistCandidate(assistByID, assistID, info)
	return len(assistByID[assistID]) > before
}

func addRecoveryDataAssistID(assistByID map[uint32][]dataTableInfo, info dataTableInfo, assistID uint32) bool {
	info.recoveryMode = true
	info.historicalRows = shouldAllowHistoricalRows(info, assistID)
	info.pagePlan = nil
	info.pagePlanKnown = false
	before := len(assistByID[assistID])
	addDataAssistCandidate(assistByID, assistID, info)
	return len(assistByID[assistID]) > before
}

func addHistoricalDataAssistID(assistByID map[uint32][]dataTableInfo, info dataTableInfo, assistID uint32) bool {
	if info.dataStorageID == 0 {
		return false
	}
	info.historicalRows = shouldAllowHistoricalRows(info, assistID)
	info.pagePlan = nil
	info.pagePlanKnown = false
	info.storageKnown = false
	before := len(assistByID[assistID])
	addDataAssistCandidate(assistByID, assistID, info)
	return len(assistByID[assistID]) > before
}

func addDataAssistCandidate(assistByID map[uint32][]dataTableInfo, assistID uint32, info dataTableInfo) {
	if assistID == 0 || info.table.ID == 0 {
		return
	}
	for _, existing := range assistByID[assistID] {
		if existing.table.ID == info.table.ID && existing.storageKnown == info.storageKnown && existing.storage.ID == info.storage.ID && existing.pagePlanKnown == info.pagePlanKnown && existing.recoveryMode == info.recoveryMode && existing.historicalRows == info.historicalRows {
			return
		}
	}
	assistByID[assistID] = append(assistByID[assistID], info)
}

func addHiddenIndexObjectAssistIDs(assistByID map[uint32][]dataTableInfo, info dataTableInfo, tableID uint32, indexObjects map[uint32]dictionaryObject, indexes map[uint32]indexDef) bool {
	added := false
	for indexID, obj := range indexObjects {
		if uint32(obj.ParentID) != tableID || !isAutoHiddenIndexObject(obj) {
			continue
		}
		if _, ok := indexes[indexID]; ok {
			continue
		}
		if addUnknownDataAssistID(assistByID, info, indexID) {
			added = true
		}
	}
	return added
}

func isAutoHiddenIndexObject(obj dictionaryObject) bool {
	if obj.Type != "TABOBJ" || obj.Subtype != "INDEX" {
		return false
	}
	return strings.EqualFold(obj.Name, fmt.Sprintf("INDEX%d", obj.ID))
}

func tableDataAssistID(tableID uint32) uint32 {
	if tableID == 0 {
		return 0
	}
	return 0x02000000 | (tableID & 0x00FFFFFF)
}

func assistIndexesByParentID(tables map[uint32]dictionaryObject, indexObjects map[uint32]dictionaryObject, indexes map[uint32]indexDef) map[uint32][]indexDef {
	result := make(map[uint32][]indexDef)
	for indexID, obj := range indexObjects {
		idx, ok := indexes[indexID]
		if !ok {
			continue
		}
		parentID := uint32(obj.ParentID)
		table, ok := tables[parentID]
		if !ok {
			continue
		}
		if !isCandidateDataIndex(table, idx) || idx.RootFile < 0 {
			continue
		}
		result[parentID] = append(result[parentID], idx)
	}
	for parentID := range result {
		sort.Slice(result[parentID], func(i, j int) bool {
			return result[parentID][i].ID < result[parentID][j].ID
		})
	}
	return result
}

func isCandidateDataIndex(table dictionaryObject, idx indexDef) bool {
	if idx.Flag&1 != 0 && idx.KeyNum == 0 {
		return true
	}
	return table.isIOTTable() && idx.Flag&0x4 != 0
}

func selectDataPageCandidate(candidates []dataTableInfo, file dataFileRef, pageNo uint32, page []byte, pageSize uint32, rows []locatedDataRow, decoder textDecoder) (dataTableInfo, bool) {
	if len(rows) == 0 {
		return dataTableInfo{}, false
	}
	pageKind := dataPageKind(page)
	pageStorageID := dataPageStorageID(page)
	ordered := append([]dataTableInfo(nil), candidates...)
	sort.SliceStable(ordered, func(i, j int) bool {
		return dataCandidateRank(ordered[i]) < dataCandidateRank(ordered[j])
	})
	for _, candidate := range ordered {
		if isTableDataAssistHeaderCandidate(candidate, pageStorageID, pageKind) {
			continue
		}
		if isLooseHistoricalCandidate(candidate) && pageKind == dmPageKindRowData {
			continue
		}
		if !candidateMatchesFile(candidate, file, pageNo) {
			continue
		}
		limit := len(rows)
		if limit > 3 {
			limit = 3
		}
		for i := 0; i < limit; i++ {
			row := rows[i]
			rowStart := int(row.offset)
			rowEnd := rowStart + int(row.length)
			if rowStart < 0 || rowEnd > int(pageSize) || rowEnd > len(page) {
				continue
			}
			if _, _, _, err := renderInsertForDataRow(candidate, page[rowStart:rowEnd], decoder); err == nil {
				return candidate, true
			}
		}
	}
	return dataTableInfo{}, false
}

func isTableDataAssistHeaderCandidate(info dataTableInfo, pageStorageID uint32, pageKind uint32) bool {
	if info.recoveryMode || info.dataStorageID == 0 || pageKind != dmPageKindRowData {
		return false
	}
	tableAssistID := tableDataAssistID(info.table.ID)
	return pageStorageID == tableAssistID && pageStorageID != info.dataStorageID
}

func isLooseHistoricalCandidate(info dataTableInfo) bool {
	return info.historicalRows && !info.recoveryMode && !info.pagePlanKnown && !info.storageKnown
}

func dataCandidateRank(info dataTableInfo) int {
	switch {
	case info.pagePlanKnown:
		return 0
	case info.recoveryMode:
		return 1
	case info.segmentKnown:
		return 2
	case info.storageKnown:
		return 3
	default:
		return 4
	}
}

func candidateMatchesFile(info dataTableInfo, file dataFileRef, pageNo uint32) bool {
	if info.pagePlanKnown {
		if len(info.pagePlan) == 0 || !info.pagePlan[dataPageRef{key: file.key, pageNo: pageNo}] {
			return false
		}
		if info.recoveryMode {
			return candidateMatchesRecoveryFile(info, file)
		}
		return candidateMatchesSegmentIdentity(info, file)
	}
	if info.recoveryMode {
		return candidateMatchesRecoveryFile(info, file)
	}
	if info.segmentKnown {
		if !candidateMatchesSegmentIdentity(info, file) {
			return false
		}
		if info.segment.blocks > 0 && info.segment.extents <= 1 {
			return pageNo >= info.segment.headerPage && pageNo < info.segment.headerPage+info.segment.blocks
		}
		if info.segment.headerPage > 0 && info.segment.extents <= 1 {
			return pageNo >= info.segment.headerPage
		}
		return true
	}
	if !info.storageKnown {
		return true
	}
	return uint32(info.storage.GroupID) == file.key.groupID && info.storage.RootFile == file.key.fileID
}

func candidateMatchesRecoveryFile(info dataTableInfo, file dataFileRef) bool {
	groupID := info.recoveryGroupID
	if groupID == 0 && info.segmentKnown {
		groupID = info.segment.tablespaceID
	}
	if groupID == 0 && info.storageKnown {
		groupID = uint32(info.storage.GroupID)
	}
	if groupID != 0 && file.key.groupID != groupID {
		return false
	}
	return true
}

func candidateMatchesSegmentIdentity(info dataTableInfo, file dataFileRef) bool {
	if !info.segmentKnown {
		return true
	}
	if uint32(info.segment.fileID) != uint32(file.key.fileID) {
		return false
	}
	if info.segment.tablespaceID != 0 && info.segment.tablespaceID != file.key.groupID {
		return false
	}
	return true
}

func segmentByTableID(dict *DictionaryInfo, tableID uint32) tableSegment {
	if dict == nil {
		return tableSegment{}
	}
	for _, table := range dict.Tables {
		if table.ID != tableID || !dictionaryTableHasSegment(table) {
			continue
		}
		return tableSegment{
			fileID:       table.HeaderFile,
			headerPage:   table.HeaderBlock,
			blocks:       table.Blocks,
			extents:      table.Extents,
			bytes:        table.Bytes,
			tablespace:   table.Tablespace,
			tablespaceID: table.GroupID,
		}
	}
	return tableSegment{}
}

func hasSegmentRange(dict *DictionaryInfo, tableID uint32) bool {
	if dict == nil {
		return false
	}
	for _, table := range dict.Tables {
		if table.ID == tableID {
			return dictionaryTableHasSegment(table)
		}
	}
	return false
}

func dictionaryTableHasSegment(table DictionaryTable) bool {
	return table.HeaderBlock > 0 && table.Blocks > 0
}

func dictionaryTableGroupID(tables map[uint32]DictionaryTable, tableID uint32) uint32 {
	table, ok := tables[tableID]
	if !ok {
		return 0
	}
	return table.GroupID
}

func dataStorageIDForTable(dictionaryTables map[uint32]DictionaryTable, dataStorageByTable map[uint32]indexDef, tableID uint32) uint32 {
	if table, ok := dictionaryTables[tableID]; ok && table.StorageID != 0 {
		return table.StorageID
	}
	if storage, ok := dataStorageByTable[tableID]; ok {
		return storage.ID
	}
	return 0
}

func shouldAllowHistoricalRows(info dataTableInfo, assistID uint32) bool {
	return info.dataStorageID != 0 && assistID != 0
}

func dictionaryDataAssistIDs(tables map[uint32]DictionaryTable, tableID uint32) []uint32 {
	table, ok := tables[tableID]
	if !ok {
		return nil
	}
	seen := make(map[uint32]bool)
	var result []uint32
	add := func(id uint32) {
		if id == 0 || seen[id] {
			return
		}
		seen[id] = true
		result = append(result, id)
	}
	add(table.StorageID)
	for _, id := range table.AssistIDs {
		add(id)
	}
	add(tableDataAssistID(tableID))
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

func resolveDataFiles(controlPath string, controlDULPath string, dataDir string) ([]dataFileRef, error) {
	var refs []dataFileRef
	tablespaceNames := defaultTablespaceNames()
	mergeControlDULTablespaceNames(tablespaceNames, controlDULPath)
	seenKeys := make(map[dataFileKey]bool)
	addRef := func(key dataFileKey, path string, tablespaceName string) {
		if key.groupID < 4 || path == "" || seenKeys[key] {
			return
		}
		refs = append(refs, dataFileRef{
			key:            key,
			path:           path,
			tablespaceName: tablespaceName,
		})
		seenKeys[key] = true
	}
	if strings.TrimSpace(controlPath) != "" {
		ctl, err := InspectControlFile(controlPath)
		if err != nil {
			return nil, fmt.Errorf("inspect dm.ctl: %w", err)
		}
		for _, entry := range ctl.Entries {
			tablespaceNames[entry.ID] = entry.Name
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
				addRef(dataFileKey{groupID: entry.ID, fileID: fileID}, resolved, entry.Name)
				fileID++
			}
		}
	}
	if files, ok := readControlDUL(controlDULPath); ok {
		for _, file := range files {
			if file.GroupID < 4 || strings.TrimSpace(file.Path) == "" {
				continue
			}
			if file.Tablespace != "" {
				tablespaceNames[file.GroupID] = file.Tablespace
			}
			resolved, ok := resolveDataFilePath(file.Path, dataDir)
			if !ok {
				continue
			}
			addRef(dataFileKey{groupID: file.GroupID, fileID: file.FileID}, resolved, tablespaceNames[file.GroupID])
		}
	}
	refs = append(refs, scanDataFilesByPageHeader(dataDir, tablespaceNames, seenKeys)...)
	sort.Slice(refs, func(i, j int) bool {
		if refs[i].key.groupID != refs[j].key.groupID {
			return refs[i].key.groupID < refs[j].key.groupID
		}
		return refs[i].key.fileID < refs[j].key.fileID
	})
	return refs, nil
}

func scanDataFilesByPageHeader(dataDir string, tablespaceNames map[uint32]string, seenKeys map[dataFileKey]bool) []dataFileRef {
	if dataDir == "" {
		return nil
	}
	entries, err := os.ReadDir(dataDir)
	if err != nil {
		return nil
	}
	var refs []dataFileRef
	for _, entry := range entries {
		if entry.IsDir() || !strings.EqualFold(filepath.Ext(entry.Name()), ".DBF") {
			continue
		}
		path := filepath.Join(dataDir, entry.Name())
		key, ok := dataFileKeyFromPageHeader(path)
		if !ok || key.groupID < 4 || seenKeys[key] {
			continue
		}
		tablespaceName := tablespaceNames[key.groupID]
		if tablespaceName == "" {
			tablespaceName = inferTablespaceNameFromDataFile(path, key.groupID)
			tablespaceNames[key.groupID] = tablespaceName
		}
		refs = append(refs, dataFileRef{
			key:            key,
			path:           path,
			tablespaceName: tablespaceName,
		})
		seenKeys[key] = true
	}
	return refs
}

func dataFileKeyFromPageHeader(path string) (dataFileKey, bool) {
	f, err := os.Open(path)
	if err != nil {
		return dataFileKey{}, false
	}
	defer f.Close()
	var head [8]byte
	if _, err := f.Read(head[:]); err != nil {
		return dataFileKey{}, false
	}
	pageNo := binary.LittleEndian.Uint32(head[4:])
	if pageNo != 0 {
		return dataFileKey{}, false
	}
	fileID := binary.LittleEndian.Uint16(head[2:])
	if fileID > uint16(^uint16(0)>>1) {
		return dataFileKey{}, false
	}
	return dataFileKey{
		groupID: uint32(binary.LittleEndian.Uint16(head[0:])),
		fileID:  int16(fileID),
	}, true
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

func filterNeededDataFiles(files []dataFileRef, needed map[dataFileKey]bool, scanAll bool) []dataFileRef {
	if scanAll {
		return files
	}
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
	kind := dataPageKind(page)
	if nSlot >= 2048 {
		return false
	}
	if nRec > nSlot {
		return false
	}
	if treeLevel != 0 {
		return false
	}
	if kind != dmPageKindRowData && kind != dmPageKindRowOverflow {
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
	line, dataStart, dataEnd, _, err := renderInsertForDataRowWithMeta(info, row, decoder)
	return line, dataStart, dataEnd, err
}

func renderInsertForDataRowWithMeta(info dataTableInfo, row []byte, decoder textDecoder) (string, int, int, dataRowRenderMeta, error) {
	values, dataStart, dataEnd, err := parseDataRowValues(row, info.columns, decoder, info.historicalRows, info.lobReader)
	if err != nil {
		return "", 0, 0, dataRowRenderMeta{}, err
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
			return "", 0, 0, dataRowRenderMeta{}, err
		}
		vals = append(vals, sqlValue)
	}
	sql := fmt.Sprintf("INSERT INTO %s.%s (%s) VALUES (%s);",
		quoteIdent(info.table.Owner),
		quoteIdent(info.table.Name),
		strings.Join(cols, ", "),
		strings.Join(vals, ", "),
	)
	return sql, dataStart, dataEnd, dataRowRenderMetaForValues(info.columns, values), nil
}

func renderCSVForDataRow(info dataTableInfo, row []byte, decoder textDecoder) ([]string, int, int, error) {
	record, dataStart, dataEnd, _, err := renderCSVForDataRowWithMeta(info, row, decoder)
	return record, dataStart, dataEnd, err
}

func renderCSVForDataRowWithMeta(info dataTableInfo, row []byte, decoder textDecoder) ([]string, int, int, dataRowRenderMeta, error) {
	values, dataStart, dataEnd, err := parseDataRowValues(row, info.columns, decoder, info.historicalRows, info.lobReader)
	if err != nil {
		return nil, 0, 0, dataRowRenderMeta{}, err
	}
	record := make([]string, 0, len(info.columns))
	for _, col := range info.columns {
		value, ok := values[col.ColID]
		if !ok {
			record = append(record, "")
			continue
		}
		csvValue, err := csvValueForDataColumn(col, value.value)
		if err != nil {
			return nil, 0, 0, dataRowRenderMeta{}, err
		}
		record = append(record, csvValue)
	}
	return record, dataStart, dataEnd, dataRowRenderMetaForValues(info.columns, values), nil
}

func csvHeaderForDataTable(info dataTableInfo) []string {
	header := make([]string, 0, len(info.columns))
	for _, col := range info.columns {
		header = append(header, col.Name)
	}
	return header
}

func dataRowRenderMetaForValues(columns []columnDef, values map[uint16]dataValue) dataRowRenderMeta {
	ordered := append([]columnDef(nil), columns...)
	sort.SliceStable(ordered, func(i, j int) bool { return ordered[i].ColID < ordered[j].ColID })
	var present []columnDef
	for _, col := range ordered {
		if _, ok := values[col.ColID]; !ok {
			break
		}
		present = append(present, col)
	}
	meta := dataRowRenderMeta{
		partial: len(present) < len(ordered),
	}
	for _, col := range present {
		meta.presentColIDs = append(meta.presentColIDs, col.ColID)
	}
	if len(present) > 0 {
		meta.prefixKey = dataRowPrefixKey(present, values)
		meta.weakPrefixKey = dataRowPrefixKey(present[:1], values)
	}
	for keep := 1; keep <= len(present); keep++ {
		meta.coverageKeys = append(meta.coverageKeys, dataRowPrefixKey(present[:keep], values))
	}
	return meta
}

func dataRowPrefixKey(columns []columnDef, values map[uint16]dataValue) string {
	var parts []string
	for _, col := range columns {
		value, ok := values[col.ColID]
		if !ok {
			break
		}
		parts = append(parts, fmt.Sprintf("%d=%s", col.ColID, dataValueSignature(value.value)))
	}
	return strings.Join(parts, "|")
}

func dataValueSignature(value any) string {
	switch v := value.(type) {
	case nil:
		return "NULL"
	case dmBinary:
		return "BIN:" + hex.EncodeToString(v)
	default:
		return fmt.Sprintf("%T:%v", value, value)
	}
}

func markCoveredRowPrefixes(covered map[uint32]map[string]bool, tableID uint32, keys []string) {
	if len(keys) == 0 {
		return
	}
	tableKeys := covered[tableID]
	if tableKeys == nil {
		tableKeys = make(map[string]bool)
		covered[tableID] = tableKeys
	}
	for _, key := range keys {
		if key != "" {
			tableKeys[key] = true
		}
	}
}

func singleSelectedDataTable(tables map[uint32]dataTableInfo) (dataTableInfo, bool) {
	if len(tables) != 1 {
		return dataTableInfo{}, false
	}
	for _, table := range tables {
		return table, true
	}
	return dataTableInfo{}, false
}

func normalizeDataOutputFormat(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "" {
		return "sql"
	}
	switch value {
	case "sql", "csv":
		return value
	default:
		return ""
	}
}

func parseDataRowValues(row []byte, columns []columnDef, decoder textDecoder, allowHistoricalRows bool, lobReader *dmLOBReader) (map[uint16]dataValue, int, int, error) {
	values, start, end, err := parseDataRowValuesForColumns(row, columns, decoder, lobReader)
	if err == nil {
		return values, start, end, nil
	}
	if !allowHistoricalRows {
		return nil, 0, 0, err
	}
	firstErr := err
	for _, historicalColumns := range historicalColumnPrefixes(columns) {
		values, start, end, err = parseDataRowValuesForColumns(row, historicalColumns, decoder, lobReader)
		if err == nil {
			return values, start, end, nil
		}
	}
	return nil, 0, 0, firstErr
}

func historicalColumnPrefixes(columns []columnDef) [][]columnDef {
	if len(columns) <= 1 {
		return nil
	}
	ordered := append([]columnDef(nil), columns...)
	sort.SliceStable(ordered, func(i, j int) bool { return ordered[i].ColID < ordered[j].ColID })
	var result [][]columnDef
	for keep := len(ordered) - 1; keep >= 1; keep-- {
		if !canOmitHistoricalColumns(ordered[keep:]) {
			break
		}
		result = append(result, append([]columnDef(nil), ordered[:keep]...))
	}
	return result
}

func canOmitHistoricalColumns(columns []columnDef) bool {
	if len(columns) == 0 {
		return false
	}
	for _, col := range columns {
		if !isNullableColumn(col) && strings.TrimSpace(col.Default) == "" {
			return false
		}
	}
	return true
}

func dataStorageColumns(columns []columnDef) []columnDef {
	ordered := append([]columnDef(nil), columns...)
	sort.SliceStable(ordered, func(i, j int) bool { return ordered[i].ColID < ordered[j].ColID })
	var fixedCols []columnDef
	var varCols []columnDef
	for _, col := range ordered {
		switch {
		case isVariableDataType(col.DataType):
			varCols = append(varCols, col)
		case fixedDataSizeForColumn(col) > 0:
			fixedCols = append(fixedCols, col)
		default:
			varCols = append(varCols, col)
		}
	}
	return append(fixedCols, varCols...)
}

func rowMetadataLength(columnCount int) int {
	if columnCount <= 0 {
		return 0
	}
	return (columnCount + 3) / 4
}

func decodeRowColumnStates(raw []byte, columnCount int) []byte {
	states := make([]byte, columnCount)
	for i := 0; i < columnCount; i++ {
		b := raw[i/4]
		states[i] = (b >> uint((i%4)*2)) & 0x03
	}
	return states
}

func isRowColumnNull(state byte) bool {
	return state == 0x03
}

func isRowColumnOutOfLine(state byte) bool {
	return state == 0x01
}

func readInRowDataValue(col columnDef, row []byte, pos int, decoder textDecoder, lobReader *dmLOBReader) (any, int, error) {
	if fixedDataSizeForColumn(col) > 0 {
		return parseFixedDataValue(col, row, pos)
	}
	return readVariableDataValue(col, row, pos, decoder, lobReader)
}

func readOutOfLineDataValue(col columnDef, row []byte, pos int, decoder textDecoder, lobReader *dmLOBReader) (any, int, error) {
	locator, next, err := readDMLOBLocator(row, pos)
	if err != nil {
		return nil, pos, fmt.Errorf("%s: %w", col.Name, err)
	}
	if lobReader == nil {
		return nil, pos, fmt.Errorf("%s: out-of-line locator cannot be resolved without data files", col.Name)
	}
	if isBinaryDataType(col.DataType) {
		payload, err := lobReader.readLOBPayload(locator, dmPageKindLOBData)
		if err != nil {
			return nil, pos, fmt.Errorf("%s: %w", col.Name, err)
		}
		return dmBinary(payload), next, nil
	}
	if isCharacterLOBDataType(col.DataType) {
		payload, err := lobReader.readTextLOBOrLongRowPayload(locator)
		if err != nil {
			return nil, pos, fmt.Errorf("%s: %w", col.Name, err)
		}
		value, ok := decoder.decode(payload)
		if !ok || strings.ContainsRune(value, '\uFFFD') || containsBadControl(value) {
			return nil, pos, fmt.Errorf("%s: cannot decode out-of-line text LOB", col.Name)
		}
		return value, next, nil
	}
	if isCharacterDataType(col.DataType) {
		payload, err := lobReader.readLongRowPayload(locator)
		if err != nil {
			return nil, pos, fmt.Errorf("%s: %w", col.Name, err)
		}
		value, ok := decoder.decode(payload)
		if !ok || strings.ContainsRune(value, '\uFFFD') || containsBadControl(value) {
			return nil, pos, fmt.Errorf("%s: cannot decode out-of-line long row text", col.Name)
		}
		return value, next, nil
	}
	return nil, pos, fmt.Errorf("%s: unsupported out-of-line data type %s", col.Name, col.DataType)
}

func parseDataRowValuesForColumns(row []byte, columns []columnDef, decoder textDecoder, lobReader *dmLOBReader) (map[uint16]dataValue, int, int, error) {
	values, start, end, err := parseDataRowValuesWithMetadata(row, columns, decoder, lobReader)
	if err == nil {
		return values, start, end, nil
	}
	metadataErr := err
	values, start, end, err = parseDataRowValuesHeuristic(row, columns, decoder, lobReader)
	if err == nil {
		return values, start, end, nil
	}
	return nil, 0, 0, fmt.Errorf("%v; heuristic: %w", metadataErr, err)
}

func parseDataRowValuesWithMetadata(row []byte, columns []columnDef, decoder textDecoder, lobReader *dmLOBReader) (map[uint16]dataValue, int, int, error) {
	if len(columns) == 0 {
		return nil, 0, 0, fmt.Errorf("no columns")
	}
	storageColumns := dataStorageColumns(columns)
	metaLen := rowMetadataLength(len(storageColumns))
	start := 2 + metaLen
	if len(row) < start {
		return nil, 0, 0, fmt.Errorf("row too short for metadata: len=%d metadata=%d", len(row), metaLen)
	}
	states := decodeRowColumnStates(row[2:start], len(storageColumns))
	pos := start
	values := make(map[uint16]dataValue, len(columns))
	for i, col := range storageColumns {
		state := states[i]
		switch {
		case isRowColumnNull(state):
			if fixedDataSizeForColumn(col) > 0 {
				pos += fixedDataSizeForColumn(col)
				if pos > len(row) {
					return nil, 0, 0, fmt.Errorf("%s fixed NULL out of range", col.Name)
				}
			}
			values[col.ColID] = dataValue{value: nil}
		case isRowColumnOutOfLine(state):
			value, next, err := readOutOfLineDataValue(col, row, pos, decoder, lobReader)
			if err != nil {
				return nil, 0, 0, err
			}
			values[col.ColID] = dataValue{value: value}
			pos = next
		default:
			value, next, err := readInRowDataValue(col, row, pos, decoder, lobReader)
			if err != nil {
				return nil, 0, 0, err
			}
			values[col.ColID] = dataValue{value: value}
			pos = next
		}
	}
	trailing := len(row) - pos
	if trailing < 0 || trailing > 64 {
		return nil, 0, 0, fmt.Errorf("bad trailing length %d", trailing)
	}
	return values, start, pos, nil
}

func parseDataRowValuesHeuristic(row []byte, columns []columnDef, decoder textDecoder, lobReader *dmLOBReader) (map[uint16]dataValue, int, int, error) {
	var fixedCols []columnDef
	var varCols []columnDef
	for _, col := range columns {
		switch {
		case isVariableDataType(col.DataType):
			varCols = append(varCols, col)
		case fixedDataSizeForColumn(col) > 0:
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
			value, next, err := parseFixedDataValue(col, row, pos)
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
			value, next, err := readVariableDataValue(col, row, pos, decoder, lobReader)
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
		if trailing > 0 && trailing < 8 {
			errors = append(errors, fmt.Sprintf("start=%d short trailing length %d", start, trailing))
			continue
		}
		score := 100 - trailing - start*4
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
	return fixedDataSizeForType(normalizeDataType(dataType), 0)
}

func fixedDataSizeForColumn(col columnDef) int {
	return fixedDataSizeForType(normalizeDataType(col.DataType), col.Length)
}

func fixedDataSizeForType(dataType string, length uint32) int {
	switch dataType {
	case "TINYINT":
		return 1
	case "SMALLINT":
		return 2
	case "INT", "INTEGER":
		return 4
	case "BIGINT":
		return 8
	case "REAL":
		return 4
	case "FLOAT":
		if length == 4 {
			return 4
		}
		return 8
	case "DOUBLE", "DOUBLE PRECISION":
		return 8
	case "DATE":
		return 3
	case "TIME":
		return 5
	case "TIME WITH TIME ZONE":
		return 7
	case "DATETIME", "TIMESTAMP", "TIMESTAMP WITH LOCAL TIME ZONE":
		return 8
	case "DATETIME WITH TIME ZONE", "TIMESTAMP WITH TIME ZONE":
		return 10
	case "INTERVAL DAY TO SECOND":
		return 24
	case "ROWID":
		return 12
	default:
		return 0
	}
}

func normalizeDataType(dataType string) string {
	upper := strings.ToUpper(strings.TrimSpace(dataType))
	if idx := strings.IndexByte(upper, '('); idx >= 0 {
		if end := strings.IndexByte(upper[idx:], ')'); end >= 0 {
			upper = strings.TrimSpace(upper[:idx] + " " + upper[idx+end+1:])
		} else {
			upper = strings.TrimSpace(upper[:idx])
		}
	}
	return strings.Join(strings.Fields(upper), " ")
}

func parseFixedDataValue(col columnDef, row []byte, pos int) (any, int, error) {
	dataType := normalizeDataType(col.DataType)
	size := fixedDataSizeForColumn(col)
	if size > 0 && pos+size <= len(row) && isNullableColumn(col) && isFixedNullSentinel(dataType, row[pos:pos+size]) {
		return nil, pos + size, nil
	}
	switch dataType {
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
	case "REAL":
		if pos+4 > len(row) {
			return nil, pos, fmt.Errorf("REAL out of range")
		}
		return math.Float32frombits(binary.LittleEndian.Uint32(row[pos:])), pos + 4, nil
	case "FLOAT":
		if size == 4 {
			if pos+4 > len(row) {
				return nil, pos, fmt.Errorf("FLOAT out of range")
			}
			return math.Float32frombits(binary.LittleEndian.Uint32(row[pos:])), pos + 4, nil
		}
		if pos+8 > len(row) {
			return nil, pos, fmt.Errorf("FLOAT out of range")
		}
		return math.Float64frombits(binary.LittleEndian.Uint64(row[pos:])), pos + 8, nil
	case "DOUBLE", "DOUBLE PRECISION":
		if pos+8 > len(row) {
			return nil, pos, fmt.Errorf("%s out of range", dataType)
		}
		return math.Float64frombits(binary.LittleEndian.Uint64(row[pos:])), pos + 8, nil
	case "DATE":
		if pos+3 > len(row) {
			return nil, pos, fmt.Errorf("DATE out of range")
		}
		value, err := decodeDMDate(row[pos : pos+3])
		if err != nil {
			return nil, pos, err
		}
		return value, pos + 3, nil
	case "TIME":
		if pos+5 > len(row) {
			return nil, pos, fmt.Errorf("TIME out of range")
		}
		value, err := decodeDMTime(row[pos : pos+5])
		if err != nil {
			return nil, pos, err
		}
		return value, pos + 5, nil
	case "TIME WITH TIME ZONE":
		if pos+7 > len(row) {
			return nil, pos, fmt.Errorf("TIME WITH TIME ZONE out of range")
		}
		value, err := decodeDMTime(row[pos : pos+5])
		if err != nil {
			return nil, pos, err
		}
		return value + " " + decodeDMTimezone(row[pos+5:pos+7]), pos + 7, nil
	case "DATETIME", "TIMESTAMP", "TIMESTAMP WITH LOCAL TIME ZONE":
		if pos+8 > len(row) {
			return nil, pos, fmt.Errorf("%s out of range", dataType)
		}
		value, err := decodeDMDateTime(row[pos : pos+8])
		if err != nil {
			return nil, pos, err
		}
		return value, pos + 8, nil
	case "DATETIME WITH TIME ZONE", "TIMESTAMP WITH TIME ZONE":
		if pos+10 > len(row) {
			return nil, pos, fmt.Errorf("%s out of range", dataType)
		}
		value, err := decodeDMDateTime(row[pos : pos+8])
		if err != nil {
			return nil, pos, err
		}
		return value + " " + decodeDMTimezone(row[pos+8:pos+10]), pos + 10, nil
	case "INTERVAL DAY TO SECOND":
		if pos+24 > len(row) {
			return nil, pos, fmt.Errorf("INTERVAL DAY TO SECOND out of range")
		}
		return decodeDMIntervalDayToSecond(row[pos : pos+24]), pos + 24, nil
	case "ROWID":
		if pos+12 > len(row) {
			return nil, pos, fmt.Errorf("ROWID out of range")
		}
		return dmRowID(strings.ToUpper(hex.EncodeToString(row[pos : pos+12]))), pos + 12, nil
	default:
		return nil, pos, fmt.Errorf("unsupported fixed type: %s", dataType)
	}
}

func isFixedNullSentinel(dataType string, raw []byte) bool {
	switch dataType {
	case "DATE":
		if len(raw) != 3 {
			return false
		}
		return raw[0] == 0 && raw[1] == 0 && raw[2] != 0
	case "TIME":
		if len(raw) != 5 {
			return false
		}
		if isAllBytes(raw, 0x00) {
			return true
		}
		if raw[0] == 0 && raw[1] == 0 {
			return true
		}
		return raw[0] == 0xFF && raw[1] == 0xFF && raw[4] == 0x7F
	case "TIME WITH TIME ZONE":
		if len(raw) != 7 {
			return false
		}
		return isFixedNullSentinel("TIME", raw[:5])
	case "DATETIME", "TIMESTAMP", "TIMESTAMP WITH LOCAL TIME ZONE":
		if len(raw) != 8 {
			return false
		}
		if isAllBytes(raw, 0x00) {
			return true
		}
		if raw[0] == 0 && raw[1] == 0 {
			return true
		}
		return raw[0] == 0xFF && raw[1] == 0xFF && raw[4] == 0x7F
	case "DATETIME WITH TIME ZONE", "TIMESTAMP WITH TIME ZONE":
		if len(raw) != 10 {
			return false
		}
		return isFixedNullSentinel("DATETIME", raw[:8])
	default:
		return false
	}
}

func isAllBytes(raw []byte, value byte) bool {
	if len(raw) == 0 {
		return false
	}
	for _, b := range raw {
		if b != value {
			return false
		}
	}
	return true
}

func readVariableDataValue(col columnDef, row []byte, pos int, decoder textDecoder, lobReader *dmLOBReader) (any, int, error) {
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
		} else if locator, ok := parseDMLOBLocatorRaw(value); ok {
			if lobReader == nil {
				return nil, pos, fmt.Errorf("%s: out-of-line binary LOB locator cannot be resolved without data files", col.Name)
			}
			value, err = lobReader.readLOBPayload(locator, dmPageKindLOBData)
			if err != nil {
				return nil, pos, fmt.Errorf("%s: %w", col.Name, err)
			}
		}
		return dmBinary(value), next, nil
	}
	if isCharacterLOBDataType(col.DataType) {
		value, next, err := readInlineTextLOB(row, pos, decoder, lobReader)
		if err != nil {
			return nil, pos, fmt.Errorf("%s: %w", col.Name, err)
		}
		return value, next, nil
	}
	raw, next, marker, err := readShortDataBytesWithMarker(row, pos)
	if err != nil {
		return nil, pos, fmt.Errorf("%s: %w", col.Name, err)
	}
	if locator, ok := parseDMLOBLocatorRaw(raw); ok {
		if lobReader == nil {
			return nil, pos, fmt.Errorf("%s: out-of-line long row locator cannot be resolved without data files", col.Name)
		}
		raw, err = lobReader.readLongRowPayload(locator)
		if err != nil {
			return nil, pos, fmt.Errorf("%s: %w", col.Name, err)
		}
	}
	value, ok := decoder.decode(raw)
	if !ok {
		return nil, pos, fmt.Errorf("%s: cannot decode varchar marker=0x%02X raw=%s", col.Name, marker, strings.ToUpper(hex.EncodeToString(raw)))
	}
	if strings.ContainsRune(value, '\uFFFD') || containsBadControl(value) {
		return nil, pos, fmt.Errorf("%s: decoded varchar contains invalid characters marker=0x%02X raw=%s", col.Name, marker, strings.ToUpper(hex.EncodeToString(raw)))
	}
	return value, next, nil
}

func readInlineTextLOB(row []byte, pos int, decoder textDecoder, lobReader *dmLOBReader) (string, int, error) {
	raw, next, marker, err := readShortDataBytesWithMarker(row, pos)
	if err != nil {
		return "", pos, fmt.Errorf("%s", strings.Replace(err.Error(), "raw value", "text LOB", 1))
	}
	if payload, ok := unwrapInlineLOBPayload(raw); ok {
		raw = payload
	} else if locator, ok := parseDMLOBLocatorRaw(raw); ok {
		if lobReader == nil {
			return "", pos, fmt.Errorf("out-of-line text LOB locator cannot be resolved without data files")
		}
		raw, err = lobReader.readTextLOBOrLongRowPayload(locator)
		if err != nil {
			return "", pos, err
		}
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
	if raw[0] != 0x01 || raw[2] != 0x04 {
		return nil, false
	}
	payloadLen := int(binary.LittleEndian.Uint32(raw[9:13]))
	if payloadLen < 0 || payloadLen != len(raw)-13 {
		return nil, false
	}
	return append([]byte(nil), raw[13:]...), true
}

func readDMLOBLocator(row []byte, pos int) (dmLOBLocator, int, error) {
	if pos < 0 || pos+dmLOBLocatorSize > len(row) {
		return dmLOBLocator{}, pos, fmt.Errorf("LOB locator out of range")
	}
	raw := append([]byte(nil), row[pos:pos+dmLOBLocatorSize]...)
	locator, ok := parseDMLOBLocatorRaw(raw)
	if !ok {
		return dmLOBLocator{}, pos, fmt.Errorf("invalid LOB locator %s", strings.ToUpper(hex.EncodeToString(raw)))
	}
	return locator, pos + dmLOBLocatorSize, nil
}

func parseDMLOBLocatorRaw(raw []byte) (dmLOBLocator, bool) {
	if len(raw) != dmLOBLocatorSize || raw[0] != 0x02 {
		return dmLOBLocator{}, false
	}
	locator := dmLOBLocator{
		raw:       append([]byte(nil), raw...),
		lobID:     binary.LittleEndian.Uint32(raw[1:5]),
		byteLen:   binary.LittleEndian.Uint32(raw[9:13]),
		groupID:   binary.LittleEndian.Uint32(raw[13:17]),
		firstPage: binary.LittleEndian.Uint32(raw[17:21]),
	}
	if locator.lobID == 0 || locator.groupID == 0 || locator.firstPage == 0 {
		return dmLOBLocator{}, false
	}
	return locator, true
}

func (r *dmLOBReader) readLOBPayload(locator dmLOBLocator, kind uint32) ([]byte, error) {
	if r == nil || r.cache == nil {
		return nil, fmt.Errorf("LOB reader is not available")
	}
	start, ok := r.findFirstLOBPage(locator, kind)
	if !ok {
		return nil, fmt.Errorf("LOB page not found: lob_id=%d group=%d page=%d kind=0x%X", locator.lobID, locator.groupID, locator.firstPage, kind)
	}
	var out []byte
	current := start
	seen := make(map[dataPageRef]bool)
	maxSteps := r.cache.totalPageCount() * maxLeafChainWalkMultiplier
	if maxSteps <= 0 {
		maxSteps = 1
	}
	for steps := 0; steps < maxSteps && len(out) < int(locator.byteLen); steps++ {
		if seen[current] {
			break
		}
		seen[current] = true
		page, ok := r.cache.readPage(current)
		if !ok || !pageHeaderMatchesRef(page, current) || dataPageKind(page) != kind || lobPageID(page) != locator.lobID {
			break
		}
		payloadLen := int(lobPagePayloadLen(page))
		if payloadLen < 0 || dmLOBPagePayloadOff+payloadLen > len(page) {
			return nil, fmt.Errorf("bad LOB payload length %d at page %d", payloadLen, current.pageNo)
		}
		out = append(out, page[dmLOBPagePayloadOff:dmLOBPagePayloadOff+payloadLen]...)
		nextFileID, nextPageNo, ok := readDMPageRef(page, dmPageNextRefOff)
		if !ok {
			break
		}
		current = dataPageRef{
			key: dataFileKey{
				groupID: locator.groupID,
				fileID:  nextFileID,
			},
			pageNo: nextPageNo,
		}
	}
	if len(out) < int(locator.byteLen) {
		return nil, fmt.Errorf("LOB payload incomplete: got=%d want=%d", len(out), locator.byteLen)
	}
	return append([]byte(nil), out[:int(locator.byteLen)]...), nil
}

func (r *dmLOBReader) readTextLOBOrLongRowPayload(locator dmLOBLocator) ([]byte, error) {
	payload, err := r.readLOBPayload(locator, dmPageKindLOBData)
	if err == nil {
		return payload, nil
	}
	longPayload, longErr := r.readLongRowPayload(locator)
	if longErr == nil {
		return longPayload, nil
	}
	return nil, err
}

func (r *dmLOBReader) readLongRowPayload(locator dmLOBLocator) ([]byte, error) {
	if r == nil || r.cache == nil {
		return nil, fmt.Errorf("LOB reader is not available")
	}
	start, ok := r.findFirstLOBPage(locator, dmPageKindLongRowData)
	if !ok {
		return nil, fmt.Errorf("long-row page not found: lob_id=%d group=%d page=%d", locator.lobID, locator.groupID, locator.firstPage)
	}
	current := start
	seen := make(map[dataPageRef]bool)
	maxSteps := r.cache.totalPageCount() * maxLeafChainWalkMultiplier
	if maxSteps <= 0 {
		maxSteps = 1
	}
	for steps := 0; steps < maxSteps; steps++ {
		if seen[current] {
			break
		}
		seen[current] = true
		page, ok := r.cache.readPage(current)
		if !ok || !pageHeaderMatchesRef(page, current) || dataPageKind(page) != dmPageKindLongRowData {
			break
		}
		if payload, ok := longRowPayloadFromPage(page, locator); ok {
			return payload, nil
		}
		nextFileID, nextPageNo, ok := readDMPageRef(page, dmPageNextRefOff)
		if !ok {
			break
		}
		current = dataPageRef{
			key: dataFileKey{
				groupID: locator.groupID,
				fileID:  nextFileID,
			},
			pageNo: nextPageNo,
		}
	}
	return nil, fmt.Errorf("long-row payload not found: lob_id=%d", locator.lobID)
}

func (r *dmLOBReader) findFirstLOBPage(locator dmLOBLocator, kind uint32) (dataPageRef, bool) {
	if r == nil || r.cache == nil {
		return dataPageRef{}, false
	}
	for key := range r.cache.refs {
		if key.groupID != locator.groupID {
			continue
		}
		ref := dataPageRef{key: key, pageNo: locator.firstPage}
		page, ok := r.cache.readPage(ref)
		if !ok || !pageHeaderMatchesRef(page, ref) || dataPageKind(page) != kind {
			continue
		}
		if kind == dmPageKindLOBData && lobPageID(page) != locator.lobID {
			continue
		}
		if kind == dmPageKindLongRowData {
			if _, ok := longRowPayloadFromPage(page, locator); !ok {
				continue
			}
		}
		return ref, true
	}
	return dataPageRef{}, false
}

func lobPageID(page []byte) uint32 {
	if len(page) < dmLOBPageIDOff+4 {
		return 0
	}
	return binary.LittleEndian.Uint32(page[dmLOBPageIDOff:])
}

func lobPagePayloadLen(page []byte) uint16 {
	if len(page) < dmLOBPagePayloadLenOff+2 {
		return 0
	}
	return binary.LittleEndian.Uint16(page[dmLOBPagePayloadLenOff:])
}

func longRowPayloadFromPage(page []byte, locator dmLOBLocator) ([]byte, bool) {
	for off := dmLOBPagePayloadOff; off+0x0E <= len(page); off++ {
		recordLen := int(binary.BigEndian.Uint16(page[off:]))
		if recordLen < 0x0E || off+recordLen > len(page) {
			continue
		}
		if binary.LittleEndian.Uint32(page[off+0x02:off+0x06]) != locator.lobID {
			continue
		}
		payloadLen1 := int(binary.LittleEndian.Uint16(page[off+0x0A:]))
		payloadLen2 := int(binary.LittleEndian.Uint16(page[off+0x0C:]))
		payloadLen := payloadLen1
		if payloadLen2 > 0 && payloadLen2 < payloadLen {
			payloadLen = payloadLen2
		}
		if payloadLen <= 0 || payloadLen > int(locator.byteLen) || off+0x0E+payloadLen > off+recordLen {
			continue
		}
		return append([]byte(nil), page[off+0x0E:off+0x0E+payloadLen]...), true
	}
	return nil, false
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
	date, err := decodeDMDate(raw[:3])
	if err != nil {
		return "", fmt.Errorf("%s", strings.Replace(err.Error(), "date", "datetime date", 1))
	}
	timeValue, err := decodeDMTime(raw[3:8])
	if err != nil {
		return "", fmt.Errorf("%s", strings.Replace(err.Error(), "time", "datetime time", 1))
	}
	return date + " " + timeValue, nil
}

func decodeDMTime(raw []byte) (string, error) {
	if len(raw) != 5 {
		return "", fmt.Errorf("time needs 5 bytes")
	}
	v := uint64(raw[0]) | uint64(raw[1])<<8 | uint64(raw[2])<<16 | uint64(raw[3])<<24 | uint64(raw[4])<<32
	hour := int(v & 0x1F)
	minute := int((v >> 5) & 0x3F)
	second := int((v >> 11) & 0x3F)
	micro := int((v >> 17) & ((1 << 23) - 1))
	if hour > 23 || minute > 59 || second > 59 || micro > 999999 {
		return "", fmt.Errorf("invalid datetime time bits: %02d:%02d:%02d.%06d", hour, minute, second, micro)
	}
	return fmt.Sprintf("%02d:%02d:%02d.%06d", hour, minute, second, micro), nil
}

func decodeDMTimezone(raw []byte) string {
	if len(raw) != 2 {
		return "+00:00"
	}
	minutes := int(int16(binary.LittleEndian.Uint16(raw)))
	sign := "+"
	if minutes < 0 {
		sign = "-"
		minutes = -minutes
	}
	return fmt.Sprintf("%s%02d:%02d", sign, minutes/60, minutes%60)
}

func decodeDMIntervalDayToSecond(raw []byte) string {
	if len(raw) < 20 {
		return ""
	}
	day := int32(binary.LittleEndian.Uint32(raw[0:4]))
	hour := int32(binary.LittleEndian.Uint32(raw[4:8]))
	minute := int32(binary.LittleEndian.Uint32(raw[8:12]))
	second := int32(binary.LittleEndian.Uint32(raw[12:16]))
	micro := int32(binary.LittleEndian.Uint32(raw[16:20]))
	return fmt.Sprintf("%d %02d:%02d:%02d.%06d", day, hour, minute, second, micro)
}

func sqlValueForDataColumn(col columnDef, value any) (string, error) {
	if value == nil {
		return "NULL", nil
	}
	typ := normalizeDataType(col.DataType)
	switch typ {
	case "TINYINT", "SMALLINT", "INT", "INTEGER", "BIGINT":
		return fmt.Sprintf("%v", value), nil
	case "REAL", "FLOAT", "DOUBLE", "DOUBLE PRECISION":
		return fmt.Sprintf("%v", value), nil
	case "NUMBER", "NUMERIC", "DEC", "DECIMAL":
		return fmt.Sprintf("%v", value), nil
	case "DATETIME", "TIMESTAMP", "DATETIME WITH TIME ZONE", "TIMESTAMP WITH TIME ZONE", "TIMESTAMP WITH LOCAL TIME ZONE":
		prefix := "DATETIME "
		if strings.HasPrefix(typ, "TIMESTAMP") {
			prefix = "TIMESTAMP "
		}
		return prefix + sqlLiteral(fmt.Sprintf("%v", value)), nil
	case "DATE":
		text := fmt.Sprintf("%v", value)
		if len(text) >= 10 {
			text = text[:10]
		}
		return "DATE " + sqlLiteral(text), nil
	case "TIME", "TIME WITH TIME ZONE":
		return "TIME " + sqlLiteral(fmt.Sprintf("%v", value)), nil
	case "INTERVAL DAY TO SECOND":
		return "INTERVAL " + sqlLiteral(fmt.Sprintf("%v", value)) + " DAY TO SECOND", nil
	case "ROWID":
		return sqlLiteral(fmt.Sprintf("%v", value)), nil
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

func csvValueForDataColumn(col columnDef, value any) (string, error) {
	if value == nil {
		return "", nil
	}
	if raw, ok := value.(dmBinary); ok {
		return strings.ToUpper(hex.EncodeToString(raw)), nil
	}
	text := fmt.Sprintf("%v", value)
	if strings.ContainsRune(text, '\uFFFD') || containsBadControl(text) {
		return "", fmt.Errorf("invalid text value for %s", col.Name)
	}
	return text, nil
}
