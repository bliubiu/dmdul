package dm

import (
	"encoding/hex"
	"testing"
)

func TestHugeScalarDM8Samples(t *testing.T) {
	for _, tc := range []struct {
		typ, raw string
		scale    int16
		want     any
	}{
		{"BIT", "01000000", 0, int8(1)},
		{"TINYINT", "81ffffff", 0, int8(-127)},
		{"REAL", "00e015c3", 0, float32(-149.875)},
		{"DOUBLE", "0000000000bc62c0", 0, float64(-149.875)},
		{"TIME", "6c0701010c223800ca5be80307", 6, "12:34:56.123456"},
		{"TIMESTAMP", "e807021d0c223800ca5be80307", 6, "2024-02-29 12:34:56.123456"},
		{"TIMESTAMP WITH TIME ZONE", "e807021d0c223800ca5be00107", 6, "2024-02-29 12:34:56.123456 +08:00"},
	} {
		t.Run(tc.typ, func(t *testing.T) {
			raw, _ := hex.DecodeString(tc.raw)
			got, err := decodeHugeFixedValue(columnDef{DataType: tc.typ, Scale: tc.scale}, raw)
			if err != nil || got != tc.want {
				t.Fatalf("got=%v want=%v err=%v", got, tc.want, err)
			}
		})
	}
	for _, tc := range []struct {
		typ, raw string
		scale    int16
	}{
		{"BIT", "02000000", 0}, {"TINYINT", "80000000", 0},
		{"TIME", "6c07010118223800ca5be80307", 6},
		{"TIMESTAMP", "e807021d0c223800ca5be80307", 10},
		{"TIMESTAMP WITH TIME ZONE", "e807021d0c223800ca5bff7f07", 6},
	} {
		raw, _ := hex.DecodeString(tc.raw)
		if _, err := decodeHugeFixedValue(columnDef{DataType: tc.typ, Scale: tc.scale}, raw); err == nil {
			t.Fatalf("accepted invalid %s", tc.typ)
		}
	}
}
