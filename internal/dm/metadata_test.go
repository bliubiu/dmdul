package dm

import "testing"

func TestDetectSystemCharsetFromDataUsesControlPage4UnicodeFlag(t *testing.T) {
	pageSize := uint32(128)
	data := make([]byte, int(pageSize)*5)
	data[int(pageSize)*systemControlPage4No+systemUnicodeFlagOffset] = 1

	charset, ok := detectSystemCharsetFromData(data, pageSize)
	if !ok {
		t.Fatalf("detectSystemCharsetFromData() did not detect charset")
	}
	if charset.DecoderName != "utf-8" || charset.Flag != 1 {
		t.Fatalf("charset = %+v, want UTF-8 flag 1", charset)
	}
}

func TestCharsetComparisonMatchesIniNumericValue(t *testing.T) {
	meta := DatabaseMetadata{
		Charset:        "EUC-KR (UNICODE_FLAG=2)",
		CharsetSource:  "SYSTEM.DBF page 4 + 0x2D",
		CharsetFlag:    2,
		HasCharsetFlag: true,
		IniCharset:     "2",
	}

	if got := meta.CharsetComparison(); got != "match" {
		t.Fatalf("CharsetComparison() = %q, want match", got)
	}
}

func TestRestorePageProtectionBytesRestoresFourKBoundary(t *testing.T) {
	page := make([]byte, 8192)
	copy(page[0x0FFC:0x1000], []byte{0xAA, 0xBB, 0xCC, 0xDD})
	copy(page[0x1FF0:0x1FF4], []byte("cess"))

	restorePageProtectionBytes(page, 8192)

	if got := string(page[0x0FFC:0x1000]); got != "cess" {
		t.Fatalf("restored boundary = %q, want cess", got)
	}
	if got := pageTailReservedLen(8192); got != 16 {
		t.Fatalf("pageTailReservedLen(8192) = %d, want 16", got)
	}
	if got := pageTailReservedLen(32768); got != 40 {
		t.Fatalf("pageTailReservedLen(32768) = %d, want 40", got)
	}
}

func TestRestorePageProtectionBytesKeepsPlausibleBoundary(t *testing.T) {
	page := make([]byte, 32768)
	copy(page[0x4FFC:0x5000], []byte("RREN"))
	copy(page[0x7FE8:0x7FEC], []byte{0xCB, 0x09, 0x91, 0x09})

	restorePageProtectionBytes(page, 32768)

	if got := string(page[0x4FFC:0x5000]); got != "RREN" {
		t.Fatalf("boundary = %q, want RREN", got)
	}
}
