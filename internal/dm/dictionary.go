package dm

import (
	"fmt"
	"os"
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
	SystemPath       string
	ControlPath      string
	ExtentSize       uint32
	ExtentSizeSource string
	PageSize         uint32
	PageCount        uint32
	Charset          string
	CharsetSource    string
	ObjectCount      int
	UserCount        int
	TableCount       int
	ColumnCount      int
	Users            []DictionaryUser
	Tables           []DictionaryTable
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
	Temporary   bool
	Storage     string
	Partitioned bool
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
	for _, obj := range objects {
		switch {
		case obj.Type == "SCHOBJ" && obj.Subtype == "UTAB":
			tables[obj.ID] = obj
		case obj.Type == "TABOBJ" && obj.Subtype == "INDEX":
			indexObjects[obj.ID] = obj
		case obj.Type == "UR" && obj.Subtype == "USER" && isRealURObject(obj) && ownerMatcher.allowed(obj.Name):
			users[obj.ID] = DictionaryUser{ID: obj.ID, Name: obj.Name}
		}
	}

	columnsByTable := make(map[uint32]int)
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
		columnCount++
	})

	indexes := make(map[uint32]indexDef)
	iterDictionaryRows(data, pageSize, func(page []byte, pageNo uint32, slotNo uint16, slotOff uint16) {
		idx, ok := parseDDLIndexRow(page, int(slotOff), pageSize)
		if ok {
			indexes[idx.ID] = idx
		}
	})

	tablespaces := loadTablespaceNames(opts.ControlPath, opts.ControlDULPath)
	tableStorage := tableStorageByID(tables, indexObjects, indexes, tablespaces)
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
		})
		if _, ok := userNamesByName[strings.ToUpper(table.Owner)]; !ok {
			userNamesByName[strings.ToUpper(table.Owner)] = DictionaryUser{Name: table.Owner}
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

	return &DictionaryInfo{
		SystemPath:       opts.SystemPath,
		ControlPath:      opts.ControlPath,
		ExtentSize:       extentSize,
		ExtentSizeSource: extentSizeSource,
		PageSize:         pageSize,
		PageCount:        pageCount,
		Charset:          charsetDisplay,
		CharsetSource:    charsetSource,
		ObjectCount:      len(objects),
		UserCount:        len(userList),
		TableCount:       len(tableList),
		ColumnCount:      columnCount,
		Users:            userList,
		Tables:           tableList,
	}, nil
}
