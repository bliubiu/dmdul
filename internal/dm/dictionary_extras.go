package dm

import (
	"bytes"
	"encoding/binary"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

type dictionaryTextDef struct {
	ID       uint32
	SeqNo    uint32
	Text     string
	Location ddlLocation
}

type dictionaryObjectPrivilegeDef struct {
	GranteeID uint32
	ObjectID  uint32
	PrivID    int32
	Privilege string
	Grantable string
	Location  ddlLocation
}

func parseDDLSynonymTarget(page []byte, start int, decoder textDecoder) (string, string) {
	limit := start + 128
	if limit > len(page) {
		limit = len(page)
	}
	for pos := start; pos+4 < limit; pos++ {
		ownerLen := int(binary.LittleEndian.Uint16(page[pos:]))
		if ownerLen <= 0 || ownerLen > 128 {
			continue
		}
		ownerStart := pos + 2
		ownerEnd := ownerStart + ownerLen
		if ownerEnd+2 > limit {
			continue
		}
		nameLen := int(binary.LittleEndian.Uint16(page[ownerEnd:]))
		if nameLen <= 0 || nameLen > 256 {
			continue
		}
		nameStart := ownerEnd + 2
		nameEnd := nameStart + nameLen
		if nameEnd > len(page) {
			continue
		}
		owner, ok := decoder.decode(page[ownerStart:ownerEnd])
		if !ok || !isSafeShortText(owner) {
			continue
		}
		name, ok := decoder.decode(page[nameStart:nameEnd])
		if !ok || !isSafeShortText(name) {
			continue
		}
		if !looksLikeIdentifierText(owner) || !looksLikeIdentifierText(name) {
			continue
		}
		return owner, name
	}
	return "", ""
}

func looksLikeIdentifierText(value string) bool {
	if strings.TrimSpace(value) == "" {
		return false
	}
	for _, ch := range value {
		if ch == '_' || ch == '$' || ch == '#' || ch == '"' || ch == '.' {
			continue
		}
		if ch >= '0' && ch <= '9' {
			continue
		}
		if ch >= 'A' && ch <= 'Z' {
			continue
		}
		if ch >= 'a' && ch <= 'z' {
			continue
		}
		if ch > 127 {
			continue
		}
		return false
	}
	return true
}

func scanDictionaryTexts(data []byte, pageSize uint32, decoder textDecoder) map[uint32]map[uint32]string {
	result := make(map[uint32]map[uint32]string)
	iterDictionaryRows(data, pageSize, func(page []byte, pageNo uint32, slotNo uint16, slotOff uint16) {
		row, ok := parseDDLTextRow(page, int(slotOff), pageNo, slotNo, slotOff, pageSize, decoder)
		if !ok {
			return
		}
		seqs := result[row.ID]
		if seqs == nil {
			seqs = make(map[uint32]string)
			result[row.ID] = seqs
		}
		if existing := seqs[row.SeqNo]; len(row.Text) > len(existing) {
			seqs[row.SeqNo] = row.Text
		}
	})
	return result
}

func parseDDLTextRow(page []byte, slotOff int, pageNo uint32, slotNo uint16, rawSlotOff uint16, pageSize uint32, decoder textDecoder) (dictionaryTextDef, bool) {
	for delta := 0; delta < 4; delta++ {
		base := slotOff + delta
		if base+32 > int(pageSize) || base+32 > len(page) {
			continue
		}
		rowLen := int(binary.LittleEndian.Uint16(page[base:]))
		if rowLen < 32 || rowLen > int(pageSize)-base {
			continue
		}
		id := binary.LittleEndian.Uint32(page[base+2:])
		seqNo := binary.LittleEndian.Uint32(page[base+6:])
		if id == 0 || seqNo > 32 {
			continue
		}
		textLen := int(binary.LittleEndian.Uint32(page[base+21:]))
		textStart := base + 25
		textEnd := textStart + textLen
		if textLen <= 0 || textLen > rowLen || textEnd > len(page) || textEnd > base+rowLen {
			continue
		}
		text, ok := decoder.decode(page[textStart:textEnd])
		if !ok || strings.TrimSpace(text) == "" {
			continue
		}
		rowAbs := uint64(pageNo)*uint64(pageSize) + uint64(base)
		return dictionaryTextDef{
			ID:       id,
			SeqNo:    seqNo,
			Text:     text,
			Location: ddlLocation{PageNo: pageNo, SlotNo: slotNo, SlotOffset: rawSlotOff, RowOffset: rowAbs},
		}, true
	}
	return dictionaryTextDef{}, false
}

func scanDictionaryViews(objects map[uint32]dictionaryObject, texts map[uint32]map[uint32]string, matcher ownerMatcher) []DictionaryView {
	var views []DictionaryView
	for _, obj := range objects {
		if obj.Type != "SCHOBJ" || obj.Subtype != "VIEW" || obj.Valid == "N" || !matcher.allowed(obj.Owner) {
			continue
		}
		seqs := texts[obj.ID]
		view := DictionaryView{
			ID:       obj.ID,
			Owner:    obj.Owner,
			Name:     obj.Name,
			Valid:    obj.Valid,
			SQL:      seqs[0],
			QuerySQL: seqs[1],
		}
		views = append(views, view)
	}
	sortDictionaryViews(views)
	return views
}

func scanDictionarySequences(objects map[uint32]dictionaryObject, texts map[uint32]map[uint32]string, matcher ownerMatcher) []DictionarySequence {
	var sequences []DictionarySequence
	for _, obj := range objects {
		if obj.Type != "SCHOBJ" || obj.Subtype != "SEQ" || obj.Valid == "N" || !matcher.allowed(obj.Owner) {
			continue
		}
		seqInfo := parseSequencePayload(obj.Payload)
		sequences = append(sequences, DictionarySequence{
			ID:          obj.ID,
			Owner:       obj.Owner,
			Name:        obj.Name,
			Valid:       obj.Valid,
			StartWith:   seqInfo.startWith,
			MinValue:    seqInfo.minValue,
			MaxValue:    seqInfo.maxValue,
			IncrementBy: obj.Info4,
			CycleFlag:   boolFlag(obj.Info1&0x01 != 0),
			OrderFlag:   boolFlag(obj.Info1&0xFF00 == 0x100),
			CacheSize:   seqInfo.cacheSize,
			SQL:         sequenceTextSQL(texts[obj.ID]),
		})
	}
	sortDictionarySequences(sequences)
	return sequences
}

func sequenceTextSQL(seqs map[uint32]string) string {
	for _, seqNo := range []uint32{0, 1} {
		if sql := strings.TrimSpace(seqs[seqNo]); strings.HasPrefix(strings.ToUpper(sql), "CREATE") {
			return sql
		}
	}
	return ""
}

type sequencePayloadInfo struct {
	startWith uint64
	minValue  uint64
	maxValue  uint64
	cacheSize uint32
}

func parseSequencePayload(payload []byte) sequencePayloadInfo {
	var result sequencePayloadInfo
	if len(payload) >= 16 {
		result.maxValue = binary.LittleEndian.Uint64(payload[0:])
		result.minValue = binary.LittleEndian.Uint64(payload[8:])
		if result.minValue > 0 {
			result.startWith = result.minValue
		}
	}
	if len(payload) >= 28 {
		cache := binary.LittleEndian.Uint32(payload[24:])
		if cache < 1_000_000 {
			result.cacheSize = cache
		}
	}
	return result
}

func scanDictionaryTriggers(objects map[uint32]dictionaryObject, texts map[uint32]map[uint32]string, rawTexts map[string]string, matcher ownerMatcher) []DictionaryTrigger {
	var triggers []DictionaryTrigger
	for _, obj := range objects {
		if obj.Type != "SCHOBJ" || obj.Subtype != "TRIG" || obj.Valid == "N" || !matcher.allowed(obj.Owner) {
			continue
		}
		sql := triggerTextSQL(texts[obj.ID])
		if raw := rawTexts[qualifiedObjectKey(obj.Owner, obj.Name)]; len(raw) > len(sql) {
			sql = raw
		}
		tableOwner, tableName := triggerTargetFromParent(objects, obj)
		if tableOwner == "" || tableName == "" {
			tableOwner, tableName = parseTriggerTargetTable(sql)
		}
		triggers = append(triggers, DictionaryTrigger{
			ID:         obj.ID,
			Owner:      obj.Owner,
			Name:       obj.Name,
			TableOwner: tableOwner,
			TableName:  tableName,
			Valid:      obj.Valid,
			SQL:        sql,
		})
	}
	sortDictionaryTriggers(triggers)
	return triggers
}

func triggerTextSQL(seqs map[uint32]string) string {
	for _, seqNo := range []uint32{0, 1} {
		if sql := strings.TrimSpace(seqs[seqNo]); strings.Contains(strings.ToUpper(sql), "TRIGGER") {
			return sql
		}
	}
	return ""
}

func triggerTargetFromParent(objects map[uint32]dictionaryObject, trigger dictionaryObject) (string, string) {
	if trigger.ParentID <= 0 {
		return "", ""
	}
	table, ok := objects[uint32(trigger.ParentID)]
	if !ok || table.Type != "SCHOBJ" || table.Subtype != "UTAB" {
		return "", ""
	}
	return table.Owner, table.Name
}

func scanDictionarySynonyms(objects map[uint32]dictionaryObject, matcher ownerMatcher) []DictionarySynonym {
	var synonyms []DictionarySynonym
	for _, obj := range objects {
		if obj.Subtype != "SYNOM" && obj.Type != "DSYNOM" {
			continue
		}
		if obj.TargetOwner == "" || obj.TargetName == "" {
			continue
		}
		if strings.HasPrefix(obj.Name, "##") || strings.HasPrefix(obj.TargetName, "##") {
			continue
		}
		owner := obj.Owner
		public := false
		if obj.Type == "DSYNOM" || owner == "" {
			owner = "PUBLIC"
			public = true
		}
		if owner == "SYS" || (public && strings.EqualFold(obj.TargetOwner, "SYS")) {
			continue
		}
		target, targetOK := dictionaryObjectByOwnerName(objects, obj.TargetOwner, obj.TargetName)
		if targetOK && !isTabPrivilegeTarget(target) {
			continue
		}
		if public && !targetOK {
			continue
		}
		if !matcher.allowed(owner) && !matcher.allowed(obj.TargetOwner) {
			continue
		}
		synonyms = append(synonyms, DictionarySynonym{
			ID:         obj.ID,
			Owner:      owner,
			Name:       obj.Name,
			TableOwner: obj.TargetOwner,
			TableName:  obj.TargetName,
			Public:     public,
		})
	}
	sortDictionarySynonyms(synonyms)
	return synonyms
}

func scanDictionaryTabPrivileges(data []byte, pageSize uint32, objects map[uint32]dictionaryObject, users map[uint32]dictionaryObject, roles map[uint32]dictionaryObject, matcher ownerMatcher, tableMatcher tableNameMatcher) []DictionaryTabPrivilege {
	granteeNames := make(map[uint32]string)
	for id, user := range users {
		granteeNames[id] = user.Name
	}
	for id, role := range roles {
		granteeNames[id] = role.Name
	}
	seen := make(map[string]bool)
	var privileges []DictionaryTabPrivilege
	iterDictionaryRows(data, pageSize, func(page []byte, pageNo uint32, slotNo uint16, slotOff uint16) {
		grant, ok := parseDDLObjectPrivilegeRow(page, int(slotOff), pageNo, slotNo, slotOff, pageSize)
		if !ok {
			return
		}
		target, ok := objects[grant.ObjectID]
		if !ok || !isTabPrivilegeTarget(target) {
			return
		}
		if isSystemCatalogOwner(target.Owner) {
			return
		}
		grantee := granteeNames[grant.GranteeID]
		if grantee == "" {
			return
		}
		if !matcher.allowed(target.Owner) && !matcher.allowed(grantee) {
			return
		}
		if !tableMatcher.allowed(target.Owner, target.Name) && !matcher.allowed(grantee) {
			return
		}
		item := DictionaryTabPrivilege{
			Grantee:    grantee,
			Owner:      target.Owner,
			ObjectName: target.Name,
			ObjectType: dictionaryPrivilegeObjectType(target),
			Privilege:  grant.Privilege,
			Grantable:  grant.Grantable,
		}
		key := strings.Join([]string{item.Grantee, item.Owner, item.ObjectName, item.Privilege, item.Grantable}, "\x00")
		if seen[key] {
			return
		}
		seen[key] = true
		privileges = append(privileges, item)
	})
	sortDictionaryTabPrivileges(privileges)
	return privileges
}

func dictionaryObjectByOwnerName(objects map[uint32]dictionaryObject, owner string, name string) (dictionaryObject, bool) {
	for _, obj := range objects {
		if strings.EqualFold(obj.Owner, owner) && strings.EqualFold(obj.Name, name) {
			return obj, true
		}
	}
	return dictionaryObject{}, false
}

func isSystemCatalogOwner(owner string) bool {
	switch strings.ToUpper(strings.TrimSpace(owner)) {
	case "SYS", "CTISYS", "SYSAUDITOR", "SYSSSO", "SYSJOB":
		return true
	default:
		return false
	}
}

func parseDDLObjectPrivilegeRow(page []byte, slotOff int, pageNo uint32, slotNo uint16, rawSlotOff uint16, pageSize uint32) (dictionaryObjectPrivilegeDef, bool) {
	for delta := 0; delta < 4; delta++ {
		base := slotOff + delta
		if base+44 > int(pageSize) || base+44 > len(page) {
			continue
		}
		rowLen := binary.LittleEndian.Uint16(page[base+1:])
		if rowLen != 44 {
			continue
		}
		colID := int32(binary.LittleEndian.Uint32(page[base+12:]))
		privID := int32(binary.LittleEndian.Uint32(page[base+16:]))
		grantor := int32(binary.LittleEndian.Uint32(page[base+20:]))
		grantable := page[base+24]
		if colID != -1 || privID == -1 || grantor == -1 {
			continue
		}
		if grantable != 'Y' && grantable != 'N' {
			continue
		}
		privilege := objectPrivilegeName(privID)
		if privilege == "" {
			continue
		}
		granteeID := binary.LittleEndian.Uint32(page[base+4:])
		objectID := binary.LittleEndian.Uint32(page[base+8:])
		if granteeID == 0 || objectID == 0 {
			continue
		}
		rowAbs := uint64(pageNo)*uint64(pageSize) + uint64(base)
		return dictionaryObjectPrivilegeDef{
			GranteeID: granteeID,
			ObjectID:  objectID,
			PrivID:    privID,
			Privilege: privilege,
			Grantable: string([]byte{grantable}),
			Location:  ddlLocation{PageNo: pageNo, SlotNo: slotNo, SlotOffset: rawSlotOff, RowOffset: rowAbs},
		}, true
	}
	return dictionaryObjectPrivilegeDef{}, false
}

func objectPrivilegeName(privID int32) string {
	switch privID {
	case 8192:
		return "SELECT"
	case 8193:
		return "INSERT"
	case 8194:
		return "DELETE"
	case 8195:
		return "UPDATE"
	default:
		return ""
	}
}

func isTabPrivilegeTarget(obj dictionaryObject) bool {
	if obj.Type != "SCHOBJ" {
		return false
	}
	if strings.HasPrefix(obj.Name, "##") {
		return false
	}
	switch obj.Subtype {
	case "UTAB", "VIEW", "SEQ":
		return obj.Owner != "" && obj.Name != ""
	default:
		return false
	}
}

func dictionaryPrivilegeObjectType(obj dictionaryObject) string {
	switch obj.Subtype {
	case "VIEW":
		return "VIEW"
	case "SEQ":
		return "SEQUENCE"
	default:
		return "TABLE"
	}
}

func sortDictionaryViews(views []DictionaryView) {
	sort.Slice(views, func(i, j int) bool {
		if views[i].Owner != views[j].Owner {
			return views[i].Owner < views[j].Owner
		}
		if views[i].Name != views[j].Name {
			return views[i].Name < views[j].Name
		}
		return views[i].ID < views[j].ID
	})
}

func sortDictionarySequences(sequences []DictionarySequence) {
	sort.Slice(sequences, func(i, j int) bool {
		if sequences[i].Owner != sequences[j].Owner {
			return sequences[i].Owner < sequences[j].Owner
		}
		if sequences[i].Name != sequences[j].Name {
			return sequences[i].Name < sequences[j].Name
		}
		return sequences[i].ID < sequences[j].ID
	})
}

func sortDictionaryTriggers(triggers []DictionaryTrigger) {
	sort.Slice(triggers, func(i, j int) bool {
		if triggers[i].Owner != triggers[j].Owner {
			return triggers[i].Owner < triggers[j].Owner
		}
		if triggers[i].Name != triggers[j].Name {
			return triggers[i].Name < triggers[j].Name
		}
		return triggers[i].ID < triggers[j].ID
	})
}

func sortDictionarySynonyms(synonyms []DictionarySynonym) {
	sort.Slice(synonyms, func(i, j int) bool {
		if synonyms[i].Owner != synonyms[j].Owner {
			return synonyms[i].Owner < synonyms[j].Owner
		}
		if synonyms[i].Name != synonyms[j].Name {
			return synonyms[i].Name < synonyms[j].Name
		}
		return synonyms[i].ID < synonyms[j].ID
	})
}

func sortDictionaryTabPrivileges(privileges []DictionaryTabPrivilege) {
	sort.Slice(privileges, func(i, j int) bool {
		if privileges[i].Grantee != privileges[j].Grantee {
			return privileges[i].Grantee < privileges[j].Grantee
		}
		if privileges[i].Owner != privileges[j].Owner {
			return privileges[i].Owner < privileges[j].Owner
		}
		if privileges[i].ObjectName != privileges[j].ObjectName {
			return privileges[i].ObjectName < privileges[j].ObjectName
		}
		if privileges[i].Privilege != privileges[j].Privilege {
			return privileges[i].Privilege < privileges[j].Privilege
		}
		return privileges[i].Grantable < privileges[j].Grantable
	})
}

func dictionarySequencesForDDL(dict *DictionaryInfo, matcher ownerMatcher) ([]DictionarySequence, bool) {
	if dict == nil || len(dict.Sequences) == 0 {
		return nil, false
	}
	sequences := make([]DictionarySequence, 0, len(dict.Sequences))
	for _, seq := range dict.Sequences {
		if strings.TrimSpace(seq.Owner) == "" || strings.TrimSpace(seq.Name) == "" || !matcher.allowed(seq.Owner) {
			continue
		}
		sequences = append(sequences, seq)
	}
	sortDictionarySequences(sequences)
	return sequences, true
}

func dictionaryTriggersForDDL(dict *DictionaryInfo, matcher ownerMatcher, tableMatcher tableNameMatcher) ([]DictionaryTrigger, bool) {
	if dict == nil || len(dict.Triggers) == 0 {
		return nil, false
	}
	triggers := make([]DictionaryTrigger, 0, len(dict.Triggers))
	for _, trigger := range dict.Triggers {
		if strings.TrimSpace(trigger.Owner) == "" || strings.TrimSpace(trigger.Name) == "" {
			continue
		}
		if !matcher.allowed(trigger.Owner) && !matcher.allowed(trigger.TableOwner) {
			continue
		}
		if tableMatcher.hasRules && !tableMatcher.all && !tableMatcher.allowed(trigger.TableOwner, trigger.TableName) {
			continue
		}
		triggers = append(triggers, trigger)
	}
	sortDictionaryTriggers(triggers)
	return triggers, true
}

func dictionaryViewsForDDL(dict *DictionaryInfo, matcher ownerMatcher) ([]DictionaryView, bool) {
	if dict == nil || len(dict.Views) == 0 {
		return nil, false
	}
	views := make([]DictionaryView, 0, len(dict.Views))
	for _, view := range dict.Views {
		if strings.TrimSpace(view.Owner) == "" || strings.TrimSpace(view.Name) == "" || !matcher.allowed(view.Owner) {
			continue
		}
		views = append(views, view)
	}
	sortDictionaryViews(views)
	return views, true
}

func dictionarySynonymsForDDL(dict *DictionaryInfo, matcher ownerMatcher) ([]DictionarySynonym, bool) {
	if dict == nil || len(dict.Synonyms) == 0 {
		return nil, false
	}
	synonyms := make([]DictionarySynonym, 0, len(dict.Synonyms))
	for _, syn := range dict.Synonyms {
		if strings.TrimSpace(syn.Owner) == "" || strings.TrimSpace(syn.Name) == "" || strings.TrimSpace(syn.TableOwner) == "" || strings.TrimSpace(syn.TableName) == "" {
			continue
		}
		if !matcher.allowed(syn.Owner) && !matcher.allowed(syn.TableOwner) {
			continue
		}
		synonyms = append(synonyms, syn)
	}
	sortDictionarySynonyms(synonyms)
	return synonyms, true
}

func dictionaryTabPrivilegesForDDL(dict *DictionaryInfo, matcher ownerMatcher, tableMatcher tableNameMatcher) ([]DictionaryTabPrivilege, bool) {
	if dict == nil || len(dict.TabPrivileges) == 0 {
		return nil, false
	}
	privileges := make([]DictionaryTabPrivilege, 0, len(dict.TabPrivileges))
	for _, priv := range dict.TabPrivileges {
		if strings.TrimSpace(priv.Grantee) == "" || strings.TrimSpace(priv.Owner) == "" || strings.TrimSpace(priv.ObjectName) == "" || strings.TrimSpace(priv.Privilege) == "" {
			continue
		}
		if !matcher.allowed(priv.Owner) && !matcher.allowed(priv.Grantee) {
			continue
		}
		if !tableMatcher.allowed(priv.Owner, priv.ObjectName) && !matcher.allowed(priv.Grantee) {
			continue
		}
		privileges = append(privileges, priv)
	}
	sortDictionaryTabPrivileges(privileges)
	return privileges, true
}

func boolFlag(value bool) string {
	if value {
		return "Y"
	}
	return "N"
}

func qualifiedObjectKey(owner string, name string) string {
	return strings.ToUpper(strings.TrimSpace(owner)) + "." + strings.ToUpper(strings.TrimSpace(name))
}

var createTriggerNamePattern = regexp.MustCompile(`(?is)CREATE\s+OR\s+REPLACE\s+TRIGGER\s+((?:"[^"]+"|[A-Za-z_][A-Za-z0-9_$#]*)\.)?("[^"]+"|[A-Za-z_][A-Za-z0-9_$#]*)`)
var triggerOnTablePattern = regexp.MustCompile(`(?is)\bON\s+((?:"[^"]+"|[A-Za-z_][A-Za-z0-9_$#]*)\.)?("[^"]+"|[A-Za-z_][A-Za-z0-9_$#]*)`)

func scanRawTriggerTexts(data []byte, decoder textDecoder) map[string]string {
	result := make(map[string]string)
	keyword := []byte("CREATE OR REPLACE TRIGGER")
	for searchFrom := 0; searchFrom < len(data); {
		rel := indexASCIIInsensitive(data[searchFrom:], keyword)
		if rel < 0 {
			break
		}
		start := searchFrom + rel
		end := rawTriggerEnd(data, start)
		if end <= start {
			searchFrom = start + len(keyword)
			continue
		}
		sql, ok := decoder.decode(data[start:end])
		if !ok {
			sql = string(data[start:end])
		}
		sql = strings.TrimSpace(sql)
		owner, name := parseCreateTriggerName(sql)
		if owner != "" && name != "" {
			key := qualifiedObjectKey(owner, name)
			if len(sql) > len(result[key]) {
				result[key] = sql
			}
		}
		searchFrom = end
	}
	return result
}

func indexASCIIInsensitive(data []byte, needle []byte) int {
	if len(needle) == 0 {
		return 0
	}
	if len(data) < len(needle) {
		return -1
	}
	first := toUpperASCII(needle[0])
	limit := len(data) - len(needle)
	for i := 0; i <= limit; i++ {
		if toUpperASCII(data[i]) != first {
			continue
		}
		matched := true
		for j := 1; j < len(needle); j++ {
			if toUpperASCII(data[i+j]) != toUpperASCII(needle[j]) {
				matched = false
				break
			}
		}
		if matched {
			return i
		}
	}
	return -1
}

func toUpperASCII(b byte) byte {
	if b >= 'a' && b <= 'z' {
		return b - ('a' - 'A')
	}
	return b
}

func rawTriggerEnd(data []byte, start int) int {
	maxEnd := start + 65536
	if maxEnd > len(data) {
		maxEnd = len(data)
	}
	window := data[start:maxEnd]
	upper := bytes.ToUpper(window)
	for search := 0; search < len(upper); {
		rel := bytes.Index(upper[search:], []byte("END;"))
		if rel < 0 {
			return 0
		}
		end := search + rel + len("END;")
		if rawSQLBoundary(window, end) {
			return start + end
		}
		search = end
	}
	return 0
}

func rawSQLBoundary(window []byte, end int) bool {
	for i := end; i < len(window) && i < end+64; i++ {
		b := window[i]
		if b == 0 {
			return true
		}
		if b == '/' || b == '\r' || b == '\n' || b == '\t' || b == ' ' {
			continue
		}
		if b < 32 || b >= 0x80 {
			return true
		}
		return false
	}
	return true
}

func parseCreateTriggerName(sql string) (string, string) {
	matches := createTriggerNamePattern.FindStringSubmatch(sql)
	if len(matches) == 0 {
		return "", ""
	}
	owner := strings.TrimSuffix(matches[1], ".")
	name := matches[2]
	return unquoteIdentifier(owner), unquoteIdentifier(name)
}

func parseTriggerTargetTable(sql string) (string, string) {
	matches := triggerOnTablePattern.FindStringSubmatch(sql)
	if len(matches) == 0 {
		return "", ""
	}
	owner := strings.TrimSuffix(matches[1], ".")
	name := matches[2]
	return unquoteIdentifier(owner), unquoteIdentifier(name)
}

func unquoteIdentifier(value string) string {
	value = strings.TrimSpace(value)
	if len(value) >= 2 && strings.HasPrefix(value, `"`) && strings.HasSuffix(value, `"`) {
		return strings.ReplaceAll(value[1:len(value)-1], `""`, `"`)
	}
	return value
}

func formatInt64Field(value int64) string {
	if value == 0 {
		return ""
	}
	return strconv.FormatInt(value, 10)
}

func parseInt64Field(value string) int64 {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0
	}
	parsed, err := strconv.ParseInt(value, 10, 64)
	if err != nil {
		return 0
	}
	return parsed
}
