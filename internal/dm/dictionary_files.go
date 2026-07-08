package dm

import (
	"encoding/csv"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

const DefaultDictionaryDirName = "dmdul_dict"

type DictionaryFilesResult struct {
	Dir               string
	MetaPath          string
	UsersPath         string
	TablesPath        string
	ColumnsPath       string
	ViewsPath         string
	SequencesPath     string
	RoutinesPath      string
	TriggersPath      string
	SynonymsPath      string
	TabPrivilegesPath string
	UserCount         int
	TableCount        int
	ColumnCount       int
	ViewCount         int
	SequenceCount     int
	RoutineCount      int
	TriggerCount      int
	SynonymCount      int
	TabPrivilegeCount int
}

func WriteDictionaryFiles(dir string, dict *DictionaryInfo) (*DictionaryFilesResult, error) {
	if dict == nil {
		return nil, fmt.Errorf("dictionary is nil")
	}
	if strings.TrimSpace(dir) == "" {
		return nil, fmt.Errorf("dictionary directory is empty")
	}
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, err
	}
	result := dictionaryFilesResultForDir(dir)
	if err := writeDictionaryMeta(result.MetaPath, dict); err != nil {
		return nil, err
	}
	if err := writeDictionaryUsers(result.UsersPath, dict.Users); err != nil {
		return nil, err
	}
	if err := writeDictionaryTables(result.TablesPath, dict.Tables); err != nil {
		return nil, err
	}
	if err := writeDictionaryColumns(result.ColumnsPath, dict.Columns); err != nil {
		return nil, err
	}
	if err := writeDictionaryViews(result.ViewsPath, dict.Views); err != nil {
		return nil, err
	}
	if err := writeDictionarySequences(result.SequencesPath, dict.Sequences); err != nil {
		return nil, err
	}
	if err := writeDictionaryRoutines(result.RoutinesPath, dict.Routines); err != nil {
		return nil, err
	}
	if err := writeDictionaryTriggers(result.TriggersPath, dict.Triggers); err != nil {
		return nil, err
	}
	if err := writeDictionarySynonyms(result.SynonymsPath, dict.Synonyms); err != nil {
		return nil, err
	}
	if err := writeDictionaryTabPrivileges(result.TabPrivilegesPath, dict.TabPrivileges); err != nil {
		return nil, err
	}
	result.UserCount = len(dict.Users)
	result.TableCount = len(dict.Tables)
	result.ColumnCount = len(dict.Columns)
	result.ViewCount = len(dict.Views)
	result.SequenceCount = len(dict.Sequences)
	result.RoutineCount = len(dict.Routines)
	result.TriggerCount = len(dict.Triggers)
	result.SynonymCount = len(dict.Synonyms)
	result.TabPrivilegeCount = len(dict.TabPrivileges)
	return result, nil
}

func LoadDictionaryFiles(dir string) (*DictionaryInfo, *DictionaryFilesResult, error) {
	if strings.TrimSpace(dir) == "" {
		return nil, nil, fmt.Errorf("dictionary directory is empty")
	}
	result := dictionaryFilesResultForDir(dir)
	meta, err := readDictionaryMeta(result.MetaPath)
	if err != nil {
		return nil, nil, err
	}
	users, err := readDictionaryUsers(result.UsersPath)
	if err != nil {
		return nil, nil, err
	}
	tables, err := readDictionaryTables(result.TablesPath)
	if err != nil {
		return nil, nil, err
	}
	columns, err := readDictionaryColumns(result.ColumnsPath)
	if err != nil && !os.IsNotExist(err) {
		return nil, nil, err
	}
	if err != nil && os.IsNotExist(err) {
		columns = nil
	}
	views, err := readDictionaryViews(result.ViewsPath)
	if err != nil && !os.IsNotExist(err) {
		return nil, nil, err
	}
	if err != nil && os.IsNotExist(err) {
		views = nil
	}
	sequences, err := readDictionarySequences(result.SequencesPath)
	if err != nil && !os.IsNotExist(err) {
		return nil, nil, err
	}
	if err != nil && os.IsNotExist(err) {
		sequences = nil
	}
	routines, err := readDictionaryRoutines(result.RoutinesPath)
	if err != nil && !os.IsNotExist(err) {
		return nil, nil, err
	}
	if err != nil && os.IsNotExist(err) {
		routines = nil
	}
	triggers, err := readDictionaryTriggers(result.TriggersPath)
	if err != nil && !os.IsNotExist(err) {
		return nil, nil, err
	}
	if err != nil && os.IsNotExist(err) {
		triggers = nil
	}
	synonyms, err := readDictionarySynonyms(result.SynonymsPath)
	if err != nil && !os.IsNotExist(err) {
		return nil, nil, err
	}
	if err != nil && os.IsNotExist(err) {
		synonyms = nil
	}
	tabPrivileges, err := readDictionaryTabPrivileges(result.TabPrivilegesPath)
	if err != nil && !os.IsNotExist(err) {
		return nil, nil, err
	}
	if err != nil && os.IsNotExist(err) {
		tabPrivileges = nil
	}
	users, tables, columns, views, sequences, routines, triggers, synonyms, tabPrivileges = normalizeDictionaryFromFiles(users, tables, columns, views, sequences, routines, triggers, synonyms, tabPrivileges)
	dict := &DictionaryInfo{
		SystemPath:        meta["system_path"],
		ControlPath:       meta["control_path"],
		Source:            "dictionary files",
		DictionaryDir:     dir,
		ExtentSize:        parseMetaUint32(meta["extent_size"]),
		ExtentSizeSource:  meta["extent_size_source"],
		PageSize:          parseMetaUint32(meta["page_size"]),
		PageCount:         parseMetaUint32(meta["page_count"]),
		Charset:           meta["charset"],
		CharsetSource:     meta["charset_source"],
		ObjectCount:       parseMetaInt(meta["object_count"]),
		UserCount:         len(users),
		TableCount:        len(tables),
		ColumnCount:       len(columns),
		ViewCount:         len(views),
		SequenceCount:     len(sequences),
		RoutineCount:      len(routines),
		TriggerCount:      len(triggers),
		SynonymCount:      len(synonyms),
		TabPrivilegeCount: len(tabPrivileges),
		Users:             users,
		Tables:            tables,
		Columns:           columns,
		Views:             views,
		Sequences:         sequences,
		Routines:          routines,
		Triggers:          triggers,
		Synonyms:          synonyms,
		TabPrivileges:     tabPrivileges,
	}
	result.UserCount = len(users)
	result.TableCount = len(tables)
	result.ColumnCount = len(columns)
	result.ViewCount = len(views)
	result.SequenceCount = len(sequences)
	result.RoutineCount = len(routines)
	result.TriggerCount = len(triggers)
	result.SynonymCount = len(synonyms)
	result.TabPrivilegeCount = len(tabPrivileges)
	return dict, result, nil
}

func dictionaryFilesResultForDir(dir string) *DictionaryFilesResult {
	return &DictionaryFilesResult{
		Dir:               dir,
		MetaPath:          filepath.Join(dir, "meta.tsv"),
		UsersPath:         filepath.Join(dir, "users.tsv"),
		TablesPath:        filepath.Join(dir, "tables.tsv"),
		ColumnsPath:       filepath.Join(dir, "columns.tsv"),
		ViewsPath:         filepath.Join(dir, "views.tsv"),
		SequencesPath:     filepath.Join(dir, "sequences.tsv"),
		RoutinesPath:      filepath.Join(dir, "routines.tsv"),
		TriggersPath:      filepath.Join(dir, "triggers.tsv"),
		SynonymsPath:      filepath.Join(dir, "synonyms.tsv"),
		TabPrivilegesPath: filepath.Join(dir, "tab_privs.tsv"),
	}
}

func writeDictionaryMeta(path string, dict *DictionaryInfo) error {
	rows := [][]string{
		{"format_version", "1"},
		{"source", dict.Source},
		{"system_path", dict.SystemPath},
		{"control_path", dict.ControlPath},
		{"extent_size", formatUint32Field(dict.ExtentSize)},
		{"extent_size_source", dict.ExtentSizeSource},
		{"page_size", formatUint32Field(dict.PageSize)},
		{"page_count", formatUint32Field(dict.PageCount)},
		{"charset", dict.Charset},
		{"charset_source", dict.CharsetSource},
		{"object_count", strconv.Itoa(dict.ObjectCount)},
		{"user_count", strconv.Itoa(len(dict.Users))},
		{"table_count", strconv.Itoa(len(dict.Tables))},
		{"column_count", strconv.Itoa(len(dict.Columns))},
		{"view_count", strconv.Itoa(len(dict.Views))},
		{"sequence_count", strconv.Itoa(len(dict.Sequences))},
		{"routine_count", strconv.Itoa(len(dict.Routines))},
		{"trigger_count", strconv.Itoa(len(dict.Triggers))},
		{"synonym_count", strconv.Itoa(len(dict.Synonyms))},
		{"tab_privilege_count", strconv.Itoa(len(dict.TabPrivileges))},
	}
	return writeTSV(path, []string{"key", "value"}, rows)
}

func writeDictionaryUsers(path string, users []DictionaryUser) error {
	rows := make([][]string, 0, len(users))
	for _, user := range users {
		rows = append(rows, []string{formatUint32Field(user.ID), user.Name})
	}
	return writeTSV(path, []string{"user_id", "user_name"}, rows)
}

func writeDictionaryTables(path string, tables []DictionaryTable) error {
	rows := make([][]string, 0, len(tables))
	for _, table := range tables {
		headerFile := ""
		if dictionaryTableHasSegment(table) {
			headerFile = strconv.FormatInt(int64(table.HeaderFile), 10)
		}
		rows = append(rows, []string{
			formatUint32Field(table.ID),
			table.Owner,
			table.Name,
			strconv.Itoa(table.ColumnCount),
			table.Tablespace,
			formatUint32Field(table.GroupID),
			headerFile,
			formatUint32Field(table.HeaderBlock),
			formatUint64Field(table.Bytes),
			formatUint32Field(table.Blocks),
			formatUint32Field(table.Extents),
			formatBoolField(table.Temporary),
			table.Storage,
			formatBoolField(table.Partitioned),
			formatUint32Field(table.StorageID),
			formatInt16Field(table.RootFile),
			formatUint32Field(table.RootPage),
			formatUint32ListField(table.AssistIDs),
		})
	}
	return writeTSV(path, []string{"table_id", "owner", "table_name", "column_count", "tablespace", "group_id", "header_file", "header_block", "bytes", "blocks", "extents", "temporary", "storage", "partitioned", "storage_id", "root_file", "root_page", "assist_ids"}, rows)
}

func writeDictionaryColumns(path string, columns []DictionaryColumn) error {
	rows := make([][]string, 0, len(columns))
	for _, col := range columns {
		rows = append(rows, []string{
			formatUint32Field(col.TableID),
			col.TableOwner,
			col.TableName,
			strconv.Itoa(int(col.ColID)),
			col.Name,
			col.DataType,
			formatUint32Field(col.Length),
			strconv.Itoa(int(col.Scale)),
			col.Nullable,
			col.Default,
		})
	}
	return writeTSV(path, []string{"table_id", "owner", "table_name", "col_id", "column_name", "data_type", "length", "scale", "nullable", "default"}, rows)
}

func writeDictionaryViews(path string, views []DictionaryView) error {
	rows := make([][]string, 0, len(views))
	for _, view := range views {
		rows = append(rows, []string{
			formatUint32Field(view.ID),
			view.Owner,
			view.Name,
			view.Valid,
			cleanRecoveredSQLText(view.SQL),
			cleanRecoveredSQLText(view.QuerySQL),
		})
	}
	return writeTSV(path, []string{"view_id", "owner", "view_name", "valid", "sql", "query_sql"}, rows)
}

func writeDictionarySequences(path string, sequences []DictionarySequence) error {
	rows := make([][]string, 0, len(sequences))
	for _, seq := range sequences {
		rows = append(rows, []string{
			formatUint32Field(seq.ID),
			seq.Owner,
			seq.Name,
			seq.Valid,
			formatUint64Field(seq.StartWith),
			formatUint64Field(seq.MinValue),
			formatUint64Field(seq.MaxValue),
			formatInt64Field(seq.IncrementBy),
			seq.CycleFlag,
			seq.OrderFlag,
			formatUint32Field(seq.CacheSize),
			cleanRecoveredSQLText(seq.SQL),
		})
	}
	return writeTSV(path, []string{"sequence_id", "owner", "sequence_name", "valid", "start_with", "min_value", "max_value", "increment_by", "cycle_flag", "order_flag", "cache_size", "sql"}, rows)
}

func writeDictionaryRoutines(path string, routines []DictionaryRoutine) error {
	rows := make([][]string, 0, len(routines))
	for _, routine := range routines {
		rows = append(rows, []string{
			formatUint32Field(routine.ID),
			routine.Owner,
			routine.Name,
			normalizeRoutineObjectType(routine.ObjectType),
			strconv.FormatUint(uint64(routine.SeqNo), 10),
			routine.Valid,
			cleanRecoveredSQLText(routine.SQL),
		})
	}
	return writeTSV(path, []string{"routine_id", "owner", "routine_name", "object_type", "seq_no", "valid", "sql"}, rows)
}

func writeDictionaryTriggers(path string, triggers []DictionaryTrigger) error {
	rows := make([][]string, 0, len(triggers))
	for _, trigger := range triggers {
		rows = append(rows, []string{
			formatUint32Field(trigger.ID),
			trigger.Owner,
			trigger.Name,
			trigger.TableOwner,
			trigger.TableName,
			trigger.Valid,
			cleanRecoveredSQLText(trigger.SQL),
		})
	}
	return writeTSV(path, []string{"trigger_id", "owner", "trigger_name", "table_owner", "table_name", "valid", "sql"}, rows)
}

func writeDictionarySynonyms(path string, synonyms []DictionarySynonym) error {
	rows := make([][]string, 0, len(synonyms))
	for _, syn := range synonyms {
		rows = append(rows, []string{
			formatUint32Field(syn.ID),
			syn.Owner,
			syn.Name,
			syn.TableOwner,
			syn.TableName,
			formatBoolField(syn.Public),
		})
	}
	return writeTSV(path, []string{"synonym_id", "owner", "synonym_name", "table_owner", "table_name", "public"}, rows)
}

func writeDictionaryTabPrivileges(path string, privileges []DictionaryTabPrivilege) error {
	rows := make([][]string, 0, len(privileges))
	for _, priv := range privileges {
		rows = append(rows, []string{
			priv.Grantee,
			priv.Owner,
			priv.ObjectName,
			priv.ObjectType,
			priv.Privilege,
			priv.Grantable,
		})
	}
	return writeTSV(path, []string{"grantee", "owner", "object_name", "object_type", "privilege", "grantable"}, rows)
}

func readDictionaryMeta(path string) (map[string]string, error) {
	records, err := readTSV(path)
	if err != nil {
		return nil, err
	}
	meta := make(map[string]string)
	for _, rec := range records {
		if len(rec) < 2 || rec[0] == "key" {
			continue
		}
		meta[rec[0]] = rec[1]
	}
	if len(meta) == 0 {
		return nil, fmt.Errorf("dictionary meta is empty: %s", path)
	}
	return meta, nil
}

func readDictionaryUsers(path string) ([]DictionaryUser, error) {
	records, err := readTSV(path)
	if err != nil {
		return nil, err
	}
	var users []DictionaryUser
	for _, rec := range records {
		if len(rec) < 2 || rec[0] == "user_id" {
			continue
		}
		users = append(users, DictionaryUser{
			ID:   parseUint32Field(rec[0]),
			Name: rec[1],
		})
	}
	return users, nil
}

func readDictionaryTables(path string) ([]DictionaryTable, error) {
	records, err := readTSV(path)
	if err != nil {
		return nil, err
	}
	var tables []DictionaryTable
	for _, rec := range records {
		if len(rec) < 9 || rec[0] == "table_id" {
			continue
		}
		table := DictionaryTable{
			ID:          parseUint32Field(rec[0]),
			Owner:       rec[1],
			Name:        rec[2],
			ColumnCount: parseIntField(rec[3]),
			Tablespace:  rec[4],
			GroupID:     parseUint32Field(rec[5]),
		}
		if len(rec) >= 14 {
			table.HeaderFile = int16(parseIntField(rec[6]))
			table.HeaderBlock = parseUint32Field(rec[7])
			table.Bytes = parseUint64Field(rec[8])
			table.Blocks = parseUint32Field(rec[9])
			table.Extents = parseUint32Field(rec[10])
			table.Temporary = parseBoolField(rec[11])
			table.Storage = rec[12]
			table.Partitioned = parseBoolField(rec[13])
			if len(rec) >= 18 {
				table.StorageID = parseUint32Field(rec[14])
				table.RootFile = parseOptionalInt16Field(rec[15], -1)
				table.RootPage = parseUint32Field(rec[16])
				table.AssistIDs = parseUint32ListField(rec[17])
			}
			tables = append(tables, table)
			continue
		}
		tables = append(tables, DictionaryTable{
			ID:          table.ID,
			Owner:       table.Owner,
			Name:        table.Name,
			ColumnCount: table.ColumnCount,
			Tablespace:  table.Tablespace,
			GroupID:     table.GroupID,
			Temporary:   parseBoolField(rec[6]),
			Storage:     rec[7],
			Partitioned: parseBoolField(rec[8]),
		})
	}
	return tables, nil
}

func readDictionaryColumns(path string) ([]DictionaryColumn, error) {
	records, err := readTSV(path)
	if err != nil {
		return nil, err
	}
	var columns []DictionaryColumn
	for _, rec := range records {
		if len(rec) < 10 || rec[0] == "table_id" {
			continue
		}
		columns = append(columns, DictionaryColumn{
			TableID:    parseUint32Field(rec[0]),
			TableOwner: rec[1],
			TableName:  rec[2],
			ColID:      uint16(parseUint32Field(rec[3])),
			Name:       rec[4],
			DataType:   rec[5],
			Length:     parseUint32Field(rec[6]),
			Scale:      int16(parseIntField(rec[7])),
			Nullable:   rec[8],
			Default:    rec[9],
		})
	}
	return columns, nil
}

func readDictionaryViews(path string) ([]DictionaryView, error) {
	records, err := readTSV(path)
	if err != nil {
		return nil, err
	}
	var views []DictionaryView
	for _, rec := range records {
		if len(rec) < 5 || rec[0] == "view_id" {
			continue
		}
		view := DictionaryView{
			ID:    parseUint32Field(rec[0]),
			Owner: rec[1],
			Name:  rec[2],
			Valid: rec[3],
			SQL:   cleanRecoveredSQLText(rec[4]),
		}
		if len(rec) >= 6 {
			view.QuerySQL = cleanRecoveredSQLText(rec[5])
		}
		views = append(views, view)
	}
	return views, nil
}

func readDictionarySequences(path string) ([]DictionarySequence, error) {
	records, err := readTSV(path)
	if err != nil {
		return nil, err
	}
	var sequences []DictionarySequence
	for _, rec := range records {
		if len(rec) < 7 || rec[0] == "sequence_id" {
			continue
		}
		seq := DictionarySequence{
			ID:    parseUint32Field(rec[0]),
			Owner: rec[1],
			Name:  rec[2],
			Valid: rec[3],
		}
		if len(rec) >= 12 {
			seq.StartWith = parseUint64Field(rec[4])
			seq.MinValue = parseUint64Field(rec[5])
			seq.MaxValue = parseUint64Field(rec[6])
			seq.IncrementBy = parseInt64Field(rec[7])
			seq.CycleFlag = rec[8]
			seq.OrderFlag = rec[9]
			seq.CacheSize = parseUint32Field(rec[10])
			seq.SQL = cleanRecoveredSQLText(rec[11])
		} else {
			seq.IncrementBy = parseInt64Field(rec[4])
			seq.CycleFlag = rec[5]
			seq.OrderFlag = rec[6]
			if len(rec) >= 8 {
				seq.SQL = cleanRecoveredSQLText(rec[7])
			}
		}
		sequences = append(sequences, seq)
	}
	return sequences, nil
}

func readDictionaryRoutines(path string) ([]DictionaryRoutine, error) {
	records, err := readTSV(path)
	if err != nil {
		return nil, err
	}
	var routines []DictionaryRoutine
	for _, rec := range records {
		if len(rec) < 7 || rec[0] == "routine_id" {
			continue
		}
		routines = append(routines, DictionaryRoutine{
			ID:         parseUint32Field(rec[0]),
			Owner:      rec[1],
			Name:       rec[2],
			ObjectType: normalizeRoutineObjectType(rec[3]),
			SeqNo:      parseUint32Field(rec[4]),
			Valid:      rec[5],
			SQL:        cleanRecoveredSQLText(rec[6]),
		})
	}
	return routines, nil
}

func readDictionaryTriggers(path string) ([]DictionaryTrigger, error) {
	records, err := readTSV(path)
	if err != nil {
		return nil, err
	}
	var triggers []DictionaryTrigger
	for _, rec := range records {
		if len(rec) < 7 || rec[0] == "trigger_id" {
			continue
		}
		triggers = append(triggers, DictionaryTrigger{
			ID:         parseUint32Field(rec[0]),
			Owner:      rec[1],
			Name:       rec[2],
			TableOwner: rec[3],
			TableName:  rec[4],
			Valid:      rec[5],
			SQL:        cleanRecoveredSQLText(rec[6]),
		})
	}
	return triggers, nil
}

func readDictionarySynonyms(path string) ([]DictionarySynonym, error) {
	records, err := readTSV(path)
	if err != nil {
		return nil, err
	}
	var synonyms []DictionarySynonym
	for _, rec := range records {
		if len(rec) < 6 || rec[0] == "synonym_id" {
			continue
		}
		synonyms = append(synonyms, DictionarySynonym{
			ID:         parseUint32Field(rec[0]),
			Owner:      rec[1],
			Name:       rec[2],
			TableOwner: rec[3],
			TableName:  rec[4],
			Public:     parseBoolField(rec[5]),
		})
	}
	return synonyms, nil
}

func readDictionaryTabPrivileges(path string) ([]DictionaryTabPrivilege, error) {
	records, err := readTSV(path)
	if err != nil {
		return nil, err
	}
	var privileges []DictionaryTabPrivilege
	for _, rec := range records {
		if len(rec) < 6 || rec[0] == "grantee" {
			continue
		}
		privileges = append(privileges, DictionaryTabPrivilege{
			Grantee:    rec[0],
			Owner:      rec[1],
			ObjectName: rec[2],
			ObjectType: rec[3],
			Privilege:  rec[4],
			Grantable:  rec[5],
		})
	}
	return privileges, nil
}

func normalizeDictionaryFromFiles(users []DictionaryUser, tables []DictionaryTable, columns []DictionaryColumn, views []DictionaryView, sequences []DictionarySequence, routines []DictionaryRoutine, triggers []DictionaryTrigger, synonyms []DictionarySynonym, privileges []DictionaryTabPrivilege) ([]DictionaryUser, []DictionaryTable, []DictionaryColumn, []DictionaryView, []DictionarySequence, []DictionaryRoutine, []DictionaryTrigger, []DictionarySynonym, []DictionaryTabPrivilege) {
	columnCounts := make(map[uint32]int)
	for _, col := range columns {
		columnCounts[col.TableID]++
	}
	for i := range tables {
		if tables[i].ColumnCount == 0 {
			tables[i].ColumnCount = columnCounts[tables[i].ID]
		}
	}
	userNames := make(map[string]bool)
	for _, user := range users {
		userNames[strings.ToUpper(user.Name)] = true
	}
	for _, table := range tables {
		key := strings.ToUpper(table.Owner)
		if key == "" || userNames[key] {
			continue
		}
		users = append(users, DictionaryUser{Name: table.Owner})
		userNames[key] = true
	}
	sort.Slice(users, func(i, j int) bool {
		return users[i].Name < users[j].Name
	})
	sort.Slice(tables, func(i, j int) bool {
		if tables[i].Owner == tables[j].Owner {
			if tables[i].Name == tables[j].Name {
				return tables[i].ID < tables[j].ID
			}
			return tables[i].Name < tables[j].Name
		}
		return tables[i].Owner < tables[j].Owner
	})
	sort.Slice(columns, func(i, j int) bool {
		if columns[i].TableOwner != columns[j].TableOwner {
			return columns[i].TableOwner < columns[j].TableOwner
		}
		if columns[i].TableName != columns[j].TableName {
			return columns[i].TableName < columns[j].TableName
		}
		return columns[i].ColID < columns[j].ColID
	})
	sortDictionaryViews(views)
	sortDictionarySequences(sequences)
	sortDictionaryRoutines(routines)
	sortDictionaryTriggers(triggers)
	sortDictionarySynonyms(synonyms)
	sortDictionaryTabPrivileges(privileges)
	return users, tables, columns, views, sequences, routines, triggers, synonyms, privileges
}

func writeTSV(path string, header []string, rows [][]string) error {
	file, err := os.Create(path)
	if err != nil {
		return err
	}
	defer file.Close()
	writer := csv.NewWriter(file)
	writer.Comma = '\t'
	if len(header) > 0 {
		if err := writer.Write(header); err != nil {
			return err
		}
	}
	for _, row := range rows {
		if err := writer.Write(row); err != nil {
			return err
		}
	}
	writer.Flush()
	return writer.Error()
}

func readTSV(path string) ([][]string, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	reader := csv.NewReader(file)
	reader.Comma = '\t'
	reader.FieldsPerRecord = -1
	reader.Comment = '#'
	var records [][]string
	for {
		rec, err := reader.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		if len(rec) > 0 {
			rec[0] = strings.TrimPrefix(rec[0], "\ufeff")
		}
		records = append(records, rec)
	}
	return records, nil
}

func formatUint32Field(value uint32) string {
	if value == 0 {
		return ""
	}
	return strconv.FormatUint(uint64(value), 10)
}

func formatInt16Field(value int16) string {
	if value < 0 {
		return ""
	}
	return strconv.FormatInt(int64(value), 10)
}

func formatUint32ListField(values []uint32) string {
	if len(values) == 0 {
		return ""
	}
	seen := make(map[uint32]bool, len(values))
	var parts []string
	for _, value := range values {
		if value == 0 || seen[value] {
			continue
		}
		seen[value] = true
		parts = append(parts, strconv.FormatUint(uint64(value), 10))
	}
	return strings.Join(parts, ",")
}

func formatUint64Field(value uint64) string {
	if value == 0 {
		return ""
	}
	return strconv.FormatUint(value, 10)
}

func formatBoolField(value bool) string {
	if value {
		return "YES"
	}
	return "NO"
}

func parseMetaUint32(value string) uint32 {
	return parseUint32Field(value)
}

func parseMetaInt(value string) int {
	return parseIntField(value)
}

func parseUint32Field(value string) uint32 {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0
	}
	parsed, err := strconv.ParseUint(value, 10, 32)
	if err != nil {
		return 0
	}
	return uint32(parsed)
}

func parseUint32ListField(value string) []uint32 {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	seen := make(map[uint32]bool)
	var result []uint32
	for _, part := range strings.FieldsFunc(value, func(r rune) bool {
		return r == ',' || r == ';' || r == ' ' || r == '\t'
	}) {
		id := parseUint32Field(part)
		if id == 0 || seen[id] {
			continue
		}
		seen[id] = true
		result = append(result, id)
	}
	return result
}

func parseUint64Field(value string) uint64 {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0
	}
	parsed, err := strconv.ParseUint(value, 10, 64)
	if err != nil {
		return 0
	}
	return parsed
}

func parseIntField(value string) int {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0
	}
	parsed, err := strconv.Atoi(value)
	if err != nil {
		return 0
	}
	return parsed
}

func parseOptionalInt16Field(value string, emptyValue int16) int16 {
	value = strings.TrimSpace(value)
	if value == "" {
		return emptyValue
	}
	parsed, err := strconv.ParseInt(value, 10, 16)
	if err != nil {
		return emptyValue
	}
	return int16(parsed)
}

func parseBoolField(value string) bool {
	switch strings.ToUpper(strings.TrimSpace(value)) {
	case "1", "T", "TRUE", "Y", "YES":
		return true
	default:
		return false
	}
}
