package dm

import (
	"sort"
	"strings"
)

func applyDictionaryUserOverrides(dict *DictionaryInfo, users map[uint32]dictionaryObject) {
	if dict == nil || len(dict.Users) == 0 {
		return
	}
	nextSyntheticID := uint32(0xF0000000)
	for _, user := range dict.Users {
		name := strings.TrimSpace(user.Name)
		if name == "" {
			continue
		}
		id := user.ID
		if id == 0 {
			if existingID, ok := userIDByName(users, name); ok {
				id = existingID
			} else {
				for {
					if _, exists := users[nextSyntheticID]; !exists {
						id = nextSyntheticID
						nextSyntheticID++
						break
					}
					nextSyntheticID++
				}
			}
		}
		obj := users[id]
		if obj.ID == 0 {
			obj.ID = id
		}
		if obj.Type == "" {
			obj.Type = "UR"
		}
		if obj.Subtype == "" {
			obj.Subtype = "USER"
		}
		if obj.Valid == "" {
			obj.Valid = "Y"
		}
		obj.Name = name
		users[id] = obj
	}
}

func userIDByName(users map[uint32]dictionaryObject, name string) (uint32, bool) {
	for id, user := range users {
		if strings.EqualFold(user.Name, name) {
			return id, true
		}
	}
	return 0, false
}

func applyDictionaryTableOverrides(dict *DictionaryInfo, tables map[uint32]dictionaryObject, tablespaces map[uint32]string) map[uint32]DictionaryTable {
	result := make(map[uint32]DictionaryTable)
	if dict == nil || len(dict.Tables) == 0 {
		return result
	}
	for _, table := range dict.Tables {
		if table.ID == 0 || strings.TrimSpace(table.Owner) == "" || strings.TrimSpace(table.Name) == "" {
			continue
		}
		result[table.ID] = table
		obj := tables[table.ID]
		if obj.ID == 0 {
			obj.ID = table.ID
		}
		if obj.Type == "" {
			obj.Type = "SCHOBJ"
		}
		if obj.Subtype == "" {
			obj.Subtype = "UTAB"
		}
		if obj.Valid == "" {
			obj.Valid = "Y"
		}
		obj.Owner = table.Owner
		obj.Name = table.Name
		obj = applyDictionaryTableFlags(obj, table)
		tables[table.ID] = obj
		if tablespaces != nil && table.GroupID != 0 && strings.TrimSpace(table.Tablespace) != "" {
			tablespaces[table.GroupID] = table.Tablespace
		}
	}
	return result
}

func applyDictionaryTableFlags(obj dictionaryObject, table DictionaryTable) dictionaryObject {
	switch strings.ToUpper(strings.TrimSpace(table.Storage)) {
	case heapStorageOrg:
		obj.Info1 |= 0x10
	case defaultStorageOrg:
		obj.Info1 &^= tableIOTInfo1Mask
	}
	if table.Temporary {
		obj.Info3 |= tableTemporaryInfo3Flag
	} else {
		obj.Info3 &^= tableTemporaryInfo3Flag | tableTemporarySessionInfo3Flag
	}
	return obj
}

func applyDictionaryTableStorage(dictionaryTables map[uint32]DictionaryTable, tableStorage map[uint32]indexDef, tablespaces map[uint32]string) {
	for tableID, table := range dictionaryTables {
		groupID := table.GroupID
		if groupID == 0 && table.Tablespace != "" {
			groupID = tablespaceIDByName(tablespaces, table.Tablespace)
		}
		if groupID == 0 {
			continue
		}
		storage := tableStorage[tableID]
		storage.GroupID = uint16(groupID)
		storage.Flag |= 1
		storage.KeyNum = 0
		tableStorage[tableID] = storage
		if table.Tablespace != "" {
			tablespaces[groupID] = table.Tablespace
		}
	}
}

func tablespaceIDByName(tablespaces map[uint32]string, name string) uint32 {
	for id, existing := range tablespaces {
		if strings.EqualFold(existing, name) {
			return id
		}
	}
	return 0
}

func dictionaryColumnMaps(dict *DictionaryInfo, dictionaryTables map[uint32]DictionaryTable, tables map[uint32]dictionaryObject, ownerMatcher ownerMatcher, tableMatcher tableNameMatcher, excludeMatcher tableNameMatcher) (map[uint32][]columnDef, map[tableColKey]columnDef, int, bool) {
	if dict == nil || len(dict.Columns) == 0 || len(dictionaryTables) == 0 {
		return nil, nil, 0, false
	}
	tableIDsByName := dictionaryTableIDsByOwnerName(dictionaryTables)
	columnsByTable := make(map[uint32][]columnDef)
	columnsByTableColID := make(map[tableColKey]columnDef)
	count := 0
	for _, col := range dict.Columns {
		tableID := col.TableID
		if tableID == 0 {
			tableID = tableIDsByName[ownerTableKey{owner: col.TableOwner, table: col.TableName}]
		}
		if tableID == 0 {
			continue
		}
		if _, ok := dictionaryTables[tableID]; !ok {
			continue
		}
		table, ok := tables[tableID]
		if !ok || !ownerMatcher.allowed(table.Owner) || excludeMatcher.allowed(table.Owner, table.Name) {
			continue
		}
		def := columnDef{
			TableID:  tableID,
			ColID:    col.ColID,
			Name:     col.Name,
			DataType: col.DataType,
			Length:   col.Length,
			Scale:    col.Scale,
			Nullable: col.Nullable,
			Default:  col.Default,
		}
		columnsByTableColID[tableColKey{tableID: tableID, colID: col.ColID}] = def
		if !tableMatcher.allowed(table.Owner, table.Name) {
			continue
		}
		columnsByTable[tableID] = append(columnsByTable[tableID], def)
		count++
	}
	for tableID := range columnsByTable {
		sort.Slice(columnsByTable[tableID], func(i, j int) bool {
			if columnsByTable[tableID][i].ColID == columnsByTable[tableID][j].ColID {
				return columnsByTable[tableID][i].Name < columnsByTable[tableID][j].Name
			}
			return columnsByTable[tableID][i].ColID < columnsByTable[tableID][j].ColID
		})
	}
	return columnsByTable, columnsByTableColID, count, true
}

func dictionaryTableIDsByOwnerName(tables map[uint32]DictionaryTable) map[ownerTableKey]uint32 {
	result := make(map[ownerTableKey]uint32)
	var ids []uint32
	for id := range tables {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	for _, id := range ids {
		table := tables[id]
		key := ownerTableKey{owner: table.Owner, table: table.Name}
		if _, exists := result[key]; !exists {
			result[key] = id
		}
	}
	return result
}
