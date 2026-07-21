package dm

// Parallel direct-read unload pipeline.
//
// The planned direct-read phase is embarrassingly parallel: every ordinary row
// lives entirely inside one page (slot directory included), so pages can be
// decoded concurrently. Out-of-line LOB and Long Row chains are followed by
// whichever worker owns the anchor row, through the shared mutex-guarded page
// cache — chains are never split across workers.
//
// Workers decode page batches into memory; a single writer goroutine (the
// ExportData caller) applies batches strictly in plan order, so counters,
// coverage marking, pending partial rows and the output bytes are identical
// to the sequential path. Fallback phases (storage scan, segment scan,
// recovery full scan) stay sequential — they are rare diagnostic paths.

import (
	"encoding/binary"
	"fmt"
	"os"
	"runtime"
	"strconv"
)

// directDecodeBatchPages is the work-queue granularity. Batches this size keep
// channel traffic low while letting the queue balance skew from pages whose
// rows carry large LOB chains.
const directDecodeBatchPages = 256

// exportWorkerCount picks the decode parallelism. There is deliberately no
// user-facing knob: extraction speed is the tool's job. DMDUL_UNLOAD_WORKERS
// exists for tests and as an emergency override only.
func exportWorkerCount() int {
	if raw := os.Getenv("DMDUL_UNLOAD_WORKERS"); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil && n > 0 {
			return n
		}
	}
	n := runtime.NumCPU()
	if n > 8 {
		n = 8
	}
	if n < 1 {
		n = 1
	}
	return n
}

type directDecodedRow struct {
	line   string
	record []string
	fields []DMPField
	// dmpSegments holds the row pre-encoded on the worker; nil when the row
	// carries streaming LOB fields, which fall back to writer-side WriteRow.
	dmpSegments      []dmpRowSegment
	meta             dataRowRenderMeta
	timeFractionLoss bool
	failed           bool
	failMsg          string
}

type directPageResult struct {
	ref              dataPageRef
	readErr          error
	validationFailed bool
	skipped          bool
	orphanReason     string
	info             dataTableInfo
	storageID        uint32
	rows             []directDecodedRow
}

// renderDataRowForExport is the single render dispatch shared by the
// sequential processPage path and the parallel decode workers.
func renderDataRowForExport(outputFormat string, info dataTableInfo, rowBytes []byte, decoder textDecoder, dmpCharset dmpCharsetHeader) (line string, record []string, fields []DMPField, meta dataRowRenderMeta, timeFractionLoss bool, err error) {
	switch outputFormat {
	case "csv":
		record, _, _, meta, err = renderCSVForDataRowWithMeta(info, rowBytes, decoder)
	case "dmp":
		fields, _, _, meta, timeFractionLoss, err = renderDMPForDataRowWithMeta(info, rowBytes, decoder, dmpCharset)
	default:
		line, _, _, meta, err = renderInsertForDataRowWithMeta(info, rowBytes, decoder)
	}
	return line, record, fields, meta, timeFractionLoss, err
}

// decodeDirectPlannedRef reads and fully decodes one planned page. It only
// touches worker-local state plus read-only shared structures (candidates,
// dictionary) and the mutex-guarded LOB page cache, so it is safe to run
// concurrently. All bookkeeping is deferred to the writer via the result.
func decodeDirectPlannedRef(
	reader *dataFilePageReader,
	ref dataPageRef,
	file dataFileRef,
	candidates []dataTableInfo,
	pageSize uint32,
	decoder textDecoder,
	outputFormat string,
	dmpCharset dmpCharsetHeader,
	writeFailedComments bool,
) directPageResult {
	res := directPageResult{ref: ref}
	page, err := reader.readPage(ref)
	if err != nil {
		res.readErr = err
		return res
	}
	if !plannedDataPageMatches(page, ref, candidates) {
		res.validationFailed = true
		return res
	}
	if len(candidates) == 0 || !isProbableDMDataPage(page, pageSize) {
		res.skipped = true
		return res
	}
	nRec := int(binary.LittleEndian.Uint16(page[dataPageRecordCountOff:]))
	rows := locateRowsInDataPage(page, pageSize, nRec)
	info, ok := selectDataPageCandidate(candidates, file, ref.pageNo, page, pageSize, rows, decoder)
	if !ok {
		res.skipped = true
		return res
	}
	res.info = info
	res.storageID = dataPageStorageID(page)
	if info.orphanRecovery {
		res.orphanReason = fmt.Sprintf("%s.%s orphan storage recovery is heuristic; verify recovery source group/file/storage_id/page range before import", info.table.Owner, info.table.Name)
	}
	res.rows = make([]directDecodedRow, 0, len(rows))
	for _, row := range rows {
		rowStart := int(row.offset)
		rowEnd := rowStart + int(row.length)
		rowBytes := append([]byte(nil), page[rowStart:rowEnd]...)
		line, record, fields, meta, timeFractionLoss, err := renderDataRowForExport(outputFormat, info, rowBytes, decoder, dmpCharset)
		decoded := directDecodedRow{
			line:             line,
			record:           record,
			fields:           fields,
			meta:             meta,
			timeFractionLoss: timeFractionLoss,
		}
		if err != nil {
			decoded.failed = true
			if writeFailedComments {
				decoded.failMsg = fmt.Sprintf("-- FAILED %s.%s page=%d slot=%d off=0x%X len=%d: %v",
					quoteIdent(info.table.Owner), quoteIdent(info.table.Name), ref.pageNo, row.slotNo, row.offset, row.length, err)
			}
		} else if outputFormat == "dmp" && !meta.partial {
			// Pre-encode on the worker; encode errors fall back to the
			// writer-side WriteRow path, which reports them identically.
			if segments, ok, encodeErr := encodeDMPRowSegments(fields, uint16(len(info.columns))); ok && encodeErr == nil {
				decoded.dmpSegments = segments
			}
		}
		res.rows = append(res.rows, decoded)
	}
	return res
}
