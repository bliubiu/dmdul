package dm

import "testing"

func TestScanRawTriggerTextsFindsMultipleTriggers(t *testing.T) {
	raw := []byte("xxCREATE OR REPLACE TRIGGER APP.TRG_A\nBEFORE INSERT ON APP.T\nBEGIN\n    IF 1 = 1 THEN\n        NULL;\n    END IF;\nEND;\x00yyCREATE OR REPLACE TRIGGER APP.TRG_B\nBEFORE UPDATE ON APP.T\nBEGIN\n    NULL;\nEND;\xD7zz")

	got := scanRawTriggerTexts(raw, textDecoder{preferred: "utf-8"})
	if !containsText(got["APP.TRG_A"], "END IF;") || !containsText(got["APP.TRG_A"], "TRG_A") {
		t.Fatalf("TRG_A text not recovered correctly: %q", got["APP.TRG_A"])
	}
	if !containsText(got["APP.TRG_B"], "TRG_B") {
		t.Fatalf("TRG_B text not recovered correctly: %q", got["APP.TRG_B"])
	}
}

func TestSequenceIsPrivilegeTarget(t *testing.T) {
	obj := dictionaryObject{Type: "SCHOBJ", Subtype: "SEQ", Owner: "APP", Name: "SEQ_T"}
	if !isTabPrivilegeTarget(obj) {
		t.Fatalf("sequence should be accepted as an object privilege target")
	}
	if got := dictionaryPrivilegeObjectType(obj); got != "SEQUENCE" {
		t.Fatalf("unexpected sequence object type: %q", got)
	}
}

func TestParseSequencePayload(t *testing.T) {
	payload := []byte{
		0xFF, 0x0F, 0xA5, 0xD4, 0xE8, 0x00, 0x00, 0x00,
		0x01, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00,
		0x00, 0x00, 0x02, 0x00, 0x00, 0x00, 0x02, 0x00,
		0x14, 0x00, 0x00, 0x00,
	}
	got := parseSequencePayload(payload)
	if got.maxValue != 999999999999 || got.minValue != 1 || got.startWith != 1 || got.cacheSize != 20 {
		t.Fatalf("unexpected sequence payload parse: %+v", got)
	}
}

func containsText(value string, part string) bool {
	return len(value) >= len(part) && indexASCIIInsensitive([]byte(value), []byte(part)) >= 0
}
