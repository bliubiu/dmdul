package dm

import (
	"encoding/binary"
	"sort"
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
	case "UTAB", "VIEW":
		return obj.Owner != "" && obj.Name != ""
	default:
		return false
	}
}

func dictionaryPrivilegeObjectType(obj dictionaryObject) string {
	if obj.Subtype == "VIEW" {
		return "VIEW"
	}
	return "TABLE"
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
