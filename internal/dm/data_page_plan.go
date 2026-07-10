package dm

import (
	"encoding/binary"
	"os"
)

const (
	dmPageKindOff              = 0x14
	dmPageNextRefOff           = 0x0E
	dmBTreeLeftmostChildOff    = 0x52
	dmPageKindBTreeLeaf        = 0x14
	dmPageKindBTreeRoot        = 0x15
	maxBTreeDescentDepth       = 64
	maxLeafChainWalkMultiplier = 2
	maxCachedDataFilePages     = 256
)

type dataFilePageCache struct {
	pageSize uint32
	refs     map[dataFileKey]dataFileRef
	sizes    map[dataFileKey]int64
	pages    map[dataPageRef][]byte
	pageFIFO []dataPageRef
}

func newDataFilePageCache(files []dataFileRef, pageSize uint32) *dataFilePageCache {
	cache := &dataFilePageCache{
		pageSize: pageSize,
		refs:     make(map[dataFileKey]dataFileRef, len(files)),
		sizes:    make(map[dataFileKey]int64),
		pages:    make(map[dataPageRef][]byte),
	}
	for _, file := range files {
		cache.refs[file.key] = file
	}
	return cache
}

func (c *dataFilePageCache) readPage(ref dataPageRef) ([]byte, bool) {
	if c == nil || c.pageSize == 0 {
		return nil, false
	}
	if page, ok := c.pages[ref]; ok {
		return page, true
	}
	pageSize := int(c.pageSize)
	if pageSize <= 0 || int64(ref.pageNo) >= int64(c.pageCount(ref.key)) {
		return nil, false
	}
	file, ok := c.refs[ref.key]
	if !ok || file.path == "" {
		return nil, false
	}
	f, err := os.Open(file.path)
	if err != nil {
		return nil, false
	}
	defer f.Close()
	page := make([]byte, pageSize)
	n, err := f.ReadAt(page, int64(ref.pageNo)*int64(pageSize))
	if err != nil || n != pageSize {
		return nil, false
	}
	if len(c.pages) >= maxCachedDataFilePages && len(c.pageFIFO) > 0 {
		oldest := c.pageFIFO[0]
		c.pageFIFO = c.pageFIFO[1:]
		delete(c.pages, oldest)
	}
	c.pages[ref] = page
	c.pageFIFO = append(c.pageFIFO, ref)
	return page, true
}

func (c *dataFilePageCache) pageCount(key dataFileKey) int {
	if c == nil || c.pageSize == 0 {
		return 0
	}
	size, ok := c.sizes[key]
	if !ok {
		file, ok := c.refs[key]
		if !ok || file.path == "" {
			return 0
		}
		info, err := os.Stat(file.path)
		if err != nil || info.IsDir() {
			return 0
		}
		size = info.Size()
		c.sizes[key] = size
	}
	if size < int64(c.pageSize) {
		return 0
	}
	return int(size / int64(c.pageSize))
}

func (c *dataFilePageCache) totalPageCount() int {
	if c == nil {
		return 0
	}
	total := 0
	for key := range c.refs {
		total += c.pageCount(key)
	}
	return total
}

func buildStoragePagePlan(storage indexDef, cache *dataFilePageCache) map[dataPageRef]bool {
	if cache == nil || storage.ID == 0 || storage.RootFile < 0 || storage.RootPage < 0 {
		return nil
	}
	root := dataPageRef{
		key: dataFileKey{
			groupID: uint32(storage.GroupID),
			fileID:  storage.RootFile,
		},
		pageNo: uint32(storage.RootPage),
	}
	rootPage, ok := cache.readPage(root)
	if !ok || !pageHeaderMatchesRef(rootPage, root) || dataPageStorageID(rootPage) != storage.ID {
		return nil
	}
	switch dataPageKind(rootPage) {
	case dmPageKindBTreeLeaf:
		return walkLeafChain(cache, root, storage.ID)
	case dmPageKindBTreeRoot:
		leaf, ok := descendLeftmostLeaf(cache, root, storage.ID)
		if !ok {
			return nil
		}
		return walkLeafChain(cache, leaf, storage.ID)
	default:
		return nil
	}
}

func descendLeftmostLeaf(cache *dataFilePageCache, start dataPageRef, storageID uint32) (dataPageRef, bool) {
	current := start
	seen := make(map[dataPageRef]bool)
	for depth := 0; depth < maxBTreeDescentDepth; depth++ {
		if seen[current] {
			return dataPageRef{}, false
		}
		seen[current] = true
		page, ok := cache.readPage(current)
		if !ok || !pageHeaderMatchesRef(page, current) || dataPageStorageID(page) != storageID {
			return dataPageRef{}, false
		}
		switch dataPageKind(page) {
		case dmPageKindBTreeLeaf:
			return current, true
		case dmPageKindBTreeRoot:
			childPage, ok := btreeLeftmostChildPage(page)
			if ok {
				childRef := dataPageRef{key: current.key, pageNo: childPage}
				if childPage, childOK := cache.readPage(childRef); childOK && pageHeaderMatchesRef(childPage, childRef) && dataPageStorageID(childPage) == storageID && isBTreePlanPageKind(dataPageKind(childPage)) {
					current = childRef
					continue
				}
			}
			nextRef, ok := btreeNextInternalPage(page, current.key.groupID)
			if !ok {
				return dataPageRef{}, false
			}
			current = nextRef
		default:
			return dataPageRef{}, false
		}
	}
	return dataPageRef{}, false
}

func isBTreePlanPageKind(kind uint32) bool {
	return kind == dmPageKindBTreeLeaf || kind == dmPageKindBTreeRoot
}

func btreeNextInternalPage(page []byte, groupID uint32) (dataPageRef, bool) {
	nextFileID, nextPageNo, ok := readDMPageRef(page, dmPageNextRefOff)
	if !ok {
		return dataPageRef{}, false
	}
	return dataPageRef{
		key: dataFileKey{
			groupID: groupID,
			fileID:  nextFileID,
		},
		pageNo: nextPageNo,
	}, true
}

func walkLeafChain(cache *dataFilePageCache, start dataPageRef, storageID uint32) map[dataPageRef]bool {
	planned := make(map[dataPageRef]bool)
	current := start
	maxSteps := cache.totalPageCount() * maxLeafChainWalkMultiplier
	if maxSteps <= 0 {
		maxSteps = 1
	}
	for steps := 0; steps < maxSteps; steps++ {
		if planned[current] {
			break
		}
		page, ok := cache.readPage(current)
		if !ok || !pageHeaderMatchesRef(page, current) || dataPageKind(page) != dmPageKindBTreeLeaf || dataPageStorageID(page) != storageID {
			break
		}
		planned[current] = true
		nextFileID, nextPageNo, ok := readDMPageRef(page, dmPageNextRefOff)
		if !ok {
			break
		}
		current = dataPageRef{
			key: dataFileKey{
				groupID: current.key.groupID,
				fileID:  nextFileID,
			},
			pageNo: nextPageNo,
		}
	}
	if len(planned) == 0 {
		return nil
	}
	return planned
}

func pageHeaderMatchesRef(page []byte, ref dataPageRef) bool {
	if len(page) < dataPageAssistIndexOff+4 {
		return false
	}
	if binary.LittleEndian.Uint16(page[0:]) != uint16(ref.key.groupID) {
		return false
	}
	if binary.LittleEndian.Uint16(page[2:]) != uint16(ref.key.fileID) {
		return false
	}
	return binary.LittleEndian.Uint32(page[4:]) == ref.pageNo
}

func dataPageKind(page []byte) uint32 {
	if len(page) < dmPageKindOff+4 {
		return 0
	}
	return binary.LittleEndian.Uint32(page[dmPageKindOff:])
}

func dataPageStorageID(page []byte) uint32 {
	if len(page) < dataPageAssistIndexOff+4 {
		return 0
	}
	return binary.LittleEndian.Uint32(page[dataPageAssistIndexOff:])
}

func btreeLeftmostChildPage(page []byte) (uint32, bool) {
	if len(page) < dmBTreeLeftmostChildOff+4 {
		return 0, false
	}
	pageNo := binary.LittleEndian.Uint32(page[dmBTreeLeftmostChildOff:])
	return pageNo, pageNo > 0
}

func readDMPageRef(page []byte, offset int) (int16, uint32, bool) {
	if offset < 0 || len(page) < offset+6 {
		return 0, 0, false
	}
	raw := page[offset : offset+6]
	allFF := true
	for _, b := range raw {
		if b != 0xFF {
			allFF = false
			break
		}
	}
	if allFF {
		return 0, 0, false
	}
	fileID := binary.LittleEndian.Uint16(raw[0:2])
	if fileID > uint16(^uint16(0)>>1) {
		return 0, 0, false
	}
	return int16(fileID), binary.LittleEndian.Uint32(raw[2:6]), true
}
