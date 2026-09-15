package dm

import (
	"bytes"
	"encoding/binary"
	"testing"
)

// Compact inputs keep mutation useful: fuzz fields, not hundreds of KiB of
// zero-filled DBF space. Keep the raw-byte targets for arbitrary truncations.
func FuzzPageGeometryFields(f *testing.F) {
	for _, ps := range []uint32{4096, 8192, 16384, 32768} {
		f.Add(ps, uint32(0), uint16(4), uint16(0), uint8(3))
		f.Add(ps, ps, uint16(4), uint16(0), uint8(0))
	}
	f.Fuzz(func(t *testing.T, ps, header uint32, group, file uint16, count uint8) {
		if !validPageSize(ps) {
			return
		}
		raw := make([]byte, 6*ps)
		binary.LittleEndian.PutUint32(raw[systemPageSizeOffset:], header)
		rows := int(count % 4)
		for i := 0; i < rows; i++ {
			n := uint32(i + 2)
			buildHealthyRowPage(raw[n*ps:(n+1)*ps], ps, group, file, n, 1042, 1)
		}
		before := bytes.Clone(raw)
		got, _, err := ProbePageSize(bytes.NewReader(raw), int64(len(raw)))
		if !bytes.Equal(raw, before) {
			t.Fatal("probe mutated source")
		}
		sufficient := rows == 3 && group != 0xffff && file < 0x8000
		headerValid := validPageSize(header) && int(header) <= len(raw)
		if sufficient && (!headerValid || header == ps) && (err != nil || got != ps) {
			t.Fatalf("lost consistent evidence: size=%d header=%d got=%d err=%v", ps, header, got, err)
		}
		if sufficient && headerValid && header != ps && err == nil {
			t.Fatal("contradictory geometry accepted")
		}
		if err == nil && !validPageSize(got) {
			t.Fatalf("invalid page size %d", got)
		}
	})
}

func FuzzPageProbeRefs(f *testing.F) {
	f.Add(int64(0), uint32(0))
	f.Add(int64(8<<40), uint32(8192))
	f.Add(int64(1<<63-1), uint32(4096))
	f.Fuzz(func(t *testing.T, size int64, ps uint32) {
		refs := pageProbeRefs(size, ps)
		if len(refs) > pageProbePrefixPages+pageProbeSpreadPages {
			t.Fatal("unbounded sample count")
		}
		for i, ref := range refs {
			if !validPageSize(ps) || ref <= 0 || ref > int64(^uint32(0)) || (ref+1)*int64(ps) > size || (i > 0 && ref <= refs[i-1]) {
				t.Fatalf("invalid sample %d at %d for size=%d pageSize=%d", ref, i, size, ps)
			}
		}
	})
}

func FuzzPageCheck(f *testing.F) {
	for _, ps := range []uint32{8192, 16384, 32768} {
		for _, sm3 := range []bool{false, true} {
			f.Add(ps, sm3, uint16(4095), byte(1))
			f.Add(ps, sm3, uint16(0), byte(0))
		}
	}
	f.Fuzz(func(t *testing.T, ps uint32, sm3 bool, offset uint16, mask byte) {
		if !validPageSize(ps) || ps == 4096 {
			return
		}
		name := "SHA256"
		if sm3 {
			name = "SM3"
		}
		page, _ := sectorHashTestPage(int(ps), name)
		// The final eight bytes are outside the digest and are not corruption
		// proof. Every other byte is covered by a sector checksum.
		page[int(offset)%(len(page)-dmPageCheckTailSize)] ^= mask
		original := bytes.Clone(page)
		ok, err := verifyDMPageCheck(page, 2, name)
		if err != nil || ok != (mask == 0) {
			t.Fatalf("checksum result ok=%t err=%v mask=%d", ok, err, mask)
		}
		if !bytes.Equal(page, original) {
			t.Fatal("checksum verification mutated source")
		}
		if ok {
			restorePageProtectionBytes(page, ps)
			once := bytes.Clone(page)
			restorePageProtectionBytes(page, ps)
			if !bytes.Equal(page, once) {
				t.Fatal("hash protection restoration is not idempotent")
			}
		}
	})
}
