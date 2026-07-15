package dm

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"testing"
)

func TestVerifyDMPageCheckModes(t *testing.T) {
	base := make([]byte, 8192)
	for i := range base {
		base[i] = byte((i*31 + 7) % 251)
	}
	clear(base[dmPageChecksumOffset : dmPageChecksumOffset+dmPageChecksumSize])

	tests := []struct {
		name       string
		mode       uint32
		hashName   string
		storedCRC  uint32
		storedHash string
	}{
		{name: "disabled", mode: 0},
		{name: "crc32", mode: 1, storedCRC: 0x62E5B802},
		{name: "sha256", mode: 2, hashName: "SHA256", storedHash: "e2a5223c07bf224c9fb378ed1b8574755e860cd4157ff7287e3bb815d8311ed7"},
		{name: "crc32c", mode: 3, storedCRC: 0xA35AEA48},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			page := append([]byte(nil), base...)
			if tt.storedCRC != 0 {
				binary.LittleEndian.PutUint32(page[dmPageChecksumOffset:], tt.storedCRC)
			}
			if tt.storedHash != "" {
				digest, err := hex.DecodeString(tt.storedHash)
				if err != nil {
					t.Fatal(err)
				}
				offset := len(page) - len(digest) - dmPageCheckTailSize
				copy(page[offset:], digest)
			}
			ok, err := verifyDMPageCheck(page, tt.mode, tt.hashName)
			if err != nil || !ok {
				t.Fatalf("verify mode %d: ok=%t err=%v", tt.mode, ok, err)
			}
			if tt.mode != 0 {
				page[0x100] ^= 0x01
				ok, err = verifyDMPageCheck(page, tt.mode, tt.hashName)
				if err != nil || ok {
					t.Fatalf("corruption was not detected: ok=%t err=%v", ok, err)
				}
			}
		})
	}
}

func TestVerifyDMPageCheckRejectsUnknownModeAndHash(t *testing.T) {
	page := make([]byte, 8192)
	if _, err := verifyDMPageCheck(page, 9, ""); err == nil {
		t.Fatal("unknown PAGE_CHECK mode was accepted")
	}
	if _, err := verifyDMPageCheck(page, 2, "SM3"); err == nil {
		t.Fatal("unsupported hash was accepted")
	}
}

func TestHashPageMovesSlotDirectoryBeforeDigest(t *testing.T) {
	const pageSize = 8192
	page := make([]byte, pageSize)
	binary.LittleEndian.PutUint32(page[dmPageKindOff:], dmPageKindRowData)
	putTestRow(page, dataRowAreaStart, 7, 0x00)
	binary.LittleEndian.PutUint16(page[dataPageSlotCountOff:], 3)
	binary.LittleEndian.PutUint16(page[dataPageFreeEndOff:], dataRowAreaStart+7)
	binary.LittleEndian.PutUint16(page[dataPageRecordCountOff:], 1)

	hashOffset := pageSize - sha256.Size - dmPageCheckTailSize
	slotStart := hashOffset - 3*2
	binary.LittleEndian.PutUint16(page[slotStart:], 0x5A)
	binary.LittleEndian.PutUint16(page[slotStart+2:], dataRowAreaStart)
	binary.LittleEndian.PutUint16(page[slotStart+4:], 0x52)
	digest := sha256.Sum256(page[:hashOffset])
	copy(page[hashOffset:], digest[:])

	name, size, ok := detectDMPageHash(page)
	if !ok || name != "SHA256" || size != sha256.Size {
		t.Fatalf("hash page was not detected: name=%q size=%d ok=%t", name, size, ok)
	}
	rows := locateRowsInDataPage(page, pageSize, 1)
	if len(rows) != 1 || rows[0].offset != dataRowAreaStart {
		t.Fatalf("hash-adjusted slot directory was not used: %+v", rows)
	}

	page[hashOffset] ^= 0x01
	if _, _, ok := detectDMPageHash(page); ok {
		t.Fatal("corrupted digest unexpectedly verified")
	}
	if got := pageSlotTrailerLenForPage(page); got != sha256.Size+dmPageCheckTailSize {
		t.Fatalf("corrupt hash trailer length=%d, want %d", got, sha256.Size+dmPageCheckTailSize)
	}
	rows = locateRowsInDataPage(page, pageSize, 1)
	if len(rows) != 1 || rows[0].offset != dataRowAreaStart {
		t.Fatalf("corrupt hash page lost its inferable slot directory: %+v", rows)
	}
}

func TestNoCheckPageKeepsStructurallyValidFixedTrailer(t *testing.T) {
	page := make([]byte, 8192)
	binary.LittleEndian.PutUint16(page[dataPageSlotCountOff:], 5)
	binary.LittleEndian.PutUint16(page[dataPageFreeEndOff:], 0x500)
	start := len(page) - pageSlotTrailerLen - 5*2
	for i, off := range []uint16{0x5A, 0x400, 0x300, 0x200, 0x52} {
		binary.LittleEndian.PutUint16(page[start+i*2:], off)
	}
	// Repeated 0x005A values before the real directory reproduced the false
	// positive seen on a PAGE_CHECK=0 SYSOBJECTS root page.
	for pos := start - 64; pos < start; pos += 2 {
		binary.LittleEndian.PutUint16(page[pos:], 0x5A)
	}
	if got := pageSlotTrailerLenForPage(page); got != pageSlotTrailerLen {
		t.Fatalf("PAGE_CHECK=0 fixed trailer was misdetected as hash trailer: %d", got)
	}
}
