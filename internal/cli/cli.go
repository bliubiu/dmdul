package cli

import (
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"dmdul/internal/dm"
	"dmdul/internal/storage"
	"dmdul/internal/version"
)

const usageText = `dmdul - offline Dameng database extraction helper

Usage:
  dmdul <command> [options]

Commands:
  inspect   Inspect a database file and print a small binary sample
  scan-system
            Scan SYSTEM.DBF bootstrap metadata and important SYS object rows
  inspect-ctl
            Inspect dm.ctl database name, control entries, and file paths
  export-ddl
            Export user table DDL from offline SYSTEM.DBF dictionary rows
  export-data
            Export ordinary table rows as INSERT SQL from offline datafiles
  scan-partitions
            Scan offline partition table metadata from SYSTEM.DBF
  version   Print build version
  help      Print this help

Examples:
  dmdul inspect -file SYSTEM.DBF
  dmdul inspect -file MAIN.DBF -sample 256
  dmdul scan-system -file oldpro\SYSTEM.DBF -ctl oldpro\dm.ctl
  dmdul inspect-ctl -ctl oldpro\dm.ctl
  dmdul export-ddl
  dmdul export-ddl -file oldpro\SYSTEM.DBF -ctl oldpro\dm.ctl -out oldpro\dm_offline_schema.sql
  dmdul export-ddl -file SYSTEM.DBF -ctl dm.ctl -out schema.sql -owner HR_TEST,SYSDBA -charset gb18030
  dmdul export-data -file oldpro\SYSTEM.DBF -ctl oldpro\dm.ctl -data-dir oldpro -out oldpro\dm_offline_data.sql
  dmdul export-data -file SYSTEM.DBF -ctl dm.ctl -table SYSDBA.BIN_TEST2,SYSDBA.BIN_TEST2_CHILD
  dmdul scan-partitions -file SYSTEM.DBF -ctl dm.ctl -owner all
`

const (
	defaultExportSystemPath  = "SYSTEM.DBF"
	defaultExportControlPath = "dm.ctl"
	defaultExportOutputPath  = "dm_offline_default_all.sql"
	defaultDataOutputPath    = "dm_offline_default_data.sql"
)

func Run(args []string, stdout io.Writer, stderr io.Writer) error {
	if len(args) == 0 {
		fmt.Fprint(stdout, usageText)
		return nil
	}

	switch args[0] {
	case "help", "-h", "--help":
		fmt.Fprint(stdout, usageText)
		return nil
	case "version":
		fmt.Fprintln(stdout, version.String())
		return nil
	case "inspect":
		return runInspect(args[1:], stdout, stderr)
	case "scan-system":
		return runScanSystem(args[1:], stdout, stderr)
	case "inspect-ctl":
		return runInspectCtl(args[1:], stdout, stderr)
	case "export-ddl":
		return runExportDDL(args[1:], stdout, stderr)
	case "export-data":
		return runExportData(args[1:], stdout, stderr)
	case "scan-partitions":
		return runScanPartitions(args[1:], stdout, stderr)
	default:
		return fmt.Errorf("unknown command %q\n\n%s", args[0], usageText)
	}
}

func runInspect(args []string, stdout io.Writer, stderr io.Writer) error {
	fs := flag.NewFlagSet("inspect", flag.ContinueOnError)
	fs.SetOutput(stderr)

	var filePath string
	var sampleSize int
	fs.StringVar(&filePath, "file", "", "database file path")
	fs.IntVar(&sampleSize, "sample", 128, "number of bytes to sample from file head")

	if err := fs.Parse(args); err != nil {
		return err
	}
	if filePath == "" {
		return fmt.Errorf("inspect requires -file")
	}

	result, err := storage.InspectFile(filePath, sampleSize)
	if err != nil {
		return err
	}

	fmt.Fprintf(stdout, "file: %s\n", result.Path)
	fmt.Fprintf(stdout, "size: %d bytes\n", result.Size)
	fmt.Fprintf(stdout, "sample: %d bytes\n\n", len(result.Sample))
	fmt.Fprint(stdout, result.HexDump())
	return nil
}

func runScanSystem(args []string, stdout io.Writer, stderr io.Writer) error {
	fs := flag.NewFlagSet("scan-system", flag.ContinueOnError)
	fs.SetOutput(stderr)

	var systemPath string
	var ctlPath string
	var ignoredIniPath string
	fs.StringVar(&systemPath, "file", "", "SYSTEM.DBF path")
	fs.StringVar(&ctlPath, "ctl", "", "optional dm.ctl path")
	fs.StringVar(&ignoredIniPath, "ini", "", "deprecated; ignored")

	if err := fs.Parse(args); err != nil {
		return err
	}
	if systemPath == "" {
		return fmt.Errorf("scan-system requires -file")
	}

	meta := dm.InspectDatabaseMetadata(systemPath, ctlPath, "", "auto")
	info, err := dm.ScanSystem(systemPath, dm.ImportantSystemObjectNames)
	if err != nil {
		return err
	}

	fmt.Fprintf(stdout, "system file: %s\n", info.Path)
	fmt.Fprintf(stdout, "size: %d bytes\n", info.Size)
	printDatabaseMetadata(stdout, meta)
	fmt.Fprintln(stdout)

	fmt.Fprintln(stdout, "important objects:")
	fmt.Fprintf(stdout, "%-22s %-8s %-10s %-8s %-10s %-10s %-10s %-8s %-10s\n",
		"name", "owner", "type", "subtype", "object_id", "row_abs", "name_abs", "page", "slot")
	for _, obj := range info.Objects {
		fmt.Fprintf(stdout, "%-22s %-8s %-10s %-8s %-10d 0x%-8X 0x%-8X %-8d %-10d\n",
			obj.Name,
			obj.Owner,
			obj.Type,
			obj.Subtype,
			obj.ObjectID,
			obj.RowOffset,
			obj.NameOffset,
			obj.PageNo,
			obj.SlotNo,
		)
	}

	if len(info.Missing) > 0 {
		fmt.Fprintln(stdout)
		fmt.Fprintln(stdout, "missing targets:")
		for _, name := range info.Missing {
			fmt.Fprintf(stdout, "  %s\n", name)
		}
	}

	if meta.ControlPath != "" {
		ctl, err := dm.InspectControlFile(meta.ControlPath)
		if err != nil {
			if ctlPath != "" {
				return err
			}
		} else {
			fmt.Fprintln(stdout)
			printControlInfo(stdout, ctl)
		}
	}

	return nil
}

func runInspectCtl(args []string, stdout io.Writer, stderr io.Writer) error {
	fs := flag.NewFlagSet("inspect-ctl", flag.ContinueOnError)
	fs.SetOutput(stderr)

	var ctlPath string
	var legacyFilePath string
	fs.StringVar(&ctlPath, "ctl", "", "dm.ctl path")
	fs.StringVar(&legacyFilePath, "file", "", "deprecated alias for -ctl")

	if err := fs.Parse(args); err != nil {
		return err
	}
	if ctlPath == "" {
		ctlPath = legacyFilePath
	}
	if ctlPath == "" {
		return fmt.Errorf("inspect-ctl requires -ctl")
	}

	ctl, err := dm.InspectControlFile(ctlPath)
	if err != nil {
		return err
	}
	printControlInfo(stdout, ctl)
	return nil
}

func runExportDDL(args []string, stdout io.Writer, stderr io.Writer) error {
	fs := flag.NewFlagSet("export-ddl", flag.ContinueOnError)
	fs.SetOutput(stderr)

	var systemPath string
	var ctlPath string
	var ignoredIniPath string
	var outPath string
	var ownerFilter string
	var charset string
	fs.StringVar(&systemPath, "file", defaultExportSystemPath, "SYSTEM.DBF path")
	fs.StringVar(&ctlPath, "ctl", "", "dm.ctl path for tablespace names; defaults to SYSTEM.DBF directory")
	fs.StringVar(&ignoredIniPath, "ini", "", "deprecated; ignored")
	fs.StringVar(&outPath, "out", defaultExportOutputPath, "output SQL script path")
	fs.StringVar(&ownerFilter, "owner", "all", "owner filter: all, SYSDBA, or comma-separated owners")
	fs.StringVar(&charset, "charset", "auto", "dictionary text charset: auto, utf-8, gb18030, gbk, euc-kr")

	if err := fs.Parse(args); err != nil {
		return err
	}
	systemPath = defaultIfBlank(systemPath, defaultExportSystemPath)
	if strings.TrimSpace(ctlPath) == "" {
		ctlPath = dm.DefaultControlPathForSystem(systemPath)
	}
	outPath = defaultIfBlank(outPath, defaultExportOutputPath)

	if err := validateOfflineInputFiles("export-ddl", systemPath, ctlPath); err != nil {
		return err
	}
	meta := dm.InspectDatabaseMetadata(systemPath, ctlPath, "", charset)

	result, err := dm.ExportDDL(dm.DDLExportOptions{
		SystemPath:  systemPath,
		ControlPath: ctlPath,
		OutputPath:  outPath,
		OwnerFilter: ownerFilter,
		Charset:     charset,
	})
	if err != nil {
		return err
	}

	fmt.Fprintf(stdout, "system file: %s\n", result.SystemPath)
	fmt.Fprintf(stdout, "output sql: %s\n", result.OutputPath)
	printDatabaseMetadata(stdout, meta)
	fmt.Fprintf(stdout, "objects scanned: %d\n", result.ObjectCount)
	fmt.Fprintf(stdout, "users exported: %d\n", result.UserCount)
	fmt.Fprintf(stdout, "roles exported: %d\n", result.RoleCount)
	fmt.Fprintf(stdout, "role grants exported: %d\n", result.RoleGrantCount)
	fmt.Fprintf(stdout, "tables exported: %d\n", result.TableCount)
	fmt.Fprintf(stdout, "columns exported: %d\n", result.ColumnCount)
	fmt.Fprintf(stdout, "indexes exported: %d\n", result.IndexCount)
	fmt.Fprintf(stdout, "constraints exported: %d\n", result.ConstraintCount)
	fmt.Fprintf(stdout, "partition ddl exported: tables=%d partitions=%d\n", result.PartitionedTables, result.PartitionCount)
	fmt.Fprintf(stdout, "comments exported: table=%d column=%d\n", result.TableCommentCount, result.ColumnCommentCount)
	return nil
}

func runExportData(args []string, stdout io.Writer, stderr io.Writer) error {
	fs := flag.NewFlagSet("export-data", flag.ContinueOnError)
	fs.SetOutput(stderr)

	var systemPath string
	var ctlPath string
	var ignoredIniPath string
	var dataDir string
	var outPath string
	var ownerFilter string
	var tableFilter string
	var excludeTables string
	var charset string
	var maxRows int
	var failedComments bool
	fs.StringVar(&systemPath, "file", defaultExportSystemPath, "SYSTEM.DBF path")
	fs.StringVar(&ctlPath, "ctl", "", "dm.ctl path; defaults to SYSTEM.DBF directory")
	fs.StringVar(&ignoredIniPath, "ini", "", "deprecated; ignored")
	fs.StringVar(&dataDir, "data-dir", "", "directory containing MAIN.DBF and other data DBF files; defaults to SYSTEM.DBF directory")
	fs.StringVar(&outPath, "out", defaultDataOutputPath, "output INSERT SQL script path")
	fs.StringVar(&ownerFilter, "owner", "all", "owner filter: all, SYSDBA, or comma-separated owners")
	fs.StringVar(&tableFilter, "table", "all", "table filter: all or comma-separated OWNER.TABLE_NAME values")
	fs.StringVar(&tableFilter, "tables", "all", "alias for -table")
	fs.StringVar(&excludeTables, "exclude", "", "comma-separated OWNER.TABLE_NAME values to skip")
	fs.StringVar(&charset, "charset", "auto", "dictionary and row text charset: auto, utf-8, gb18030, gbk, euc-kr")
	fs.IntVar(&maxRows, "max-rows", 0, "maximum rows to process; 0 means unlimited")
	fs.BoolVar(&failedComments, "failed-comments", false, "write failed row diagnostics as SQL comments")

	if err := fs.Parse(args); err != nil {
		return err
	}
	systemPath = defaultIfBlank(systemPath, defaultExportSystemPath)
	if strings.TrimSpace(ctlPath) == "" {
		ctlPath = dm.DefaultControlPathForSystem(systemPath)
	}
	outPath = defaultIfBlank(outPath, defaultDataOutputPath)

	if err := validateOfflineInputFiles("export-data", systemPath, ctlPath); err != nil {
		return err
	}
	meta := dm.InspectDatabaseMetadata(systemPath, ctlPath, "", charset)

	result, err := dm.ExportData(dm.DataExportOptions{
		SystemPath:          systemPath,
		ControlPath:         ctlPath,
		DataDir:             dataDir,
		OutputPath:          outPath,
		OwnerFilter:         ownerFilter,
		TableFilter:         tableFilter,
		ExcludeTables:       excludeTables,
		Charset:             charset,
		MaxRows:             maxRows,
		WriteFailedComments: failedComments,
	})
	if err != nil {
		return err
	}

	fmt.Fprintf(stdout, "system file: %s\n", result.SystemPath)
	fmt.Fprintf(stdout, "data dir: %s\n", result.DataDir)
	fmt.Fprintf(stdout, "output sql: %s\n", result.OutputPath)
	printDatabaseMetadata(stdout, meta)
	fmt.Fprintf(stdout, "objects scanned: %d\n", result.ObjectCount)
	fmt.Fprintf(stdout, "tables selected: %d\n", result.TableCount)
	fmt.Fprintf(stdout, "columns selected: %d\n", result.ColumnCount)
	fmt.Fprintf(stdout, "assist indexes selected: %d\n", result.AssistIndexCount)
	fmt.Fprintf(stdout, "data files scanned: %d\n", result.DataFileCount)
	fmt.Fprintf(stdout, "pages scanned: %d\n", result.PagesScanned)
	fmt.Fprintf(stdout, "rows located: %d\n", result.RowsLocated)
	fmt.Fprintf(stdout, "rows exported: %d\n", result.RowsExported)
	fmt.Fprintf(stdout, "rows failed: %d\n", result.RowsFailed)
	fmt.Fprintf(stdout, "tables with rows: %d\n", result.TablesWithRows)
	fmt.Fprintf(stdout, "tables without rows: %d\n", result.TablesWithoutRows)
	if len(result.TableRowCounts) > 0 && len(result.TableRowCounts) <= 20 {
		fmt.Fprintln(stdout)
		fmt.Fprintln(stdout, "table row summary:")
		fmt.Fprintf(stdout, "%-18s %-34s %-10s %-10s %-10s\n", "owner", "table", "located", "exported", "failed")
		for _, item := range result.TableRowCounts {
			fmt.Fprintf(stdout, "%-18s %-34s %-10d %-10d %-10d\n",
				item.Owner,
				truncateForTable(item.Name, 34),
				item.RowsLocated,
				item.RowsExported,
				item.RowsFailed,
			)
		}
	}
	return nil
}

func runScanPartitions(args []string, stdout io.Writer, stderr io.Writer) error {
	fs := flag.NewFlagSet("scan-partitions", flag.ContinueOnError)
	fs.SetOutput(stderr)

	var systemPath string
	var ctlPath string
	var ignoredIniPath string
	var ownerFilter string
	var charset string
	fs.StringVar(&systemPath, "file", defaultExportSystemPath, "SYSTEM.DBF path")
	fs.StringVar(&ctlPath, "ctl", "", "dm.ctl path; defaults to SYSTEM.DBF directory")
	fs.StringVar(&ignoredIniPath, "ini", "", "deprecated; ignored")
	fs.StringVar(&ownerFilter, "owner", "all", "owner filter: all, SYSDBA, or comma-separated owners")
	fs.StringVar(&charset, "charset", "auto", "dictionary text charset: auto, utf-8, gb18030, gbk, euc-kr")

	if err := fs.Parse(args); err != nil {
		return err
	}
	systemPath = defaultIfBlank(systemPath, defaultExportSystemPath)
	if strings.TrimSpace(ctlPath) == "" {
		ctlPath = dm.DefaultControlPathForSystem(systemPath)
	}
	if err := validateOfflineInputFiles("scan-partitions", systemPath, ctlPath); err != nil {
		return err
	}
	meta := dm.InspectDatabaseMetadata(systemPath, ctlPath, "", charset)

	result, err := dm.ScanPartitions(dm.PartitionScanOptions{
		SystemPath:  systemPath,
		ControlPath: ctlPath,
		OwnerFilter: ownerFilter,
		Charset:     charset,
	})
	if err != nil {
		return err
	}

	fmt.Fprintf(stdout, "system file: %s\n", result.SystemPath)
	printDatabaseMetadata(stdout, meta)
	fmt.Fprintf(stdout, "objects scanned: %d\n", result.ObjectCount)
	fmt.Fprintf(stdout, "partitioned tables: %d\n", result.TableCount)
	fmt.Fprintf(stdout, "partitions scanned: %d\n", result.PartitionCount)
	if len(result.Tables) == 0 {
		return nil
	}

	fmt.Fprintln(stdout)
	fmt.Fprintln(stdout, "partitioned table summary:")
	fmt.Fprintf(stdout, "%-18s %-34s %-10s %-12s %-10s\n", "owner", "table", "table_id", "partitioned", "parts")
	for _, table := range result.Tables {
		fmt.Fprintf(stdout, "%-18s %-34s %-10d %-12s %-10d\n",
			table.Owner, table.Name, table.TableID, "YES", len(table.Partitions))
	}

	fmt.Fprintln(stdout)
	fmt.Fprintln(stdout, "partition details:")
	fmt.Fprintf(stdout, "%-18s %-34s %-8s %-20s %-12s %-20s %-10s %-8s %-8s %-10s\n",
		"owner", "table", "type", "partition", "part_table", "high_value", "row_abs", "page", "slot", "offset")
	for _, table := range result.Tables {
		for _, part := range table.Partitions {
			highValue := part.HighValuePreview
			if highValue == "" {
				highValue = part.HighValueHex
			}
			fmt.Fprintf(stdout, "%-18s %-34s %-8s %-20s %-12d %-20s 0x%-8X %-8d %-8d 0x%-8X\n",
				table.Owner,
				table.Name,
				part.Type,
				part.Name,
				part.PartTableID,
				truncateForTable(highValue, 20),
				part.RowOffset,
				part.PageNo,
				part.SlotNo,
				part.SlotOffset,
			)
		}
	}
	return nil
}

func defaultIfBlank(value string, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}

func validateExportInputFiles(systemPath string, ctlPath string) error {
	return validateOfflineInputFiles("export-ddl", systemPath, ctlPath)
}

func validateOfflineInputFiles(command string, systemPath string, ctlPath string) error {
	missing := make([]string, 0, 2)
	if err := validateRegularFile(systemPath); err != nil {
		if os.IsNotExist(err) {
			missing = append(missing, fmt.Sprintf("-file %s", systemPath))
		} else {
			return fmt.Errorf("%s cannot access -file %q: %w", command, systemPath, err)
		}
	}
	if err := validateRegularFile(ctlPath); err != nil {
		if os.IsNotExist(err) {
			missing = append(missing, fmt.Sprintf("-ctl %s", ctlPath))
		} else {
			return fmt.Errorf("%s cannot access -ctl %q: %w", command, ctlPath, err)
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("%s requires -file and -ctl; default input files not found: %s", command, strings.Join(missing, ", "))
	}
	return nil
}

func validateRegularFile(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	if info.IsDir() {
		return fmt.Errorf("is a directory")
	}
	return nil
}

func printDatabaseMetadata(stdout io.Writer, meta dm.DatabaseMetadata) {
	fmt.Fprintln(stdout, "database info:")
	fmt.Fprintf(stdout, "  database name: %s\n", valueWithSource(meta.DatabaseName, meta.DatabaseNameSrc))
	fmt.Fprintf(stdout, "  extent size: %s\n", extentInfo(meta))
	fmt.Fprintf(stdout, "  page size: %s\n", pageSizeInfo(meta))
	fmt.Fprintf(stdout, "  page count: %s\n", valueWithSource(formatUint32(meta.PageCount), meta.PageCountSource))
	fmt.Fprintf(stdout, "  charset: %s\n", charsetInfo(meta))
}

func extentInfo(meta dm.DatabaseMetadata) string {
	return valueWithSource(formatUint32(meta.ExtentSize)+" pages", meta.ExtentSizeSource)
}

func pageSizeInfo(meta dm.DatabaseMetadata) string {
	return valueWithSource(formatUint32(meta.PageSize)+" bytes", meta.PageSizeSource)
}

func charsetInfo(meta dm.DatabaseMetadata) string {
	return valueWithSource(defaultIfBlank(meta.Charset, "unknown"), meta.CharsetSource)
}

func iniComparisonText(value string, comparison string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "not set"
	}
	comparison = strings.TrimSpace(comparison)
	if comparison == "" {
		return value
	}
	return fmt.Sprintf("%s (%s)", value, comparison)
}

func valueWithSource(value string, source string) string {
	value = strings.TrimSpace(value)
	if value == "" || value == "0" {
		value = "unknown"
	}
	source = strings.TrimSpace(source)
	if source == "" {
		return value
	}
	return fmt.Sprintf("%s (%s)", value, source)
}

func formatUint32(value uint32) string {
	if value == 0 {
		return "unknown"
	}
	return fmt.Sprintf("%d", value)
}

func truncateForTable(value string, width int) string {
	if width <= 0 {
		return value
	}
	runes := []rune(value)
	if len(runes) <= width {
		return value
	}
	if width <= 3 {
		return string(runes[:width])
	}
	return string(runes[:width-3]) + "..."
}

func printControlInfo(stdout io.Writer, ctl *dm.ControlInfo) {
	fmt.Fprintf(stdout, "control file: %s\n", ctl.Path)
	fmt.Fprintf(stdout, "size: %d bytes\n", ctl.Size)
	fmt.Fprintf(stdout, "control block size: %d bytes\n", ctl.BlockSize)
	fmt.Fprintf(stdout, "database name: %s\n", ctl.DatabaseName)
	fmt.Fprintln(stdout)
	fmt.Fprintln(stdout, "control entries:")
	fmt.Fprintf(stdout, "%-6s %-18s %-10s %s\n", "id", "name", "name_abs", "paths")
	for _, entry := range ctl.Entries {
		fmt.Fprintf(stdout, "%-6d %-18s 0x%-8X %s\n",
			entry.ID,
			entry.Name,
			entry.NameOffset,
			entry.PathSummary(),
		)
	}
}
