package cli

import (
	"bufio"
	"encoding/binary"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime/debug"
	"strings"
	"time"
	"unicode"

	"dmdul/internal/dm"
	"dmdul/internal/version"
)

const replPrompt = "DMDUL> "

type interactiveSession struct {
	systemPath      string
	controlPath     string
	controlProvided bool
	dataDir         string
	dataDirSet      bool
	charset         string
	charsetExplicit bool
	dataFormat      string
	outputDir       string
	outputDirSet    bool
	logPath         string
	logPathSet      bool
	logOpenPath     string
	initSource      string
	dictionary      *dm.DictionaryInfo
	logFile         *os.File
	stderr          io.Writer
}

func RunInteractive(input io.Reader, stdout io.Writer, stderr io.Writer) error {
	session := newInteractiveSession()
	session.stderr = stderr
	session.loadConfigFile(stderr)
	if err := session.writeInitDUL(); err != nil {
		fmt.Fprintf(stderr, "warning: write init.dul: %v\n", err)
	}
	defer session.closeLog()

	fmt.Fprintf(stdout, "dmdul: Release %s - Dameng Database Offline Recovery & Data Unloader\n", version.Version)
	fmt.Fprintln(stdout, "Copyright (c) 2026 greatfinish. All rights reserved.")
	fmt.Fprintln(stdout, "https://github.com/greatfinish/dmdul")
	fmt.Fprintln(stdout, "Type help; for available commands.")

	scanner := bufio.NewScanner(input)
	scanner.Buffer(make([]byte, 0, 4096), 1024*1024)
	for {
		fmt.Fprint(stdout, replPrompt)
		if !scanner.Scan() {
			fmt.Fprintln(stdout)
			break
		}
		line := strings.TrimSpace(scanner.Text())
		for _, command := range splitInteractiveCommands(line) {
			exit, err := session.execute(command, stdout)
			if err != nil {
				fmt.Fprintf(stdout, "error: %v\n", err)
				session.log(replPrompt + command)
				session.log("ERROR " + err.Error())
				continue
			}
			session.log(replPrompt + command)
			if err := session.writeInitDUL(); err != nil {
				fmt.Fprintf(stderr, "warning: write init.dul: %v\n", err)
			}
			if exit {
				return nil
			}
		}
	}
	return scanner.Err()
}

func newInteractiveSession() *interactiveSession {
	return &interactiveSession{
		systemPath: defaultSystemPath,
		charset:    "auto",
		dataFormat: "sql",
	}
}

func (s *interactiveSession) execute(command string, stdout io.Writer) (bool, error) {
	command = strings.TrimSpace(strings.TrimSuffix(command, ";"))
	if command == "" {
		return false, nil
	}
	fields := splitCommandFields(command)
	if len(fields) == 0 {
		return false, nil
	}

	switch strings.ToLower(fields[0]) {
	case "help", "?":
		printInteractiveHelp(stdout)
	case "exit", "quit":
		fmt.Fprintln(stdout, "bye")
		return true, nil
	case "set":
		return false, s.executeSet(fields[1:], stdout)
	case "show":
		return false, s.executeShow(fields[1:], stdout)
	case "bootstrap":
		return false, s.bootstrap(stdout)
	case "load":
		return false, s.executeLoad(fields[1:], stdout)
	case "list":
		return false, s.executeList(fields[1:], stdout)
	case "unload":
		return false, s.executeUnload(fields[1:], stdout)
	case "recover":
		return false, s.executeRecover(fields[1:], stdout)
	default:
		return false, fmt.Errorf("unknown command %q", fields[0])
	}
	return false, nil
}

func printInteractiveHelp(stdout io.Writer) {
	fmt.Fprintln(stdout, "Commands:")
	fmt.Fprintln(stdout, "  bootstrap;")
	fmt.Fprintln(stdout, "      Load dictionary metadata from SYSTEM.DBF and write text dictionary files.")
	fmt.Fprintln(stdout, "  load dictionary;")
	fmt.Fprintln(stdout, "      Load dictionary metadata from dmdul_dict text files.")
	fmt.Fprintln(stdout, "  load init;")
	fmt.Fprintln(stdout, "      Reload parameters from the effective init.dul file.")
	fmt.Fprintln(stdout, "  list user;")
	fmt.Fprintln(stdout, "      List recovered users/owners and dictionary cache status.")
	fmt.Fprintln(stdout, "  list table <owner>;")
	fmt.Fprintln(stdout, "      List tables owned by one user.")
	fmt.Fprintln(stdout, "  unload table <owner.table_name>;")
	fmt.Fprintln(stdout, "      Export one table to <owner>_<table>_ddl.sql and <owner>_<table>_data.{sql|csv}.")
	fmt.Fprintln(stdout, "  unload user <owner>;")
	fmt.Fprintln(stdout, "      Export all tables for one owner to DDL plus SQL or per-table CSV data files.")
	fmt.Fprintln(stdout, "  unload database;")
	fmt.Fprintln(stdout, "      Export all recovered users and tables to DDL plus SQL or per-table CSV data files.")
	fmt.Fprintln(stdout, "  recover table <owner.table_name>;")
	fmt.Fprintln(stdout, "      Scan residual pages by storage/assist id for TRUNCATE/DROP table recovery.")
	fmt.Fprintln(stdout, "  set system <SYSTEM.DBF path>;")
	fmt.Fprintln(stdout, "  set data_dir <DBF directory>;")
	fmt.Fprintln(stdout, "  set control <dm.ctl path>;")
	fmt.Fprintln(stdout, "  set output_dir <directory>;")
	fmt.Fprintln(stdout, "      Output SQL directory. Defaults to data_dir when set, otherwise current directory.")
	fmt.Fprintln(stdout, "  set data_format sql|csv;")
	fmt.Fprintln(stdout, "  set charset auto|utf-8|gb18030|gbk|euc-kr;")
	fmt.Fprintln(stdout, "  show parameter;")
	fmt.Fprintln(stdout, "  exit;")
}

func (s *interactiveSession) executeRecover(args []string, stdout io.Writer) error {
	if len(args) < 1 {
		return fmt.Errorf("usage: recover table <owner.table_name>")
	}
	if err := s.ensureDictionaryLoaded(); err != nil {
		return err
	}
	switch strings.ToLower(args[0]) {
	case "table":
		if len(args) < 2 {
			return fmt.Errorf("usage: recover table <owner.table_name>")
		}
		return s.recoverTable(args[1:], stdout)
	default:
		return fmt.Errorf("usage: recover table <owner.table_name>")
	}
}

func (s *interactiveSession) executeSet(args []string, stdout io.Writer) error {
	if len(args) < 2 {
		return fmt.Errorf("usage: set <parameter> <value>")
	}
	name := normalizeParameterName(args[0])
	value := strings.Join(args[1:], " ")
	switch name {
	case "system", "file":
		s.systemPath = value
		s.dictionary = nil
		s.charset = "auto"
		s.charsetExplicit = false
	case "data_dir", "datadir":
		s.dataDir = value
		s.dataDirSet = strings.TrimSpace(value) != ""
	case "control", "ctl":
		s.controlPath = value
		s.controlProvided = strings.TrimSpace(value) != ""
		s.dictionary = nil
	case "output_dir", "outdir":
		s.outputDir = value
		s.outputDirSet = true
	case "data_format", "format":
		value = strings.ToLower(strings.TrimSpace(value))
		if value != "sql" && value != "csv" {
			return fmt.Errorf("data_format must be sql or csv")
		}
		s.dataFormat = value
	case "charset":
		s.charset = value
		s.charsetExplicit = true
		s.dictionary = nil
	case "log":
		s.logPath = value
		s.logPathSet = strings.TrimSpace(value) != ""
	default:
		return fmt.Errorf("unknown parameter %q", args[0])
	}
	fmt.Fprintf(stdout, "%s = %s\n", name, value)
	return nil
}

func (s *interactiveSession) executeShow(args []string, stdout io.Writer) error {
	if len(args) == 0 || !strings.EqualFold(args[0], "parameter") {
		return fmt.Errorf("usage: show parameter")
	}
	s.printParameters(stdout)
	return nil
}

func (s *interactiveSession) executeList(args []string, stdout io.Writer) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: list user | list table <owner>")
	}
	if err := s.ensureDictionaryLoaded(); err != nil {
		return err
	}
	switch strings.ToLower(args[0]) {
	case "user", "users":
		s.printUsers(stdout)
	case "table", "tables":
		if len(args) < 2 {
			return fmt.Errorf("usage: list table <owner>")
		}
		s.printTables(stdout, args[1])
	default:
		return fmt.Errorf("usage: list user | list table <owner>")
	}
	return nil
}

func (s *interactiveSession) executeLoad(args []string, stdout io.Writer) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: load dictionary | load init")
	}
	switch strings.ToLower(args[0]) {
	case "dictionary":
		return s.loadDictionaryFiles(stdout)
	case "init":
		return s.loadInitDULCommand(stdout)
	default:
		return fmt.Errorf("usage: load dictionary | load init")
	}
}

func (s *interactiveSession) executeUnload(args []string, stdout io.Writer) error {
	if len(args) < 1 {
		return fmt.Errorf("usage: unload table <owner.table_name> | unload user <owner> | unload database")
	}
	if err := s.ensureDictionaryLoaded(); err != nil {
		return err
	}
	switch strings.ToLower(args[0]) {
	case "table":
		if len(args) < 2 {
			return fmt.Errorf("usage: unload table <owner.table_name>")
		}
		return s.unloadTable(args[1:], stdout)
	case "user":
		if len(args) < 2 {
			return fmt.Errorf("usage: unload user <owner>")
		}
		return s.unloadUser(args[1:], stdout)
	case "database", "db":
		return s.unloadDatabase(args[1:], stdout)
	default:
		return fmt.Errorf("usage: unload table <owner.table_name> | unload user <owner> | unload database")
	}
}

func (s *interactiveSession) bootstrap(stdout io.Writer) error {
	startedAt := time.Now()
	systemPath := defaultIfBlank(s.systemPath, defaultSystemPath)
	ctlPath, ctlProvided := optionalControlPathForSystem(systemPath, s.controlPath, s.controlProvided)
	if err := validateOptionalControlInputFiles("bootstrap", systemPath, ctlPath, ctlProvided); err != nil {
		return err
	}
	dataDir := s.effectiveDataDir()
	controlDULPath := s.effectiveControlDULPath()
	s.emitBootstrapLine(stdout, fmt.Sprintf("[BOOTSTRAP] phase=start status=RUNNING system=%q data_dir=%q", systemPath, dataDir))
	dataFiles, err := dm.ScanOfflineDataFiles(ctlPath, "", dataDir)
	if err != nil {
		return err
	}
	if err := dm.WriteControlDUL(controlDULPath, dataFiles); err != nil {
		return fmt.Errorf("write control.dul: %w", err)
	}
	bootstrapCharset := s.bootstrapCharset(systemPath, ctlPath)
	metadata := dm.InspectDatabaseMetadata(systemPath, ctlPath, "", bootstrapCharset)
	s.emitBootstrapLine(stdout, fmt.Sprintf("[BOOTSTRAP] phase=metadata status=OK page_size=%d extent_size=%d page_count=%d charset=%q",
		metadata.PageSize, metadata.ExtentSize, metadata.PageCount, metadata.Charset))
	fileWarnings := false
	for _, file := range dataFiles {
		line, warning := formatBootstrapFileDiagnostic(file, metadata.PageSize)
		fileWarnings = fileWarnings || warning
		s.emitBootstrapLine(stdout, line)
	}
	dict, err := dm.LoadDictionary(dm.DictionaryOptions{
		SystemPath:     systemPath,
		ControlPath:    ctlPath,
		ControlDULPath: controlDULPath,
		OwnerFilter:    "all",
		Charset:        bootstrapCharset,
		Diagnostic: func(diag dm.BootstrapDiagnostic) {
			s.emitBootstrapLine(stdout, formatBootstrapDiagnostic(diag))
		},
	})
	if err != nil {
		return err
	}
	dictDir := s.effectiveDictionaryDir()
	dict.Source = "SYSTEM.DBF"
	dict.DictionaryDir = dictDir
	dictFiles, err := dm.WriteDictionaryFiles(dictDir, dict)
	if err != nil {
		return fmt.Errorf("write dictionary files: %w", err)
	}
	s.systemPath = systemPath
	s.controlPath = ctlPath
	s.controlProvided = ctlProvided
	s.dictionary = dict
	if detectedCharset, ok := charsetParameterFromDictionary(dict.Charset); ok {
		s.charset = detectedCharset
		s.charsetExplicit = false
	}
	debug.FreeOSMemory()
	status := "SUCCESS"
	if dict.BootstrapFallback || fileWarnings {
		status = "SUCCESS_WITH_WARNINGS"
	}
	s.emitBootstrapLine(stdout, fmt.Sprintf("[BOOTSTRAP] phase=output name=control.dul status=OK files=%d path=%q", len(dataFiles), controlDULPath))
	s.emitBootstrapLine(stdout, fmt.Sprintf("[BOOTSTRAP] phase=output name=dmdul_dict status=OK users=%d tables=%d columns=%d views=%d sequences=%d routines=%d triggers=%d synonyms=%d tab_privs=%d path=%q",
		dictFiles.UserCount, dictFiles.TableCount, dictFiles.ColumnCount, dictFiles.ViewCount, dictFiles.SequenceCount,
		dictFiles.RoutineCount, dictFiles.TriggerCount, dictFiles.SynonymCount, dictFiles.TabPrivilegeCount, dictFiles.Dir))
	s.emitBootstrapLine(stdout, fmt.Sprintf("[BOOTSTRAP] phase=complete status=%s mode=%s objects=%d elapsed_ms=%d",
		status, dict.BootstrapMode, dict.ObjectCount, time.Since(startedAt).Milliseconds()))

	fmt.Fprintln(stdout, "bootstrap completed")
	fmt.Fprintf(stdout, "system file: %s\n", dict.SystemPath)
	if dict.ControlPath != "" {
		fmt.Fprintf(stdout, "control file: %s\n", dict.ControlPath)
	}
	fmt.Fprintf(stdout, "control.dul: %s (data files: %d)\n", controlDULPath, len(dataFiles))
	fmt.Fprintf(stdout, "dictionary dir: %s (users=%d tables=%d columns=%d views=%d sequences=%d routines=%d triggers=%d synonyms=%d tab_privs=%d)\n",
		dictFiles.Dir, dictFiles.UserCount, dictFiles.TableCount, dictFiles.ColumnCount, dictFiles.ViewCount, dictFiles.SequenceCount, dictFiles.RoutineCount, dictFiles.TriggerCount, dictFiles.SynonymCount, dictFiles.TabPrivilegeCount)
	fmt.Fprintf(stdout, "page size: %d bytes\n", dict.PageSize)
	fmt.Fprintf(stdout, "extent size: %d pages (%s)\n", dict.ExtentSize, dict.ExtentSizeSource)
	fmt.Fprintf(stdout, "page count: %d\n", dict.PageCount)
	fmt.Fprintf(stdout, "charset: %s (%s)\n", dict.Charset, dict.CharsetSource)
	fmt.Fprintf(stdout, "charset parameter: %s\n", s.charset)
	fmt.Fprintf(stdout, "objects loaded: %d\n", dict.ObjectCount)
	fmt.Fprintf(stdout, "users loaded: %d\n", dict.UserCount)
	fmt.Fprintf(stdout, "tables loaded: %d\n", dict.TableCount)
	fmt.Fprintf(stdout, "columns loaded: %d\n", dict.ColumnCount)
	fmt.Fprintf(stdout, "views loaded: %d\n", dict.ViewCount)
	fmt.Fprintf(stdout, "sequences loaded: %d\n", dict.SequenceCount)
	fmt.Fprintf(stdout, "routines loaded: %d\n", dict.RoutineCount)
	fmt.Fprintf(stdout, "triggers loaded: %d\n", dict.TriggerCount)
	fmt.Fprintf(stdout, "synonyms loaded: %d\n", dict.SynonymCount)
	fmt.Fprintf(stdout, "tab privileges loaded: %d\n", dict.TabPrivilegeCount)
	return nil
}

func (s *interactiveSession) emitBootstrapLine(stdout io.Writer, line string) {
	fmt.Fprintln(stdout, line)
	s.log(line)
}

func formatBootstrapDiagnostic(diag dm.BootstrapDiagnostic) string {
	var out strings.Builder
	out.WriteString("[BOOTSTRAP]")
	if diag.Stage > 0 {
		fmt.Fprintf(&out, " stage=%d", diag.Stage)
	}
	if diag.Phase != "" {
		fmt.Fprintf(&out, " phase=%s", diag.Phase)
	}
	if diag.Name != "" {
		fmt.Fprintf(&out, " name=%s", diag.Name)
	}
	if diag.Mode != "" {
		fmt.Fprintf(&out, " mode=%s", diag.Mode)
	}
	if diag.Status != "" {
		fmt.Fprintf(&out, " status=%s", diag.Status)
	}
	if diag.RootFile >= 0 {
		fmt.Fprintf(&out, " root=%d/%d", diag.RootFile, diag.RootPage)
	}
	if diag.StorageID != 0 {
		fmt.Fprintf(&out, " storage=%d", diag.StorageID)
	}
	if diag.Pages > 0 {
		fmt.Fprintf(&out, " pages=%d", diag.Pages)
	}
	if diag.Rows > 0 || diag.Phase == "anchor" || diag.Phase == "extract" || diag.Phase == "source" || diag.Phase == "validate" {
		fmt.Fprintf(&out, " rows=%d", diag.Rows)
	}
	if diag.Reason != "" {
		fmt.Fprintf(&out, " reason=%q", diag.Reason)
	}
	return out.String()
}

func formatBootstrapFileDiagnostic(file dm.OfflineDataFile, pageSize uint32) (string, bool) {
	status := "OK"
	var size int64
	if info, err := os.Stat(file.Path); err == nil && !info.IsDir() {
		size = info.Size()
	} else {
		status = "MISSING"
	}
	pages := int64(0)
	aligned := false
	if pageSize > 0 && size >= 0 {
		pages = size / int64(pageSize)
		aligned = size%int64(pageSize) == 0
	}
	if status == "OK" && !aligned {
		status = "UNALIGNED"
	}
	headerGroup, headerFile, headerPage := uint16(0), uint16(0), uint32(0)
	if input, err := os.Open(file.Path); err == nil {
		var header [8]byte
		if _, err := io.ReadFull(input, header[:]); err == nil {
			headerGroup = binary.LittleEndian.Uint16(header[0:])
			headerFile = binary.LittleEndian.Uint16(header[2:])
			headerPage = binary.LittleEndian.Uint32(header[4:])
			if status == "OK" && (uint32(headerGroup) != file.GroupID || int16(headerFile) != file.FileID || headerPage != 0) {
				if file.GroupID == 3 && headerGroup == 0 && headerFile == 0 && headerPage == 0 {
					status = "IGNORED_TEMP"
				} else {
					status = "HEADER_MISMATCH"
				}
			}
		}
		_ = input.Close()
	}
	line := fmt.Sprintf("[BOOTSTRAP] phase=file status=%s group=%d file=%d header_group=%d header_file=%d header_page=%d tablespace=%q bytes=%d pages=%d aligned=%t path=%q",
		status, file.GroupID, file.FileID, headerGroup, headerFile, headerPage, file.Tablespace, size, pages, aligned, file.Path)
	return line, status != "OK" && status != "IGNORED_TEMP"
}

func (s *interactiveSession) printParameters(stdout io.Writer) {
	fmt.Fprintf(stdout, "system     = %s\n", s.systemPath)
	fmt.Fprintf(stdout, "control    = %s\n", defaultIfBlank(s.controlPath, "(auto)"))
	fmt.Fprintf(stdout, "control_dul= %s\n", s.effectiveControlDULPath())
	fmt.Fprintf(stdout, "init_dul   = %s\n", s.effectiveInitDULPath())
	fmt.Fprintf(stdout, "init_load  = %s\n", defaultIfBlank(s.initSource, "(not loaded)"))
	fmt.Fprintf(stdout, "dict_dir   = %s\n", s.effectiveDictionaryDir())
	fmt.Fprintf(stdout, "data_dir   = %s\n", defaultIfBlank(s.dataDir, "(SYSTEM.DBF directory)"))
	fmt.Fprintf(stdout, "output_dir = %s\n", s.effectiveOutputDir())
	fmt.Fprintf(stdout, "data_format= %s\n", s.dataFormat)
	fmt.Fprintf(stdout, "charset    = %s\n", s.charset)
	fmt.Fprintf(stdout, "log        = %s\n", s.effectiveLogPath())
	if s.dictionary != nil {
		fmt.Fprintf(stdout, "dictionary = loaded (%s)\n", defaultIfBlank(s.dictionary.Source, "memory"))
	} else {
		fmt.Fprintln(stdout, "dictionary = not loaded")
	}
}

func (s *interactiveSession) printUsers(stdout io.Writer) {
	s.printDictionarySummary(stdout)
	counts := dictionaryUserObjectCounts(s.dictionary)
	fmt.Fprintf(stdout, "%-22s %8s %8s %10s %10s %9s %10s %11s %9s\n",
		"user", "tables", "views", "synonyms", "sequences", "triggers", "functions", "procedures", "packages")
	for _, user := range s.dictionary.Users {
		count := counts[strings.ToUpper(user.Name)]
		fmt.Fprintf(stdout, "%-22s %8d %8d %10d %10d %9d %10d %11d %9d\n",
			user.Name,
			count.Tables,
			count.Views,
			count.Synonyms,
			count.Sequences,
			count.Triggers,
			count.Functions,
			count.Procedures,
			count.Packages,
		)
	}
}

type userObjectCounts struct {
	Tables     int
	Views      int
	Synonyms   int
	Sequences  int
	Triggers   int
	Functions  int
	Procedures int
	Packages   int
}

func dictionaryUserObjectCounts(dict *dm.DictionaryInfo) map[string]userObjectCounts {
	result := make(map[string]userObjectCounts)
	increment := func(owner string, apply func(*userObjectCounts)) {
		key := strings.ToUpper(strings.TrimSpace(owner))
		if key == "" {
			return
		}
		count := result[key]
		apply(&count)
		result[key] = count
	}
	for _, table := range dict.Tables {
		increment(table.Owner, func(count *userObjectCounts) { count.Tables++ })
	}
	for _, view := range dict.Views {
		increment(view.Owner, func(count *userObjectCounts) { count.Views++ })
	}
	for _, syn := range dict.Synonyms {
		increment(syn.Owner, func(count *userObjectCounts) { count.Synonyms++ })
	}
	for _, seq := range dict.Sequences {
		increment(seq.Owner, func(count *userObjectCounts) { count.Sequences++ })
	}
	for _, trigger := range dict.Triggers {
		increment(trigger.Owner, func(count *userObjectCounts) { count.Triggers++ })
	}
	packageNamesByOwner := make(map[string]map[string]bool)
	for _, routine := range dict.Routines {
		owner := strings.ToUpper(strings.TrimSpace(routine.Owner))
		if owner == "" {
			continue
		}
		switch strings.Join(strings.Fields(strings.ToUpper(strings.TrimSpace(routine.ObjectType))), " ") {
		case "FUNCTION":
			increment(owner, func(count *userObjectCounts) { count.Functions++ })
		case "PROCEDURE":
			increment(owner, func(count *userObjectCounts) { count.Procedures++ })
		case "PACKAGE", "PACKAGE BODY":
			names := packageNamesByOwner[owner]
			if names == nil {
				names = make(map[string]bool)
				packageNamesByOwner[owner] = names
			}
			names[strings.ToUpper(strings.TrimSpace(routine.Name))] = true
		}
	}
	for owner, names := range packageNamesByOwner {
		count := result[owner]
		count.Packages = len(names)
		result[owner] = count
	}
	return result
}

func (s *interactiveSession) printDictionarySummary(stdout io.Writer) {
	if s.dictionary == nil {
		return
	}
	source := defaultIfBlank(s.dictionary.Source, "memory")
	dir := defaultIfBlank(s.dictionary.DictionaryDir, s.effectiveDictionaryDir())
	fmt.Fprintf(stdout, "dictionary source: %s\n", source)
	fmt.Fprintf(stdout, "dictionary dir: %s\n", dir)
	if s.dictionary.BootstrapMode != "" {
		fmt.Fprintf(stdout, "bootstrap mode: %s (fallback=%t)\n", s.dictionary.BootstrapMode, s.dictionary.BootstrapFallback)
	}
	fmt.Fprintf(stdout, "dictionary rows: users=%d tables=%d columns=%d views=%d sequences=%d routines=%d triggers=%d synonyms=%d tab_privs=%d objects=%d\n\n",
		len(s.dictionary.Users), len(s.dictionary.Tables), len(s.dictionary.Columns), len(s.dictionary.Views), len(s.dictionary.Sequences), len(s.dictionary.Routines), len(s.dictionary.Triggers), len(s.dictionary.Synonyms), len(s.dictionary.TabPrivileges), s.dictionary.ObjectCount)
}

func (s *interactiveSession) printTables(stdout io.Writer, owner string) {
	owner = normalizeIdentifierInput(owner)
	var rows []dm.DictionaryTable
	for _, table := range s.dictionary.Tables {
		if strings.EqualFold(table.Owner, owner) {
			rows = append(rows, table)
		}
	}
	fmt.Fprintf(stdout, "%-22s %-34s %-10s %-10s %-12s %-12s %-10s\n", "owner", "table", "table_id", "columns", "tablespace", "storage", "partition")
	for _, table := range rows {
		partitioned := "NO"
		if table.Partitioned {
			partitioned = "YES"
		}
		tablespace := table.Tablespace
		if tablespace == "" && table.GroupID != 0 {
			tablespace = fmt.Sprintf("GROUP_%d", table.GroupID)
		}
		fmt.Fprintf(stdout, "%-22s %-34s %-10d %-10d %-12s %-12s %-10s\n",
			table.Owner,
			truncateForTable(table.Name, 34),
			table.ID,
			table.ColumnCount,
			tablespace,
			table.Storage,
			partitioned,
		)
	}
	fmt.Fprintf(stdout, "%d table(s)\n", len(rows))
}

func (s *interactiveSession) unloadTable(args []string, stdout io.Writer) error {
	tableToken := args[0]
	owner, tableName, ok := parseOwnerTableToken(tableToken)
	if !ok {
		return fmt.Errorf("usage: unload table <owner.table_name>")
	}
	table, ok := s.findTable(owner, tableName)
	if !ok {
		return fmt.Errorf("table %s not found in dictionary", tableToken)
	}
	prefix := sanitizedFilePrefix(table.Owner + "_" + table.Name)
	if customPrefix, ok := optionalToPrefix(args[1:]); ok {
		prefix = sanitizedFilePrefix(customPrefix)
	}
	ddlPath := s.outputPath(prefix + "_ddl.sql")
	dataExt := "sql"
	if s.dataFormat == "csv" {
		dataExt = "csv"
	}
	dataPath := s.outputPath(prefix + "_data." + dataExt)
	if err := s.ensureOutputDir(); err != nil {
		return err
	}
	ddl, err := dm.ExportDDL(dm.DDLExportOptions{
		SystemPath:     s.systemPath,
		ControlPath:    s.controlPath,
		ControlDULPath: s.effectiveControlDULPath(),
		OutputPath:     ddlPath,
		OwnerFilter:    table.Owner,
		TableFilter:    table.Owner + "." + table.Name,
		Charset:        s.charset,
		Dictionary:     s.dictionary,
	})
	if err != nil {
		return err
	}
	data, err := dm.ExportData(dm.DataExportOptions{
		SystemPath:     s.systemPath,
		ControlPath:    s.controlPath,
		ControlDULPath: s.effectiveControlDULPath(),
		DataDir:        s.effectiveDataDir(),
		OutputPath:     dataPath,
		OwnerFilter:    table.Owner,
		TableFilter:    table.Owner + "." + table.Name,
		ExcludeTables:  "",
		Charset:        s.charset,
		OutputFormat:   s.dataFormat,
		Dictionary:     s.dictionary,
	})
	if err != nil {
		return err
	}
	fmt.Fprintf(stdout, "ddl output: %s\n", ddl.OutputPath)
	if s.dataFormat == "csv" && data.OutputPath == "" {
		fmt.Fprintln(stdout, "data output: skipped (no rows)")
	} else {
		fmt.Fprintf(stdout, "data output: %s\n", data.OutputPath)
	}
	fmt.Fprintf(stdout, "tables exported: %d\n", ddl.TableCount)
	fmt.Fprintf(stdout, "views exported: %d\n", ddl.ViewCount)
	fmt.Fprintf(stdout, "sequences exported: %d\n", ddl.SequenceCount)
	fmt.Fprintf(stdout, "routines exported: %d\n", ddl.RoutineCount)
	fmt.Fprintf(stdout, "triggers exported: %d\n", ddl.TriggerCount)
	fmt.Fprintf(stdout, "synonyms exported: %d\n", ddl.SynonymCount)
	fmt.Fprintf(stdout, "tab privileges exported: %d\n", ddl.TabPrivilegeCount)
	fmt.Fprintf(stdout, "rows exported: %d\n", data.RowsExported)
	fmt.Fprintf(stdout, "rows failed: %d\n", data.RowsFailed)
	return nil
}

func (s *interactiveSession) recoverTable(args []string, stdout io.Writer) error {
	tableToken := args[0]
	owner, tableName, ok := parseOwnerTableToken(tableToken)
	if !ok {
		return fmt.Errorf("usage: recover table <owner.table_name>")
	}
	table, ok := s.findTable(owner, tableName)
	if !ok {
		return fmt.Errorf("table %s not found in dictionary; load a pre-drop dictionary or add the table to dmdul_dict first", tableToken)
	}
	prefix := sanitizedFilePrefix(table.Owner + "_" + table.Name + "_recover")
	if customPrefix, ok := optionalToPrefix(args[1:]); ok {
		prefix = sanitizedFilePrefix(customPrefix)
	}
	ddlPath := s.outputPath(prefix + "_ddl.sql")
	dataExt := "sql"
	if s.dataFormat == "csv" {
		dataExt = "csv"
	}
	dataPath := s.outputPath(prefix + "_data." + dataExt)
	if err := s.ensureOutputDir(); err != nil {
		return err
	}
	ddl, err := dm.ExportDDL(dm.DDLExportOptions{
		SystemPath:     s.systemPath,
		ControlPath:    s.controlPath,
		ControlDULPath: s.effectiveControlDULPath(),
		OutputPath:     ddlPath,
		OwnerFilter:    table.Owner,
		TableFilter:    table.Owner + "." + table.Name,
		Charset:        s.charset,
		Dictionary:     s.dictionary,
	})
	if err != nil {
		return err
	}
	data, err := dm.ExportData(dm.DataExportOptions{
		SystemPath:     s.systemPath,
		ControlPath:    s.controlPath,
		ControlDULPath: s.effectiveControlDULPath(),
		DataDir:        s.effectiveDataDir(),
		OutputPath:     dataPath,
		OwnerFilter:    table.Owner,
		TableFilter:    table.Owner + "." + table.Name,
		ExcludeTables:  "",
		Charset:        s.charset,
		OutputFormat:   s.dataFormat,
		RecoveryMode:   true,
		Dictionary:     s.dictionary,
	})
	if err != nil {
		return err
	}
	fmt.Fprintln(stdout, "recovery mode: on")
	fmt.Fprintf(stdout, "ddl output: %s\n", ddl.OutputPath)
	if s.dataFormat == "csv" && data.OutputPath == "" {
		fmt.Fprintln(stdout, "data output: skipped (no rows)")
	} else {
		fmt.Fprintf(stdout, "data output: %s\n", data.OutputPath)
	}
	fmt.Fprintf(stdout, "tables exported: %d\n", ddl.TableCount)
	fmt.Fprintf(stdout, "rows exported: %d\n", data.RowsExported)
	fmt.Fprintf(stdout, "rows failed: %d\n", data.RowsFailed)
	return nil
}

func (s *interactiveSession) unloadUser(args []string, stdout io.Writer) error {
	owner := normalizeIdentifierInput(args[0])
	if !s.hasOwner(owner) {
		return fmt.Errorf("user/owner %s not found in dictionary", args[0])
	}
	prefix := sanitizedFilePrefix(owner)
	if customPrefix, ok := optionalToPrefix(args[1:]); ok {
		prefix = sanitizedFilePrefix(customPrefix)
	}
	ddlPath := s.outputPath(prefix + "_ddl.sql")
	if err := s.ensureOutputDir(); err != nil {
		return err
	}
	ddl, err := dm.ExportDDL(dm.DDLExportOptions{
		SystemPath:     s.systemPath,
		ControlPath:    s.controlPath,
		ControlDULPath: s.effectiveControlDULPath(),
		OutputPath:     ddlPath,
		OwnerFilter:    owner,
		TableFilter:    "all",
		Charset:        s.charset,
		Dictionary:     s.dictionary,
	})
	if err != nil {
		return err
	}
	fmt.Fprintf(stdout, "ddl output: %s\n", ddl.OutputPath)
	fmt.Fprintf(stdout, "tables exported: %d\n", ddl.TableCount)
	fmt.Fprintf(stdout, "views exported: %d\n", ddl.ViewCount)
	fmt.Fprintf(stdout, "sequences exported: %d\n", ddl.SequenceCount)
	fmt.Fprintf(stdout, "routines exported: %d\n", ddl.RoutineCount)
	fmt.Fprintf(stdout, "triggers exported: %d\n", ddl.TriggerCount)
	fmt.Fprintf(stdout, "synonyms exported: %d\n", ddl.SynonymCount)
	fmt.Fprintf(stdout, "tab privileges exported: %d\n", ddl.TabPrivilegeCount)
	if s.dataFormat == "csv" {
		files, rowsExported, rowsFailed, err := s.unloadUserCSV(prefix, owner, stdout)
		if err != nil {
			return err
		}
		fmt.Fprintf(stdout, "csv output dir: %s\n", s.effectiveOutputDir())
		fmt.Fprintf(stdout, "csv files exported: %d\n", files)
		fmt.Fprintf(stdout, "rows exported: %d\n", rowsExported)
		fmt.Fprintf(stdout, "rows failed: %d\n", rowsFailed)
		return nil
	}
	dataPath := s.outputPath(prefix + "_data.sql")
	data, err := dm.ExportData(dm.DataExportOptions{
		SystemPath:     s.systemPath,
		ControlPath:    s.controlPath,
		ControlDULPath: s.effectiveControlDULPath(),
		DataDir:        s.effectiveDataDir(),
		OutputPath:     dataPath,
		OwnerFilter:    owner,
		TableFilter:    "all",
		ExcludeTables:  "",
		Charset:        s.charset,
		OutputFormat:   "sql",
		Dictionary:     s.dictionary,
	})
	if err != nil {
		return err
	}
	fmt.Fprintf(stdout, "data output: %s\n", data.OutputPath)
	fmt.Fprintf(stdout, "rows exported: %d\n", data.RowsExported)
	fmt.Fprintf(stdout, "rows failed: %d\n", data.RowsFailed)
	return nil
}

func (s *interactiveSession) unloadDatabase(args []string, stdout io.Writer) error {
	prefix := "DATABASE"
	if customPrefix, ok := optionalToPrefix(args); ok {
		prefix = sanitizedFilePrefix(customPrefix)
	}
	ddlPath := s.outputPath(prefix + "_ddl.sql")
	if err := s.ensureOutputDir(); err != nil {
		return err
	}
	ddl, err := dm.ExportDDL(dm.DDLExportOptions{
		SystemPath:     s.systemPath,
		ControlPath:    s.controlPath,
		ControlDULPath: s.effectiveControlDULPath(),
		OutputPath:     ddlPath,
		OwnerFilter:    "all",
		TableFilter:    "all",
		Charset:        s.charset,
		Dictionary:     s.dictionary,
	})
	if err != nil {
		return err
	}
	fmt.Fprintf(stdout, "ddl output: %s\n", ddl.OutputPath)
	fmt.Fprintf(stdout, "users exported: %d\n", ddl.UserCount)
	fmt.Fprintf(stdout, "tables exported: %d\n", ddl.TableCount)
	fmt.Fprintf(stdout, "views exported: %d\n", ddl.ViewCount)
	fmt.Fprintf(stdout, "sequences exported: %d\n", ddl.SequenceCount)
	fmt.Fprintf(stdout, "routines exported: %d\n", ddl.RoutineCount)
	fmt.Fprintf(stdout, "triggers exported: %d\n", ddl.TriggerCount)
	fmt.Fprintf(stdout, "synonyms exported: %d\n", ddl.SynonymCount)
	fmt.Fprintf(stdout, "tab privileges exported: %d\n", ddl.TabPrivilegeCount)
	if s.dataFormat == "csv" {
		files, rowsExported, rowsFailed, err := s.unloadDatabaseCSV(prefix, stdout)
		if err != nil {
			return err
		}
		fmt.Fprintf(stdout, "csv output dir: %s\n", s.effectiveOutputDir())
		fmt.Fprintf(stdout, "csv files exported: %d\n", files)
		fmt.Fprintf(stdout, "rows exported: %d\n", rowsExported)
		fmt.Fprintf(stdout, "rows failed: %d\n", rowsFailed)
		return nil
	}
	dataPath := s.outputPath(prefix + "_data.sql")
	data, err := dm.ExportData(dm.DataExportOptions{
		SystemPath:     s.systemPath,
		ControlPath:    s.controlPath,
		ControlDULPath: s.effectiveControlDULPath(),
		DataDir:        s.effectiveDataDir(),
		OutputPath:     dataPath,
		OwnerFilter:    "all",
		TableFilter:    "all",
		ExcludeTables:  "",
		Charset:        s.charset,
		OutputFormat:   "sql",
		Dictionary:     s.dictionary,
	})
	if err != nil {
		return err
	}
	fmt.Fprintf(stdout, "data output: %s\n", data.OutputPath)
	fmt.Fprintf(stdout, "rows exported: %d\n", data.RowsExported)
	fmt.Fprintf(stdout, "rows failed: %d\n", data.RowsFailed)
	return nil
}

func (s *interactiveSession) unloadUserCSV(prefix string, owner string, stdout io.Writer) (int, int, int, error) {
	files := 0
	rowsExported := 0
	rowsFailed := 0
	for _, table := range s.dictionary.Tables {
		if !strings.EqualFold(table.Owner, owner) || table.Temporary {
			continue
		}
		tablePrefix := sanitizedFilePrefix(prefix + "_" + table.Name)
		dataPath := s.outputPath(tablePrefix + "_data.csv")
		data, err := dm.ExportData(dm.DataExportOptions{
			SystemPath:     s.systemPath,
			ControlPath:    s.controlPath,
			ControlDULPath: s.effectiveControlDULPath(),
			DataDir:        s.effectiveDataDir(),
			OutputPath:     dataPath,
			OwnerFilter:    owner,
			TableFilter:    table.Owner + "." + table.Name,
			ExcludeTables:  "",
			Charset:        s.charset,
			OutputFormat:   "csv",
			Dictionary:     s.dictionary,
		})
		if err != nil {
			return files, rowsExported, rowsFailed, err
		}
		rowsExported += data.RowsExported
		rowsFailed += data.RowsFailed
		if data.OutputPath == "" {
			fmt.Fprintf(stdout, "csv skipped: %s.%s (no rows)\n", table.Owner, table.Name)
			continue
		}
		files++
		fmt.Fprintf(stdout, "csv output: %s\n", data.OutputPath)
	}
	return files, rowsExported, rowsFailed, nil
}

func (s *interactiveSession) unloadDatabaseCSV(prefix string, stdout io.Writer) (int, int, int, error) {
	files := 0
	rowsExported := 0
	rowsFailed := 0
	for _, table := range s.dictionary.Tables {
		if table.Temporary {
			continue
		}
		tablePrefix := sanitizedFilePrefix(prefix + "_" + table.Owner + "_" + table.Name)
		dataPath := s.outputPath(tablePrefix + "_data.csv")
		data, err := dm.ExportData(dm.DataExportOptions{
			SystemPath:     s.systemPath,
			ControlPath:    s.controlPath,
			ControlDULPath: s.effectiveControlDULPath(),
			DataDir:        s.effectiveDataDir(),
			OutputPath:     dataPath,
			OwnerFilter:    table.Owner,
			TableFilter:    table.Owner + "." + table.Name,
			ExcludeTables:  "",
			Charset:        s.charset,
			OutputFormat:   "csv",
			Dictionary:     s.dictionary,
		})
		if err != nil {
			return files, rowsExported, rowsFailed, err
		}
		rowsExported += data.RowsExported
		rowsFailed += data.RowsFailed
		if data.OutputPath == "" {
			fmt.Fprintf(stdout, "csv skipped: %s.%s (no rows)\n", table.Owner, table.Name)
			continue
		}
		files++
		fmt.Fprintf(stdout, "csv output: %s\n", data.OutputPath)
	}
	return files, rowsExported, rowsFailed, nil
}

func (s *interactiveSession) findTable(owner string, tableName string) (dm.DictionaryTable, bool) {
	owner = normalizeIdentifierInput(owner)
	tableName = normalizeIdentifierInput(tableName)
	for _, table := range s.dictionary.Tables {
		if strings.EqualFold(table.Owner, owner) && strings.EqualFold(table.Name, tableName) {
			return table, true
		}
	}
	return dm.DictionaryTable{}, false
}

func (s *interactiveSession) hasOwner(owner string) bool {
	owner = normalizeIdentifierInput(owner)
	for _, user := range s.dictionary.Users {
		if strings.EqualFold(user.Name, owner) {
			return true
		}
	}
	return false
}

func (s *interactiveSession) ensureDictionaryLoaded() error {
	if s.dictionary != nil {
		return nil
	}
	if err := s.loadDictionaryFiles(io.Discard); err != nil {
		return fmt.Errorf("dictionary not loaded, run bootstrap first or load dictionary files from %s: %w", s.effectiveDictionaryDir(), err)
	}
	return nil
}

func (s *interactiveSession) loadDictionaryFiles(stdout io.Writer) error {
	dict, files, err := dm.LoadDictionaryFiles(s.effectiveDictionaryDir())
	if err != nil {
		return err
	}
	s.dictionary = dict
	if strings.TrimSpace(dict.SystemPath) != "" {
		s.systemPath = dict.SystemPath
	}
	if strings.TrimSpace(dict.ControlPath) != "" {
		s.controlPath = dict.ControlPath
		s.controlProvided = true
	}
	if detectedCharset, ok := charsetParameterFromDictionary(dict.Charset); ok && !s.charsetExplicit {
		s.charset = detectedCharset
	}
	debug.FreeOSMemory()
	if stdout != io.Discard {
		fmt.Fprintf(stdout, "dictionary loaded: %s\n", files.Dir)
		fmt.Fprintf(stdout, "users=%d tables=%d columns=%d views=%d sequences=%d routines=%d triggers=%d synonyms=%d tab_privs=%d\n",
			files.UserCount, files.TableCount, files.ColumnCount, files.ViewCount, files.SequenceCount, files.RoutineCount, files.TriggerCount, files.SynonymCount, files.TabPrivilegeCount)
	}
	return nil
}

func (s *interactiveSession) outputPath(name string) string {
	if filepath.IsAbs(name) {
		return name
	}
	return filepath.Join(s.effectiveOutputDir(), name)
}

func (s *interactiveSession) effectiveDataDir() string {
	if s.dataDirSet && strings.TrimSpace(s.dataDir) != "" {
		return s.dataDir
	}
	dir := filepath.Dir(defaultIfBlank(s.systemPath, defaultSystemPath))
	if dir == "" {
		return "."
	}
	return dir
}

func (s *interactiveSession) effectiveOutputDir() string {
	if s.outputDirSet {
		return defaultIfBlank(s.outputDir, ".")
	}
	if s.dataDirSet && strings.TrimSpace(s.dataDir) != "" {
		return s.dataDir
	}
	return "."
}

func (s *interactiveSession) effectiveControlDULPath() string {
	return filepath.Join(s.effectiveOutputDir(), defaultControlDULPath)
}

func (s *interactiveSession) effectiveDictionaryDir() string {
	return filepath.Join(s.effectiveOutputDir(), dm.DefaultDictionaryDirName)
}

func (s *interactiveSession) effectiveInitDULPath() string {
	if s.dataDirSet && strings.TrimSpace(s.dataDir) != "" {
		return filepath.Join(s.dataDir, defaultInitDULPath)
	}
	return defaultInitDULPath
}

func (s *interactiveSession) effectiveLogPath() string {
	if s.logPathSet {
		if filepath.IsAbs(s.logPath) {
			return s.logPath
		}
		return filepath.Join(s.effectiveOutputDir(), s.logPath)
	}
	return filepath.Join(s.effectiveOutputDir(), "dul.log")
}

func (s *interactiveSession) ensureOutputDir() error {
	return os.MkdirAll(s.effectiveOutputDir(), 0755)
}

func (s *interactiveSession) loadConfigFile(stderr io.Writer) {
	path := defaultInitDULPath
	if err := s.loadInitDUL(path); err != nil {
		if !os.IsNotExist(err) {
			fmt.Fprintf(stderr, "warning: read %s: %v\n", path, err)
		}
	}
}

func (s *interactiveSession) loadInitDULCommand(stdout io.Writer) error {
	path := s.effectiveInitDULPath()
	if err := s.loadInitDUL(path); err != nil {
		return err
	}
	s.dictionary = nil
	fmt.Fprintf(stdout, "init loaded: %s\n", path)
	return nil
}

func (s *interactiveSession) loadInitDUL(path string) error {
	s.charsetExplicit = false
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		line = strings.TrimPrefix(line, "\ufeff")
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, "--") {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key = normalizeParameterName(key)
		value = strings.TrimSpace(value)
		switch key {
		case "system", "system_file", "file":
			s.systemPath = value
		case "control", "ctl":
			s.controlPath = value
			s.controlProvided = value != ""
		case "data_dir", "datadir":
			s.dataDir = value
			s.dataDirSet = value != ""
		case "data_format", "format":
			value = strings.ToLower(value)
			if value == "sql" || value == "csv" {
				s.dataFormat = value
			}
		case "charset":
			s.charset = value
		case "output_dir", "outdir":
			s.outputDir = value
			s.outputDirSet = value != ""
		case "log":
			s.logPath = value
			s.logPathSet = value != ""
		}
	}
	if err := scanner.Err(); err != nil {
		return err
	}
	s.initSource = path
	return nil
}

func (s *interactiveSession) writeInitDUL() error {
	path := s.effectiveInitDULPath()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(s.initDULContent()), 0644)
}

func (s *interactiveSession) initDULContent() string {
	var out strings.Builder
	out.WriteString("# Generated by dmdul interactive shell.\n")
	out.WriteString("# This file mirrors show parameter; blank values keep automatic defaults.\n")
	out.WriteString(fmt.Sprintf("# effective_control_dul=%s\n", s.effectiveControlDULPath()))
	out.WriteString(fmt.Sprintf("# effective_dict_dir=%s\n", s.effectiveDictionaryDir()))
	out.WriteString(fmt.Sprintf("# effective_init_dul=%s\n", s.effectiveInitDULPath()))
	out.WriteString(fmt.Sprintf("# effective_output_dir=%s\n", s.effectiveOutputDir()))
	out.WriteString(fmt.Sprintf("# effective_log=%s\n", s.effectiveLogPath()))
	out.WriteString(fmt.Sprintf("# init_load=%s\n", defaultIfBlank(s.initSource, "(not loaded)")))
	if s.dictionary != nil {
		out.WriteString(fmt.Sprintf("# dictionary=%s\n", defaultIfBlank(s.dictionary.Source, "memory")))
	} else {
		out.WriteString("# dictionary=not loaded\n")
	}
	out.WriteString(fmt.Sprintf("system=%s\n", s.systemPath))
	out.WriteString(fmt.Sprintf("control=%s\n", s.controlPath))
	if s.dataDirSet {
		out.WriteString(fmt.Sprintf("data_dir=%s\n", s.dataDir))
	} else {
		out.WriteString("data_dir=\n")
	}
	if s.outputDirSet {
		out.WriteString(fmt.Sprintf("output_dir=%s\n", s.outputDir))
	} else {
		out.WriteString("output_dir=\n")
	}
	out.WriteString(fmt.Sprintf("data_format=%s\n", s.dataFormat))
	out.WriteString(fmt.Sprintf("charset=%s\n", s.charset))
	if s.logPathSet {
		out.WriteString(fmt.Sprintf("log=%s\n", s.logPath))
	} else {
		out.WriteString("log=\n")
	}
	return out.String()
}

func (s *interactiveSession) openLog() {
	path := s.effectiveLogPath()
	if s.logFile != nil && s.logOpenPath == path {
		return
	}
	if s.logFile != nil {
		_ = s.logFile.Close()
		s.logFile = nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		if s.stderr != nil {
			fmt.Fprintf(s.stderr, "warning: create log directory %s: %v\n", filepath.Dir(path), err)
		}
		return
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		if s.stderr != nil {
			fmt.Fprintf(s.stderr, "warning: open log %s: %v\n", path, err)
		}
		return
	}
	s.logFile = file
	s.logOpenPath = path
}

func (s *interactiveSession) closeLog() {
	if s.logFile == nil {
		return
	}
	s.log("DMDUL session ended")
	_ = s.logFile.Close()
}

func (s *interactiveSession) log(line string) {
	s.openLog()
	if s.logFile == nil {
		return
	}
	fmt.Fprintln(s.logFile, timestampedLogLine(line, time.Now()))
}

func timestampedLogLine(line string, at time.Time) string {
	return fmt.Sprintf("%s %s", at.Format("2006-01-02 15:04:05"), line)
}

func splitInteractiveCommands(line string) []string {
	var commands []string
	for _, part := range strings.Split(line, ";") {
		part = strings.TrimSpace(part)
		if part != "" {
			commands = append(commands, part)
		}
	}
	return commands
}

func splitCommandFields(command string) []string {
	var fields []string
	var current strings.Builder
	inQuote := false
	for _, r := range command {
		switch {
		case r == '"':
			inQuote = !inQuote
			current.WriteRune(r)
		case unicode.IsSpace(r) && !inQuote:
			if current.Len() > 0 {
				fields = append(fields, strings.TrimSpace(current.String()))
				current.Reset()
			}
		default:
			current.WriteRune(r)
		}
	}
	if current.Len() > 0 {
		fields = append(fields, strings.TrimSpace(current.String()))
	}
	return fields
}

func parseOwnerTableToken(token string) (string, string, bool) {
	parts := splitQualifiedToken(token)
	if len(parts) != 2 {
		return "", "", false
	}
	return normalizeIdentifierInput(parts[0]), normalizeIdentifierInput(parts[1]), true
}

func splitQualifiedToken(token string) []string {
	var parts []string
	var current strings.Builder
	inQuote := false
	for _, r := range token {
		switch r {
		case '"':
			inQuote = !inQuote
			current.WriteRune(r)
		case '.':
			if inQuote {
				current.WriteRune(r)
				continue
			}
			parts = append(parts, current.String())
			current.Reset()
		default:
			current.WriteRune(r)
		}
	}
	parts = append(parts, current.String())
	return parts
}

func normalizeIdentifierInput(value string) string {
	value = strings.TrimSpace(value)
	if len(value) >= 2 && strings.HasPrefix(value, `"`) && strings.HasSuffix(value, `"`) {
		return strings.ReplaceAll(value[1:len(value)-1], `""`, `"`)
	}
	return strings.ToUpper(value)
}

func optionalToPrefix(args []string) (string, bool) {
	if len(args) == 0 {
		return "", false
	}
	if len(args) >= 2 && strings.EqualFold(args[0], "to") {
		return strings.Join(args[1:], " "), true
	}
	return "", false
}

func sanitizedFilePrefix(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "dmdul"
	}
	var out strings.Builder
	for _, r := range value {
		switch r {
		case '<', '>', ':', '"', '/', '\\', '|', '?', '*', '.', ' ', '\t':
			out.WriteByte('_')
		default:
			out.WriteRune(r)
		}
	}
	result := strings.Trim(out.String(), "_")
	if result == "" {
		return "dmdul"
	}
	return result
}

func normalizeParameterName(value string) string {
	return strings.ReplaceAll(strings.ToLower(strings.TrimSpace(value)), "-", "_")
}

func charsetParameterFromDictionary(value string) (string, bool) {
	value = strings.ToUpper(strings.TrimSpace(value))
	switch {
	case strings.Contains(value, "GB18030") || strings.Contains(value, "GBK") || strings.Contains(value, "UNICODE_FLAG=0"):
		return "gb18030", true
	case strings.Contains(value, "UTF-8") || strings.Contains(value, "UTF8") || strings.Contains(value, "UNICODE_FLAG=1"):
		return "utf-8", true
	case strings.Contains(value, "EUC-KR") || strings.Contains(value, "EUCKR") || strings.Contains(value, "UNICODE_FLAG=2"):
		return "euc-kr", true
	default:
		return "", false
	}
}

func (s *interactiveSession) bootstrapCharset(systemPath string, controlPath string) string {
	configured := defaultIfBlank(s.charset, "auto")
	if s.charsetExplicit {
		return configured
	}
	metadata := dm.InspectDatabaseMetadata(systemPath, controlPath, "", configured)
	if metadata.HasCharsetFlag {
		if detected, ok := charsetParameterFromDictionary(metadata.Charset); ok {
			return detected
		}
	}
	return configured
}
