package dm

import (
	"bytes"
	"compress/zlib"
	"encoding/binary"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/golang/snappy"
)

func TestHugeCompressedDM8Fixtures(t *testing.T) {
	for _, codec := range []string{"ZIP2", "ZIP9", "SNAP10"} {
		for col := 1; col <= 3; col++ {
			t.Run(fmt.Sprintf("%s/%d", codec, col), func(t *testing.T) {
				raw, err := os.ReadFile(filepath.Join("testdata", "hfs_compressed", fmt.Sprintf("%s_%d.bin", codec, col)))
				if err != nil {
					t.Fatal(err)
				}
				dir := t.TempDir()
				path := filepath.Join(dir, fmt.Sprintf("COL%04d_0000000000.dta", col))
				if err := os.WriteFile(path, append(make([]byte, 4096), raw...), 0600); err != nil {
					t.Fatal(err)
				}
				typ := map[int]string{1: "INT", 2: "VARCHAR", 3: "BIGINT"}[col]
				meta := hugeColumnSection{colID: uint16(col), offset: 4096, count: 1024, nlen: uint32(len(raw)), nullsKnown: true, nulls: uint32(1024 / (col + 2)), cprFlag: "Y", encFlag: "N"}
				r, _, err := openHugeColumnSection(dir, columnDef{ColID: uint16(col), Name: "V", DataType: typ, Nullable: "Y"}, meta, textDecoder{preferred: "utf-8"})
				if err != nil {
					t.Fatal(err)
				}
				defer r.close()
				scratch := r.scratch.Name()
				for row := 1; row <= 1024; row++ {
					var want any
					if row%(col+2) != 0 {
						switch col {
						case 1:
							want = int32(row % 7)
						case 2:
							want = fmt.Sprintf("TEXT-%d%s", row%11, strings.Repeat("x", 100))
						case 3:
							want = int64(row) * 1_000_000_000
						}
					}
					got, err := r.next()
					if err != nil || got != want {
						t.Fatalf("row=%d got=%v want=%v err=%v", row, got, want, err)
					}
				}
				r.close()
				if _, err := os.Stat(scratch); !os.IsNotExist(err) {
					t.Fatalf("scratch not removed: %v", err)
				}
			})
		}
	}
}

func TestHugeCompressionRejectsDamageAndBounds(t *testing.T) {
	var zip bytes.Buffer
	w := zlib.NewWriter(&zip)
	w.Write([]byte("verified"))
	w.Close()
	snap := snappy.Encode(nil, []byte("verified"))
	var sn bytes.Buffer
	binary.Write(&sn, binary.LittleEndian, uint32(len(snap)))
	sn.Write(snap)
	for _, raw := range [][]byte{zip.Bytes(), sn.Bytes()} {
		for _, size := range []uint32{7, 9, maxHugeDecodedSectionBytes + 1} {
			if err := decodeHugeCompressedBody(bytes.NewReader(raw), io.Discard, size); err == nil {
				t.Fatalf("size %d accepted", size)
			}
		}
		if err := decodeHugeCompressedBody(bytes.NewReader(raw[:len(raw)-1]), io.Discard, 8); err == nil {
			t.Fatal("truncation accepted")
		}
		bad := append(append([]byte(nil), raw...), byte(1))
		if err := decodeHugeCompressedBody(bytes.NewReader(bad), io.Discard, 8); err == nil {
			t.Fatal("trailing data accepted")
		}
	}
	bad := append([]byte(nil), zip.Bytes()...)
	bad[len(bad)-1] ^= 1
	if err := decodeHugeCompressedBody(bytes.NewReader(bad), io.Discard, 8); err == nil {
		t.Fatal("zlib checksum damage accepted")
	}
}

func FuzzHugeCompressedBody(f *testing.F) {
	f.Add([]byte{0x78, 0x9c, 0, 0}, uint16(8))
	f.Add([]byte{3, 0, 0, 0, 1, 0, 0}, uint16(1))
	f.Fuzz(func(t *testing.T, raw []byte, size uint16) {
		if len(raw) > 65536 {
			return
		}
		_ = decodeHugeCompressedBody(bytes.NewReader(raw), io.Discard, uint32(size))
	})
}
