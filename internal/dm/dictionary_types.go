package dm

import (
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

type DictionaryType struct {
	ID         uint32
	Owner      string
	Name       string
	ObjectType string
	SQL        string
}

var createTypePattern = regexp.MustCompile(`(?is)^\s*CREATE\s+(?:OR\s+REPLACE\s+)?TYPE\s+(BODY\s+)?((?:"(?:[^"]|"")+"|[\pL_][\pL\pN_$#]*)\s*\.\s*)?("(?:[^"]|"")+"|[\pL_][\pL\pN_$#]*)\s`)

func scanDictionaryTypes(objects map[uint32]dictionaryObject, texts map[uint32]map[uint32]string, matcher ownerMatcher) []DictionaryType {
	result := make([]DictionaryType, 0)
	for _, obj := range objects {
		if obj.Type != "SCHOBJ" || (obj.Subtype != "CLASS" && obj.Subtype != "TYPE") || isSystemCatalogOwner(obj.Owner) || !matcher.allowed(obj.Owner) {
			continue
		}
		for _, seq := range []uint32{0, 1} {
			sql := strings.TrimSpace(texts[obj.ID][seq])
			if seq == 1 && sql == "" {
				continue
			}
			typ := "TYPE"
			if seq == 1 {
				typ = "TYPE BODY"
			}
			// Native CLASS definitions are a different language. Do not relabel
			// them as TYPE; preserve only positively identified CREATE TYPE text.
			if sql != "" && !matchingTypeDefinition(sql, obj.Owner, obj.Name, typ) {
				continue
			}
			result = append(result, DictionaryType{ID: obj.ID, Owner: obj.Owner, Name: obj.Name, ObjectType: typ, SQL: sql})
		}
	}
	sortDictionaryTypes(result)
	return result
}

func matchingTypeDefinition(sql, owner, name, kind string) bool {
	m := createTypePattern.FindStringSubmatch(sql)
	if len(m) == 0 {
		return false
	}
	if (strings.TrimSpace(m[1]) != "") != (kind == "TYPE BODY") {
		return false
	}
	if !typeSQLIdentifierMatches(m[3], name) {
		return false
	}
	return m[2] == "" || typeSQLIdentifierMatches(strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(m[2]), ".")), owner)
}

func typeSQLIdentifierMatches(sql, name string) bool {
	if strings.HasPrefix(sql, `"`) {
		return strings.ReplaceAll(sql[1:len(sql)-1], `""`, `"`) == name
	}
	return strings.EqualFold(sql, name)
}

func sortDictionaryTypes(types []DictionaryType) {
	sort.SliceStable(types, func(i, j int) bool {
		if types[i].ObjectType != types[j].ObjectType {
			return types[i].ObjectType == "TYPE"
		}
		if types[i].ID != types[j].ID {
			return types[i].ID < types[j].ID
		}
		if types[i].Owner != types[j].Owner {
			return types[i].Owner < types[j].Owner
		}
		return types[i].Name < types[j].Name
	})
}

func dictionaryTypesForDDL(dict *DictionaryInfo, matcher ownerMatcher) ([]DictionaryType, bool) {
	if dict == nil || dict.Types == nil {
		return nil, false
	}
	result := make([]DictionaryType, 0)
	for _, typ := range dict.Types {
		if matcher.allowed(typ.Owner) && !isSystemCatalogOwner(typ.Owner) && (typ.ObjectType == "TYPE" || typ.ObjectType == "TYPE BODY") {
			result = append(result, typ)
		}
	}
	sortDictionaryTypes(result)
	return result, true
}

func renderTypes(out *strings.Builder, types []DictionaryType, body bool) {
	for _, typ := range types {
		if (typ.ObjectType == "TYPE BODY") != body {
			continue
		}
		if strings.TrimSpace(typ.SQL) == "" {
			fmt.Fprintf(out, "-- WARNING: missing %s source for %s.%s; inspect types.tsv\n", typ.ObjectType, quoteIdent(typ.Owner), quoteIdent(typ.Name))
			continue
		}
		out.WriteString(ensurePLSQLBlockTerminator(typeDDLSource(typ.SQL)))
		out.WriteString("\n\n")
	}
}

func typeDDLSource(sql string) string {
	sql = strings.TrimSpace(sql)
	if strings.HasSuffix(sql, "\n/") {
		prefix := strings.TrimSpace(strings.TrimSuffix(sql, "/"))
		if strings.HasSuffix(prefix, ";") {
			return prefix
		}
	}
	return sql
}

func writeDictionaryTypes(path string, types []DictionaryType) error {
	rows := make([][]string, 0, len(types))
	for _, typ := range types {
		rows = append(rows, []string{formatUint32Field(typ.ID), typ.Owner, typ.Name, typ.ObjectType, typ.SQL})
	}
	return writeTSV(path, []string{"type_id", "owner", "type_name", "object_type", "sql"}, rows)
}

func readDictionaryTypes(path string) ([]DictionaryType, error) {
	records, err := readTSV(path)
	if err != nil {
		return nil, err
	}
	result := make([]DictionaryType, 0)
	for i, rec := range records {
		if i == 0 && len(rec) > 0 && rec[0] == "type_id" {
			continue
		}
		if len(rec) != 5 {
			return nil, fmt.Errorf("types.tsv row %d: expected 5 fields", i+1)
		}
		if rec[1] == "" || rec[2] == "" || (rec[3] != "TYPE" && rec[3] != "TYPE BODY") {
			return nil, fmt.Errorf("types.tsv row %d: invalid type identity", i+1)
		}
		if rec[4] != "" && !matchingTypeDefinition(rec[4], rec[1], rec[2], rec[3]) {
			return nil, fmt.Errorf("types.tsv row %d: SQL type identity differs", i+1)
		}
		id, err := strconv.ParseUint(rec[0], 10, 32)
		if err != nil || id == 0 {
			return nil, fmt.Errorf("types.tsv row %d: invalid type id", i+1)
		}
		result = append(result, DictionaryType{ID: uint32(id), Owner: rec[1], Name: rec[2], ObjectType: rec[3], SQL: rec[4]})
	}
	sortDictionaryTypes(result)
	return result, nil
}
