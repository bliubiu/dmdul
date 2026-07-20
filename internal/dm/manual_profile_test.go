package dm

// Manual memory-profiling harness for large-snapshot exports. Not part of the
// regular suite: it only runs when DMDUL_PROFILE_DIR points at a snapshot
// directory (SYSTEM.DBF + dm.ctl + tablespace DBFs). Example:
//
//	DMDUL_PROFILE_DIR=D:\mocksnap DMDUL_PROFILE_TABLE=MOCK.T_CUSTOMER_MOCK \
//	  go test ./internal/dm -run TestManualProfileUnload -v -timeout 60m
//
// It writes the data SQL and a heap profile into the snapshot directory.

import (
	"os"
	"path/filepath"
	"runtime"
	"runtime/pprof"
	"strconv"
	"testing"
)

func TestManualProfileUnload(t *testing.T) {
	dir := os.Getenv("DMDUL_PROFILE_DIR")
	if dir == "" {
		t.Skip("set DMDUL_PROFILE_DIR to run the manual profiling harness")
	}
	table := os.Getenv("DMDUL_PROFILE_TABLE")
	if table == "" {
		table = "MOCK.T_CUSTOMER_MOCK"
	}
	maxRows := 0
	if raw := os.Getenv("DMDUL_PROFILE_MAXROWS"); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil {
			maxRows = parsed
		}
	}
	result, err := ExportData(DataExportOptions{
		SystemPath:  filepath.Join(dir, "SYSTEM.DBF"),
		ControlPath: filepath.Join(dir, "dm.ctl"),
		DataDir:     dir,
		OutputPath:  filepath.Join(dir, "profile_unload.sql"),
		TableFilter: table,
		OwnerFilter: "all",
		MaxRows:     maxRows,
	})
	if err != nil {
		t.Fatalf("ExportData failed: %v", err)
	}
	var stats runtime.MemStats
	runtime.ReadMemStats(&stats)
	t.Logf("rows exported=%d failed=%d planned=%d direct=%d fallbackPages=%d",
		result.RowsExported, result.RowsFailed, result.PlannedPages, result.DirectPagesRead, result.FallbackPagesScanned)
	t.Logf("HeapAlloc=%d MiB HeapSys=%d MiB TotalAlloc=%d MiB",
		stats.HeapAlloc>>20, stats.HeapSys>>20, stats.TotalAlloc>>20)
	heap, err := os.Create(filepath.Join(dir, "heap.pprof"))
	if err != nil {
		t.Fatalf("create heap profile: %v", err)
	}
	defer heap.Close()
	if err := pprof.WriteHeapProfile(heap); err != nil {
		t.Fatalf("write heap profile: %v", err)
	}
}
