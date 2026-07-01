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

func TestScanRawRoutineTextsFindsFunctionProcedureAndPackage(t *testing.T) {
	raw := []byte(
		"aaCREATE OR REPLACE FUNCTION APP.F_AMOUNT RETURN INT AS\nBEGIN\n    IF 1 = 1 THEN\n        RETURN 1;\n    END IF;\n    RETURN 0;\nEND;\x00" +
			"bbCREATE OR REPLACE PROCEDURE APP.P_ADD AS\nBEGIN\n    NULL;\nEND;\x00" +
			"ccCREATE OR REPLACE PACKAGE APP.PKG_TEST AS\n    PROCEDURE RUN;\nEND PKG_TEST;\x00" +
			"ddCREATE OR REPLACE PACKAGE BODY APP.PKG_TEST AS\n    PROCEDURE RUN AS BEGIN NULL; END;\nEND PKG_TEST;\x00")

	got := scanRawRoutineTexts(raw, textDecoder{preferred: "utf-8"})
	if !containsText(got[routineKey("APP", "F_AMOUNT", "FUNCTION")], "END IF;") {
		t.Fatalf("function text not recovered correctly: %q", got[routineKey("APP", "F_AMOUNT", "FUNCTION")])
	}
	if !containsText(got[routineKey("APP", "P_ADD", "PROCEDURE")], "PROCEDURE APP.P_ADD") {
		t.Fatalf("procedure text not recovered correctly: %q", got[routineKey("APP", "P_ADD", "PROCEDURE")])
	}
	if !containsText(got[routineKey("APP", "PKG_TEST", "PACKAGE")], "PACKAGE APP.PKG_TEST") {
		t.Fatalf("package spec text not recovered correctly: %q", got[routineKey("APP", "PKG_TEST", "PACKAGE")])
	}
	if !containsText(got[routineKey("APP", "PKG_TEST", "PACKAGE BODY")], "PACKAGE BODY APP.PKG_TEST") {
		t.Fatalf("package body text not recovered correctly: %q", got[routineKey("APP", "PKG_TEST", "PACKAGE BODY")])
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

func TestKnownGeneratedSYSDBARoutineIsFiltered(t *testing.T) {
	if !isKnownGeneratedSYSDBARoutine("SYSDBA", "SP_DB_BAKSET_REMOVE_BATCH") {
		t.Fatalf("known generated SYSDBA routine should be filtered")
	}
	if isKnownGeneratedSYSDBARoutine("SYSDBA", "P_ADD_SALES_LIST") {
		t.Fatalf("user-created SYSDBA procedure should not be filtered by name")
	}
	if isKnownGeneratedSYSDBARoutine("APP", "SP_DB_BAKSET_REMOVE_BATCH") {
		t.Fatalf("same name under a normal owner should not be filtered")
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
