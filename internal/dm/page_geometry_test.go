package dm

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"reflect"
	"strings"
	"testing"
)

func TestPageGeometryRecoversDamagedHeader(t *testing.T) {
	for _, size := range []uint32{4096, 8192, 16384, 32768} {
		raw := make([]byte, 32*size)
		for n := uint32(16); n < 22; n++ {
			buildHealthyRowPage(raw[n*size:(n+1)*size], size, 4, 0, n, 1042, 1)
		}
		// Neither the header field nor the file length discriminates candidates.
		binary.LittleEndian.PutUint32(raw[systemPageSizeOffset:], 65536)
		got, source, err := ProbePageSize(bytes.NewReader(raw), int64(len(raw)))
		if err != nil || got != size || !strings.Contains(source, "multi-page") {
			t.Fatalf("size=%d got=%d source=%s err=%v", size, got, source, err)
		}
	}
}

func TestPageGeometryRejectsMixedFileIdentities(t *testing.T) {
	for _, headerSize := range []uint32{0, 8192} {
		for _, other := range []dataFileKey{{groupID: 4, fileID: 1}, {groupID: 5, fileID: 0}} {
			raw := make([]byte, 32*8192)
			binary.LittleEndian.PutUint32(raw[systemPageSizeOffset:], headerSize)
			for n := uint32(16); n < 22; n++ {
				key := dataFileKey{groupID: 4, fileID: 0}
				if n >= 19 {
					key = other
				}
				buildHealthyRowPage(raw[n*8192:(n+1)*8192], 8192, uint16(key.groupID), uint16(key.fileID), n, 1042, 1)
			}
			if _, _, err := ProbePageSize(bytes.NewReader(raw), int64(len(raw))); err == nil {
				t.Fatalf("mixed group/file accepted with header=%d other=%v", headerSize, other)
			}
		}
	}
}

func TestPageGeometryRequiresIndependentEvidence(t *testing.T) {
	for _, mode := range []string{"two-pages", "wrong-number", "unknown-kind", "identity-only"} {
		t.Run(mode, func(t *testing.T) {
			raw := make([]byte, 32*8192)
			for n := uint32(16); n < 19; n++ {
				page := raw[n*8192 : (n+1)*8192]
				buildHealthyRowPage(page, 8192, 4, 0, n, 1042, 1)
				switch mode {
				case "two-pages":
					if n == 18 {
						clear(page)
					}
				case "wrong-number":
					binary.LittleEndian.PutUint32(page[4:], n+1)
				case "unknown-kind":
					binary.LittleEndian.PutUint32(page[20:], 0x41)
				case "identity-only":
					clear(page[24:])
					binary.LittleEndian.PutUint32(page[20:], 0x15)
				}
			}
			if _, _, err := ProbePageSize(bytes.NewReader(raw), int64(len(raw))); err == nil {
				t.Fatal("insufficient independent evidence accepted")
			}
		})
	}
}

type geometryTestReader struct {
	header []byte
	size   int64
	reads  [][2]int64
	fault  error
	short  bool
}

func (r *geometryTestReader) ReadAt(p []byte, off int64) (int, error) {
	r.reads = append(r.reads, [2]int64{off, int64(len(p))})
	if off < 0 || off+int64(len(p)) > r.size {
		return 0, fmt.Errorf("probe out of range: %d+%d > %d", off, len(p), r.size)
	}
	clear(p)
	if off < int64(len(r.header)) {
		copy(p, r.header[off:])
	}
	if off > 0 {
		if r.short {
			return len(p) - 1, r.fault
		}
		if r.fault != nil {
			return len(p), r.fault
		}
	}
	return len(p), nil
}

func TestPageGeometryBoundedDeterministicIO(t *testing.T) {
	const fileSize = int64(8) << 40
	header := make([]byte, 512)
	binary.LittleEndian.PutUint32(header[systemPageSizeOffset:], 8192)
	a := &geometryTestReader{header: header, size: fileSize}
	b := &geometryTestReader{header: header, size: fileSize}
	for _, r := range []*geometryTestReader{a, b} {
		if got, _, err := ProbePageSize(r, fileSize); err != nil || got != 8192 {
			t.Fatalf("size=%d err=%v", got, err)
		}
		var total int64
		for _, read := range r.reads {
			total += read[1]
			if read[1] > 32768 {
				t.Fatalf("oversized read: %v", read)
			}
		}
		if len(r.reads) > 641 || total > 10<<20 {
			t.Fatalf("unbounded probe: calls=%d bytes=%d", len(r.reads), total)
		}
	}
	if !reflect.DeepEqual(a.reads, b.reads) {
		t.Fatal("probe read order is nondeterministic")
	}
}

func TestPageGeometryReadFailuresAreNotEvidence(t *testing.T) {
	broken := errors.New("injected I/O failure")
	for _, tc := range []struct {
		name  string
		fault error
		short bool
		want  error
	}{
		{"error", broken, false, broken},
		{"short-eof", io.EOF, true, io.EOF},
		{"short-nil", nil, true, io.ErrUnexpectedEOF},
		{"complete-eof", io.EOF, false, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			header := make([]byte, 512)
			binary.LittleEndian.PutUint32(header[systemPageSizeOffset:], 8192)
			r := &geometryTestReader{header: header, size: 32 * 8192, fault: tc.fault, short: tc.short}
			_, _, err := ProbePageSize(r, r.size)
			if !errors.Is(err, tc.want) {
				t.Fatalf("got %v want %v", err, tc.want)
			}
		})
	}
}

func TestPageGeometryRejectsPageCountOverflow(t *testing.T) {
	header := make([]byte, 512)
	binary.LittleEndian.PutUint32(header[systemPageSizeOffset:], 8192)
	r := &geometryTestReader{header: header, size: int64(1<<32+1) * 8192}
	if _, _, err := ProbePageSize(r, r.size); err == nil || !strings.Contains(err.Error(), "32-bit") {
		t.Fatalf("overflow accepted: %v", err)
	}
	if count, _ := detectSystemPageCount(header, r.size, 8192); count != 0 {
		t.Fatalf("wrapped page count: %d", count)
	}
}

func TestPageProbeRefsBoundaries(t *testing.T) {
	for _, size := range []int64{-1, 0, 4096, 8193, 1 << 40, 1<<63 - 1} {
		for _, ps := range []uint32{0, 4096, 8192, 16384, 32768, 65536} {
			refs := pageProbeRefs(size, ps)
			if len(refs) > 160 {
				t.Fatalf("too many refs: %d", len(refs))
			}
			for i, ref := range refs {
				if ref <= 0 || ref > int64(^uint32(0)) || (ref+1)*int64(ps) > size || (i > 0 && ref <= refs[i-1]) {
					t.Fatalf("invalid refs for size=%d ps=%d: %v", size, ps, refs)
				}
			}
		}
	}
}

func TestPageGeometryRejectsMissingOrAmbiguousEvidence(t *testing.T) {
	raw := make([]byte, 64*32768)
	if _, _, err := ProbePageSize(bytes.NewReader(raw), int64(len(raw))); err == nil {
		t.Fatal("blank aligned file guessed")
	}
	for n := uint32(2); n < 5; n++ {
		buildHealthyRowPage(raw[n*8192:(n+1)*8192], 8192, 4, 0, n, 1042, 1)
	}
	for n := uint32(16); n < 19; n++ {
		buildHealthyRowPage(raw[n*32768:(n+1)*32768], 32768, 4, 0, n, 1042, 1)
	}
	if _, _, err := ProbePageSize(bytes.NewReader(raw), int64(len(raw))); err == nil {
		t.Fatal("ambiguous geometry accepted")
	}
}

func TestPageGeometryHeaderSurvivesTruncation(t *testing.T) {
	raw := make([]byte, 3*32768+123)
	binary.LittleEndian.PutUint32(raw[systemPageSizeOffset:], 32768)
	got, _, err := ProbePageSize(bytes.NewReader(raw), int64(len(raw)))
	if err != nil || got != 32768 {
		t.Fatalf("got=%d err=%v", got, err)
	}
	if validPageSize(65536) {
		t.Fatal("unverified 64 KiB accepted")
	}
}

func TestPageGeometryRejectsPlausibleButContradictoryHeader(t *testing.T) {
	raw := make([]byte, 32*32768)
	binary.LittleEndian.PutUint32(raw[systemPageSizeOffset:], 8192)
	for n := uint32(16); n < 22; n++ {
		buildHealthyRowPage(raw[n*32768:(n+1)*32768], 32768, 4, 0, n, 1042, 1)
	}
	if _, _, err := ProbePageSize(bytes.NewReader(raw), int64(len(raw))); err == nil || !strings.Contains(err.Error(), "conflicts") {
		t.Fatalf("%v", err)
	}
}
