package dm

import (
	"bytes"
	"testing"
)

func TestTextEncodingKnownBytes(t *testing.T) {
	for _, tc := range []struct {
		name, charset, text string
		wire                []byte
	}{
		{"utf8", "UTF-8", "\u4e2d\u6587", []byte{0xe4, 0xb8, 0xad, 0xe6, 0x96, 0x87}},
		{"gb18030-two-byte", "GB18030", "\u4e2d\u6587", []byte{0xd6, 0xd0, 0xce, 0xc4}},
		{"gb18030-four-byte", "GB18030", "\U00010000", []byte{0x90, 0x30, 0x81, 0x30}},
		{"euckr", "EUC-KR", "\ud55c\uae00", []byte{0xc7, 0xd1, 0xb1, 0xdb}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := decodeWithCharset(tc.wire, tc.charset)
			if !ok || got != tc.text {
				t.Fatalf("decode %X = %q ok=%t", tc.wire, got, ok)
			}
			charset, err := dmpCharsetFromName(tc.charset)
			if err != nil {
				t.Fatal(err)
			}
			encoded, err := encodeDMPText(tc.text, charset)
			if err != nil || !bytes.Equal(encoded, tc.wire) {
				t.Fatalf("encode %q = %X err=%v want=%X", tc.text, encoded, err, tc.wire)
			}
		})
	}
}
