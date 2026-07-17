package dm

import (
	"encoding/binary"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadStandardBootstrapCatalogUsesTwoStagePlans(t *testing.T) {
	const pageSize = 8192
	const pageCount = 320
	raw := make([]byte, pageSize*pageCount)
	binary.LittleEndian.PutUint32(raw[systemExtentSizeOffset:], 16)
	binary.LittleEndian.PutUint32(raw[systemPageSizeOffset:], pageSize)
	binary.LittleEndian.PutUint32(raw[systemPageCountOffset:], pageCount)
	raw[pageSize*int(systemControlPage4No)+systemUnicodeFlagOffset] = 1
	binary.LittleEndian.PutUint32(raw[bootstrapSYSObjectsRootOffset:], 16)
	binary.LittleEndian.PutUint32(raw[bootstrapSYSIndexesRootOffset:], 288)

	putBootstrapRootAndLeaf(raw, pageSize, 16, 17, 33554540)
	putBootstrapRootAndLeaf(raw, pageSize, 288, 289, 33554434)

	objectRows := []dictionaryObject{
		{ID: 0, Name: "SYSOBJECTS", Type: "SCHOBJ", Subtype: "STAB", Valid: "Y"},
		{ID: 2, Name: "SYSCOLUMNS", Type: "SCHOBJ", Subtype: "STAB", Valid: "Y"},
		{ID: 5, Name: "SYSTEXTS", Type: "SCHOBJ", Subtype: "STAB", Valid: "Y"},
		{ID: 6, Name: "SYSGRANTS", Type: "SCHOBJ", Subtype: "STAB", Valid: "Y"},
		{ID: 27, Name: "SYSOBJINFOS", Type: "SCHOBJ", Subtype: "STAB", Valid: "Y"},
		{ID: 19, Name: "SYSHPARTTABLEINFO", Type: "SCHOBJ", Subtype: "STAB", Valid: "Y"},
		{ID: 2002, ParentID: 2, Name: "SYSINDEXCOLUMNS", Type: "TABOBJ", Subtype: "INDEX", Valid: "Y"},
		{ID: 2005, ParentID: 5, Name: "SYSINDEXSYSTEXTS", Type: "TABOBJ", Subtype: "INDEX", Valid: "Y"},
		{ID: 2006, ParentID: 6, Name: "SYSINDEXSYSGRANTS", Type: "TABOBJ", Subtype: "INDEX", Valid: "Y"},
		{ID: 2027, ParentID: 27, Name: "SYSINDEXSYSOBJINFOS", Type: "TABOBJ", Subtype: "INDEX", Valid: "Y"},
		{ID: 2019, ParentID: 19, Name: "SYSINDEXSYSHPARTTABLEINFO", Type: "TABOBJ", Subtype: "INDEX", Valid: "Y"},
	}
	putBootstrapObjectRows(raw[17*pageSize:18*pageSize], pageSize, objectRows)

	indexRows := []indexDef{
		{ID: 2002, GroupID: 0, RootFile: 0, RootPage: 20, Type: "BT", Flag: 5},
		{ID: 2005, GroupID: 0, RootFile: 0, RootPage: 22, Type: "BT", Flag: 5},
		{ID: 2006, GroupID: 0, RootFile: 0, RootPage: 24, Type: "BT", Flag: 5},
		{ID: 2027, GroupID: 0, RootFile: 0, RootPage: 26, Type: "BT", Flag: 5},
		{ID: 2019, GroupID: 0, RootFile: 0, RootPage: 28, Type: "BT", Flag: 5},
	}
	putBootstrapIndexRows(raw[289*pageSize:290*pageSize], pageSize, indexRows)
	for root := 20; root <= 28; root += 2 {
		putBootstrapRootAndLeaf(raw, pageSize, uint32(root), uint32(root+1), uint32(1982+root))
	}
	// Match each stage-two root storage id to its SYSINDEXES row.
	for i, root := range []int{20, 22, 24, 26, 28} {
		storageID := indexRows[i].ID
		binary.LittleEndian.PutUint32(raw[root*pageSize+dataPageAssistIndexOff:], storageID)
		binary.LittleEndian.PutUint32(raw[(root+1)*pageSize+dataPageAssistIndexOff:], storageID)
	}

	path := filepath.Join(t.TempDir(), "SYSTEM.DBF")
	if err := os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	stream, err := openSystemPageStream(path)
	if err != nil {
		t.Fatal(err)
	}
	defer stream.close()

	catalog, reason := loadStandardBootstrapCatalog(stream, textDecoder{preferred: "utf-8"}, nil)
	if reason != "" {
		t.Fatalf("standard bootstrap fell back: %s", reason)
	}
	if len(catalog.objects) != len(objectRows) || len(catalog.indexes) != len(indexRows) {
		t.Fatalf("objects=%d indexes=%d", len(catalog.objects), len(catalog.indexes))
	}
	for _, name := range bootstrapStage2Tables {
		if len(catalog.plans[name]) != 1 {
			t.Fatalf("stage-two plan %s has %d pages", name, len(catalog.plans[name]))
		}
	}
}

func TestLoadDictionaryFallsBackWhenBootstrapAnchorIsInvalid(t *testing.T) {
	const pageSize = 8192
	raw := make([]byte, pageSize*5)
	binary.LittleEndian.PutUint32(raw[systemExtentSizeOffset:], 16)
	binary.LittleEndian.PutUint32(raw[systemPageSizeOffset:], pageSize)
	binary.LittleEndian.PutUint32(raw[systemPageCountOffset:], 5)
	raw[pageSize*int(systemControlPage4No)+systemUnicodeFlagOffset] = 1
	path := filepath.Join(t.TempDir(), "SYSTEM.DBF")
	if err := os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	var diagnostics []BootstrapDiagnostic
	dict, err := LoadDictionary(DictionaryOptions{
		SystemPath:  path,
		OwnerFilter: "all",
		Charset:     "utf-8",
		Diagnostic:  func(diag BootstrapDiagnostic) { diagnostics = append(diagnostics, diag) },
	})
	if err != nil {
		t.Fatal(err)
	}
	if !dict.BootstrapFallback || dict.BootstrapMode != "stream-scan-fallback" {
		t.Fatalf("mode=%s fallback=%v", dict.BootstrapMode, dict.BootstrapFallback)
	}
	found := false
	for _, diag := range diagnostics {
		if diag.Phase == "fallback" && strings.Contains(diag.Reason, "root page") {
			found = true
		}
	}
	if !found {
		t.Fatalf("fallback diagnostic not found: %+v", diagnostics)
	}
}

func putBootstrapRootAndLeaf(raw []byte, pageSize int, rootPage uint32, leafPage uint32, storageID uint32) {
	root := raw[int(rootPage)*pageSize : int(rootPage+1)*pageSize]
	putTestDMPageHeader(root, 0, 0, rootPage, dmPageKindBTreeRoot, storageID)
	binary.LittleEndian.PutUint32(root[dmBTreeLeftmostChildOff:], leafPage)
	putTestDMNullPageRef(root, dmPageNextRefOff)
	leaf := raw[int(leafPage)*pageSize : int(leafPage+1)*pageSize]
	putTestDMPageHeader(leaf, 0, 0, leafPage, dmPageKindBTreeLeaf, storageID)
	putTestDMNullPageRef(leaf, dmPageNextRefOff)
}

func putBootstrapObjectRows(page []byte, pageSize int, rows []dictionaryObject) {
	offsets := make([]uint16, 0, len(rows))
	for i, obj := range rows {
		off := 0x62 + i*112
		offsets = append(offsets, uint16(off))
		binary.LittleEndian.PutUint16(page[off+1:], 108)
		binary.LittleEndian.PutUint32(page[off+7:], obj.ID)
		binary.LittleEndian.PutUint32(page[off+0x0B:], obj.SchemaID)
		binary.LittleEndian.PutUint32(page[off+0x0F:], uint32(obj.ParentID))
		page[off+0x3F] = obj.Valid[0]
		next := putBootstrapShortString(page, off+0x40, obj.Name)
		next = putBootstrapShortString(page, next, obj.Type)
		putBootstrapShortString(page, next, obj.Subtype)
	}
	putBootstrapSlots(page, pageSize, offsets)
}

func putBootstrapIndexRows(page []byte, pageSize int, rows []indexDef) {
	offsets := make([]uint16, 0, len(rows))
	for i, idx := range rows {
		off := 0x62 + i*48
		offsets = append(offsets, uint16(off))
		binary.LittleEndian.PutUint32(page[off:], idx.ID)
		page[off+4] = 'N'
		binary.LittleEndian.PutUint16(page[off+5:], idx.GroupID)
		binary.LittleEndian.PutUint16(page[off+7:], uint16(idx.RootFile))
		binary.LittleEndian.PutUint32(page[off+9:], uint32(idx.RootPage))
		copy(page[off+13:], idx.Type)
		binary.LittleEndian.PutUint32(page[off+19:], idx.Flag)
		page[off+31] = 0x80
	}
	putBootstrapSlots(page, pageSize, offsets)
}

func putBootstrapShortString(page []byte, off int, value string) int {
	page[off] = byte(0x80 + len(value))
	copy(page[off+1:], value)
	return off + 1 + len(value)
}

func putBootstrapSlots(page []byte, pageSize int, offsets []uint16) {
	binary.LittleEndian.PutUint16(page[sysObjectsSlotCountOff:], uint16(len(offsets)))
	binary.LittleEndian.PutUint16(page[dataPageRecordCountOff:], uint16(len(offsets)))
	start := pageSize - pageSlotTrailerLen - len(offsets)*2
	for i, off := range offsets {
		binary.LittleEndian.PutUint16(page[start+i*2:], off)
	}
}
