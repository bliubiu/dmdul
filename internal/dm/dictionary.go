package dm

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type DictionaryOptions struct {
	SystemPath     string
	ControlPath    string
	ControlDULPath string
	OwnerFilter    string
	Charset        string
}

type DictionaryInfo struct {
	SystemPath        string
	ControlPath       string
	Source            string
	DictionaryDir     string
	ExtentSize        uint32
	ExtentSizeSource  string
	PageSize          uint32
	PageCount         uint32
	Charset           string
	CharsetSource     string
	ObjectCount       int
	UserCount         int
	TableCount        int
	ColumnCount       int
	ViewCount         int
	SequenceCount     int
	RoutineCount      int
	TriggerCount      int
	SynonymCount      int
	TabPrivilegeCount int
	Users             []DictionaryUser
	Tables            []DictionaryTable
	Columns           []DictionaryColumn
	Views             []DictionaryView
	Sequences         []DictionarySequence
	Routines          []DictionaryRoutine
	Triggers          []DictionaryTrigger
	Synonyms          []DictionarySynonym
	TabPrivileges     []DictionaryTabPrivilege
}

type DictionaryUser struct {
	ID   uint32
	Name string
}

type DictionaryTable struct {
	ID          uint32
	Owner       string
	Name        string
	ColumnCount int
	Tablespace  string
	GroupID     uint32
	HeaderFile  int16
	HeaderBlock uint32
	Bytes       uint64
	Blocks      uint32
	Extents     uint32
	Temporary   bool
	Storage     string
	Partitioned bool
	StorageID   uint32
	RootFile    int16
	RootPage    uint32
	AssistIDs   []uint32
}

type DictionaryColumn struct {
	TableID    uint32
	TableOwner string
	TableName  string
	ColID      uint16
	Name       string
	DataType   string
	Length     uint32
	Scale      int16
	Nullable   string
	Default    string
}

type DictionaryView struct {
	ID       uint32
	Owner    string
	Name     string
	Valid    string
	SQL      string
	QuerySQL string
}

type DictionarySequence struct {
	ID          uint32
	Owner       string
	Name        string
	Valid       string
	StartWith   uint64
	MinValue    uint64
	MaxValue    uint64
	IncrementBy int64
	CycleFlag   string
	OrderFlag   string
	CacheSize   uint32
	SQL         string
}

type DictionaryRoutine struct {
	ID         uint32
	Owner      string
	Name       string
	ObjectType string
	SeqNo      uint32
	Valid      string
	SQL        string
}

type DictionaryTrigger struct {
	ID         uint32
	Owner      string
	Name       string
	TableOwner string
	TableName  string
	Valid      string
	SQL        string
}

type DictionarySynonym struct {
	ID         uint32
	Owner      string
	Name       string
	TableOwner string
	TableName  string
	Public     bool
}

type DictionaryTabPrivilege struct {
	Grantee    string
	Owner      string
	ObjectName string
	ObjectType string
	Privilege  string
	Grantable  string
}

func LoadDictionary(opts DictionaryOptions) (*DictionaryInfo, error) {
	if opts.SystemPath == "" {
		return nil, fmt.Errorf("bootstrap requires SYSTEM.DBF path")
	}
	data, err := os.ReadFile(opts.SystemPath)
	if err != nil {
		return nil, fmt.Errorf("read SYSTEM.DBF: %w", err)
	}
	if len(data) < systemHeaderReadSize {
		return nil, fmt.Errorf("SYSTEM.DBF is too small")
	}

	pageSize, _ := detectSystemPageSize(data[:systemHeaderReadSize], int64(len(data)))
	if pageSize == 0 {
		return nil, fmt.Errorf("cannot detect SYSTEM.DBF page size")
	}
	pageCount, _ := detectSystemPageCount(data[:systemHeaderReadSize], int64(len(data)), pageSize)
	extentSize, extentSizeSource := detectSystemExtentSize(data[:systemHeaderReadSize])
	restoreSystemPages(data, pageSize)

	preferredCharset := strings.ToLower(strings.TrimSpace(opts.Charset))
	charsetDisplay := defaultIfEmpty(opts.Charset, "auto")
	charsetSource := "decoder setting"
	if charset, ok := detectSystemCharsetFromData(data, pageSize); ok {
		charsetDisplay = charset.DisplayName
		charsetSource = charset.Source
		if preferredCharset == "" || preferredCharset == "auto" {
			preferredCharset = charset.DecoderName
		}
	}
	decoder := textDecoder{preferred: preferredCharset}
	ownerMatcher := newOwnerMatcher(opts.OwnerFilter)

	objects := scanDictionaryObjects(data, pageSize, decoder)
	schemaNames := schemaNamesFromDictionaryObjects(objects)
	for id, obj := range objects {
		obj.Owner = resolveSchemaName(obj.SchemaID, schemaNames)
		objects[id] = obj
	}

	tables := make(map[uint32]dictionaryObject)
	indexObjects := make(map[uint32]dictionaryObject)
	users := make(map[uint32]DictionaryUser)
	userObjects := make(map[uint32]dictionaryObject)
	roleObjects := make(map[uint32]dictionaryObject)
	for _, obj := range objects {
		switch {
		case obj.Type == "SCHOBJ" && obj.Subtype == "UTAB":
			tables[obj.ID] = obj
		case obj.Type == "TABOBJ" && obj.Subtype == "INDEX":
			indexObjects[obj.ID] = obj
		case obj.Type == "UR" && obj.Subtype == "USER" && isRealURObject(obj):
			userObjects[obj.ID] = obj
			if ownerMatcher.allowed(obj.Name) {
				users[obj.ID] = DictionaryUser{ID: obj.ID, Name: obj.Name}
			}
		case obj.Type == "UR" && obj.Subtype == "ROLE" && isRealURObject(obj):
			roleObjects[obj.ID] = obj
		}
	}

	columnsByTable := make(map[uint32]int)
	var columnList []DictionaryColumn
	columnCount := 0
	iterDictionaryRows(data, pageSize, func(page []byte, pageNo uint32, slotNo uint16, slotOff uint16) {
		col, ok := parseDDLColumnRow(page, int(slotOff), pageNo, slotNo, slotOff, pageSize, decoder)
		if !ok {
			return
		}
		table, ok := tables[col.TableID]
		if !ok || !ownerMatcher.allowed(table.Owner) {
			return
		}
		columnsByTable[col.TableID]++
		columnList = append(columnList, DictionaryColumn{
			TableID:    col.TableID,
			TableOwner: table.Owner,
			TableName:  table.Name,
			ColID:      col.ColID,
			Name:       col.Name,
			DataType:   col.DataType,
			Length:     col.Length,
			Scale:      col.Scale,
			Nullable:   col.Nullable,
			Default:    col.Default,
		})
		columnCount++
	})

	indexes := make(map[uint32]indexDef)
	iterDictionaryRows(data, pageSize, func(page []byte, pageNo uint32, slotNo uint16, slotOff uint16) {
		idx, ok := parseDDLIndexRow(page, int(slotOff), pageSize)
		if ok {
			indexes[idx.ID] = idx
		}
	})
	texts := scanDictionaryTexts(data, pageSize, decoder)
	viewList := scanDictionaryViews(objects, texts, ownerMatcher)
	sequenceList := scanDictionarySequences(objects, texts, ownerMatcher)
	routineList := scanDictionaryRoutines(objects, texts, scanRawRoutineTexts(data, decoder), ownerMatcher)
	triggerList := scanDictionaryTriggers(objects, texts, scanRawTriggerTexts(data, decoder), ownerMatcher)
	synonymList := scanDictionarySynonyms(objects, ownerMatcher)
	tabPrivilegeList := scanDictionaryTabPrivileges(data, pageSize, objects, userObjects, roleObjects, ownerMatcher, newTableNameMatcher("all"))

	tablespaces := loadTablespaceNames(opts.ControlPath, opts.ControlDULPath)
	tableStorage := tableStorageByID(tables, indexObjects, indexes, tablespaces)
	assistByParentID := assistIndexesByParentID(tables, indexObjects, indexes)
	partitionsByTable := scanPartitionsByTable(data, pageSize, decoder, tables, ownerMatcher)
	var tableList []DictionaryTable
	userNamesByName := make(map[string]DictionaryUser)
	for _, user := range users {
		userNamesByName[strings.ToUpper(user.Name)] = user
	}
	for id, table := range tables {
		if !ownerMatcher.allowed(table.Owner) || columnsByTable[id] == 0 {
			continue
		}
		var groupID uint32
		var tablespace string
		if storage, ok := tableStorage[id]; ok {
			groupID = uint32(storage.GroupID)
			tablespace = tablespaces[groupID]
		}
		storageID, rootFile, rootPage, assistIDs := dictionaryTableStorageSnapshot(id, tableStorage, assistByParentID)
		tableList = append(tableList, DictionaryTable{
			ID:          table.ID,
			Owner:       table.Owner,
			Name:        table.Name,
			ColumnCount: columnsByTable[id],
			Tablespace:  tablespace,
			GroupID:     groupID,
			Temporary:   table.isTemporaryTable(),
			Storage:     table.tableStorageOrganization(),
			Partitioned: len(partitionsByTable[id]) > 0,
			StorageID:   storageID,
			RootFile:    rootFile,
			RootPage:    rootPage,
			AssistIDs:   assistIDs,
		})
		if _, ok := userNamesByName[strings.ToUpper(table.Owner)]; !ok {
			userNamesByName[strings.ToUpper(table.Owner)] = DictionaryUser{Name: table.Owner}
		}
	}
	segments := inferDictionaryTableSegments(opts.ControlPath, opts.ControlDULPath, filepath.Dir(opts.SystemPath), pageSize, extentSize, tables, indexObjects, indexes, partitionsByTable, tableList)
	for i := range tableList {
		if seg, ok := segments[tableList[i].ID]; ok {
			tableList[i].HeaderFile = seg.fileID
			tableList[i].HeaderBlock = seg.headerPage
			tableList[i].Bytes = seg.bytes
			tableList[i].Blocks = seg.blocks
			tableList[i].Extents = seg.extents
			if seg.tablespaceID != 0 {
				tableList[i].GroupID = seg.tablespaceID
				if name := tablespaces[seg.tablespaceID]; name != "" {
					tableList[i].Tablespace = name
				}
			}
		}
	}

	userList := make([]DictionaryUser, 0, len(userNamesByName))
	for _, user := range userNamesByName {
		userList = append(userList, user)
	}
	sort.Slice(userList, func(i, j int) bool {
		return userList[i].Name < userList[j].Name
	})
	sort.Slice(tableList, func(i, j int) bool {
		if tableList[i].Owner == tableList[j].Owner {
			if tableList[i].Name == tableList[j].Name {
				return tableList[i].ID < tableList[j].ID
			}
			return tableList[i].Name < tableList[j].Name
		}
		return tableList[i].Owner < tableList[j].Owner
	})
	sort.Slice(columnList, func(i, j int) bool {
		if columnList[i].TableOwner != columnList[j].TableOwner {
			return columnList[i].TableOwner < columnList[j].TableOwner
		}
		if columnList[i].TableName != columnList[j].TableName {
			return columnList[i].TableName < columnList[j].TableName
		}
		if columnList[i].ColID != columnList[j].ColID {
			return columnList[i].ColID < columnList[j].ColID
		}
		return columnList[i].Name < columnList[j].Name
	})

	return &DictionaryInfo{
		SystemPath:        opts.SystemPath,
		ControlPath:       opts.ControlPath,
		Source:            "SYSTEM.DBF",
		ExtentSize:        extentSize,
		ExtentSizeSource:  extentSizeSource,
		PageSize:          pageSize,
		PageCount:         pageCount,
		Charset:           charsetDisplay,
		CharsetSource:     charsetSource,
		ObjectCount:       len(objects),
		UserCount:         len(userList),
		TableCount:        len(tableList),
		ColumnCount:       columnCount,
		ViewCount:         len(viewList),
		SequenceCount:     len(sequenceList),
		RoutineCount:      len(routineList),
		TriggerCount:      len(triggerList),
		SynonymCount:      len(synonymList),
		TabPrivilegeCount: len(tabPrivilegeList),
		Users:             userList,
		Tables:            tableList,
		Columns:           columnList,
		Views:             viewList,
		Sequences:         sequenceList,
		Routines:          routineList,
		Triggers:          triggerList,
		Synonyms:          synonymList,
		TabPrivileges:     tabPrivilegeList,
	}, nil
}

func dictionaryTableStorageSnapshot(tableID uint32, tableStorage map[uint32]indexDef, assistByParentID map[uint32][]indexDef) (uint32, int16, uint32, []uint32) {
	var primary indexDef
	if storage, ok := tableStorage[tableID]; ok {
		primary = storage
	} else if assists := assistByParentID[tableID]; len(assists) > 0 {
		primary = assists[0]
	}
	rootFile := int16(-1)
	if primary.RootFile >= 0 {
		rootFile = primary.RootFile
	}
	var rootPage uint32
	if primary.RootPage >= 0 {
		rootPage = uint32(primary.RootPage)
	}
	seen := make(map[uint32]bool)
	var assistIDs []uint32
	add := func(id uint32) {
		if id == 0 || seen[id] {
			return
		}
		seen[id] = true
		assistIDs = append(assistIDs, id)
	}
	add(primary.ID)
	for _, storage := range assistByParentID[tableID] {
		add(storage.ID)
	}
	add(tableDataAssistID(tableID))
	return primary.ID, rootFile, rootPage, assistIDs
}
