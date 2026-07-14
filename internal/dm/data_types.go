package dm

import (
	"fmt"
	"strings"
)

const (
	dmJSONTextScaleMask   = 0x2000
	dmJSONBinaryScaleMask = 0x4000
	dmLocalTimezoneMask   = 0x1000
)

func normalizeCatalogColumnType(dataType string, scale int16) string {
	if normalizeDataType(dataType) != "BLOB" {
		return dataType
	}
	switch int(uint16(scale)) & (dmJSONTextScaleMask | dmJSONBinaryScaleMask) {
	case dmJSONTextScaleMask:
		return "JSON"
	case dmJSONBinaryScaleMask:
		return "JSONB"
	default:
		return dataType
	}
}

func isJSONDataType(dataType string) bool {
	switch normalizeDataType(dataType) {
	case "JSON", "JSONB":
		return true
	default:
		return false
	}
}

func isYearMonthIntervalDataType(dataType string) bool {
	switch normalizeDataType(dataType) {
	case "INTERVAL YEAR", "INTERVAL MONTH", "INTERVAL YEAR TO MONTH":
		return true
	default:
		return false
	}
}

func isDayTimeIntervalDataType(dataType string) bool {
	switch normalizeDataType(dataType) {
	case "INTERVAL DAY", "INTERVAL HOUR", "INTERVAL MINUTE", "INTERVAL SECOND",
		"INTERVAL DAY TO HOUR", "INTERVAL DAY TO MINUTE", "INTERVAL DAY TO SECOND",
		"INTERVAL HOUR TO MINUTE", "INTERVAL HOUR TO SECOND", "INTERVAL MINUTE TO SECOND":
		return true
	default:
		return false
	}
}

func intervalQualifier(dataType string) string {
	return strings.TrimPrefix(normalizeDataType(dataType), "INTERVAL ")
}

func intervalPrecisions(scale int16) (leading int, fractional int) {
	value := uint16(scale)
	return int((value >> 4) & 0x0F), int(value & 0x0F)
}

func timeFractionalPrecision(scale int16) int {
	return int(uint16(scale) &^ dmLocalTimezoneMask & 0x0FFF)
}

func formatIntervalColumnType(dataType string, scale int16) (string, bool) {
	typ := normalizeDataType(dataType)
	if !isYearMonthIntervalDataType(typ) && !isDayTimeIntervalDataType(typ) {
		return "", false
	}
	leading, fractional := intervalPrecisions(scale)
	if leading == 0 {
		return typ, true
	}
	switch typ {
	case "INTERVAL YEAR", "INTERVAL MONTH", "INTERVAL DAY", "INTERVAL HOUR", "INTERVAL MINUTE":
		return fmt.Sprintf("%s(%d)", typ, leading), true
	case "INTERVAL SECOND":
		return fmt.Sprintf("INTERVAL SECOND(%d,%d)", leading, fractional), true
	case "INTERVAL YEAR TO MONTH":
		return fmt.Sprintf("INTERVAL YEAR(%d) TO MONTH", leading), true
	case "INTERVAL DAY TO HOUR":
		return fmt.Sprintf("INTERVAL DAY(%d) TO HOUR", leading), true
	case "INTERVAL DAY TO MINUTE":
		return fmt.Sprintf("INTERVAL DAY(%d) TO MINUTE", leading), true
	case "INTERVAL DAY TO SECOND":
		return fmt.Sprintf("INTERVAL DAY(%d) TO SECOND(%d)", leading, fractional), true
	case "INTERVAL HOUR TO MINUTE":
		return fmt.Sprintf("INTERVAL HOUR(%d) TO MINUTE", leading), true
	case "INTERVAL HOUR TO SECOND":
		return fmt.Sprintf("INTERVAL HOUR(%d) TO SECOND(%d)", leading, fractional), true
	case "INTERVAL MINUTE TO SECOND":
		return fmt.Sprintf("INTERVAL MINUTE(%d) TO SECOND(%d)", leading, fractional), true
	default:
		return typ, true
	}
}
