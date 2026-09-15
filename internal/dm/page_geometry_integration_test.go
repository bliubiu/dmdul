package dm

import (
	"encoding/binary"
	"io"
	"os"
	"path/filepath"
	"testing"
)

// Optional read-only tests against operator-supplied cold SYSTEM.DBF files.
// Header corruption is an in-memory overlay; source DBFs are never modified.
func TestPageGeometryOfflineSamples(t *testing.T) {
	paths := filepath.SplitList(os.Getenv("DMDUL_GEOMETRY_SAMPLES"))
	if len(paths) == 0 {
		t.Skip("set DMDUL_GEOMETRY_SAMPLES to cold SYSTEM.DBF paths")
	}
	for _, path := range paths {
		t.Run(filepath.Base(filepath.Dir(path)), func(t *testing.T) {
			file, err := os.Open(path)
			if err != nil {
				t.Fatal(err)
			}
			defer file.Close()
			stat, err := file.Stat()
			if err != nil {
				t.Fatal(err)
			}
			header, err := readSystemHeaderFromReader(file, stat.Size())
			if err != nil {
				t.Fatal(err)
			}
			want, _ := detectSystemPageSize(header, stat.Size())
			if !validPageSize(want) {
				t.Fatal("integration fixture must have a valid original header")
			}
			for _, field := range []uint32{want, 0, 65536, 0xffffffff, 4096, 8192, 16384, 32768} {
				r := &geometryHeaderOverlay{ReaderAt: file, value: field}
				got, source, err := ProbePageSize(r, stat.Size())
				if validPageSize(field) && field != want {
					if err == nil {
						t.Fatalf("contradictory header %d accepted as %d", field, got)
					}
					t.Logf("header=%d rejected: %v", field, err)
					continue
				}
				if err != nil || got != want {
					t.Fatalf("header=%d got=%d want=%d source=%s err=%v", field, got, want, source, err)
				}
				if r.bytes > 10<<20 || r.calls > 641 {
					t.Fatalf("unbounded probe: calls=%d bytes=%d", r.calls, r.bytes)
				}
				t.Logf("header=%d recovered=%d calls=%d bytes=%d source=%s", field, got, r.calls, r.bytes, source)
			}
		})
	}
}

type geometryHeaderOverlay struct {
	io.ReaderAt
	value uint32
	calls int
	bytes int
}

func (r *geometryHeaderOverlay) ReadAt(p []byte, off int64) (int, error) {
	n, err := r.ReaderAt.ReadAt(p, off)
	r.calls++
	r.bytes += n
	var field [4]byte
	binary.LittleEndian.PutUint32(field[:], r.value)
	for i, b := range field {
		pos := int64(systemPageSizeOffset+i) - off
		if pos >= 0 && pos < int64(n) {
			p[pos] = b
		}
	}
	return n, err
}
