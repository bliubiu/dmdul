package dm

import (
	"encoding/binary"
	"fmt"
	"time"
)

func hugeFixedTypeID(column columnDef) uint16 {
	switch normalizeDataType(column.DataType) {
	case "CHAR", "CHARACTER":
		return 1
	case "VARCHAR", "VARCHAR2":
		return 2
	case "BIT", "BOOL", "BOOLEAN":
		return 3
	case "TINYINT":
		return 5
	case "SMALLINT":
		return 6
	case "INT", "INTEGER", "PLS_INTEGER":
		return 7
	case "BIGINT":
		return 8
	case "REAL", "BINARY_FLOAT":
		return 10
	case "FLOAT":
		if fixedDataSizeForColumn(column) == 4 {
			return 10
		}
		return 11
	case "DOUBLE", "DOUBLE PRECISION":
		return 11
	case "DATE":
		return 14
	case "TIME":
		return 15
	case "TIMESTAMP", "DATETIME":
		return 16
	case "BINARY":
		return 17
	case "VARBINARY":
		return 18
	case "TIMESTAMP WITH TIME ZONE", "DATETIME WITH TIME ZONE":
		return 23
	}
	if isYearMonthIntervalDataType(column.DataType) {
		return 20
	}
	if isDayTimeIntervalDataType(column.DataType) {
		return 21
	}
	return 0
}

func (reader *hugeColumnSectionReader) initFixedNulls(length int64) error {
	if length == 0 {
		if reader.meta.nullsKnown && reader.meta.nulls != 0 {
			return fmt.Errorf("HFS non-nullable column %s has N_NULL=%d", reader.column.Name, reader.meta.nulls)
		}
		return nil
	}
	if length > 32*1024*1024 {
		return fmt.Errorf("HFS NULL bitmap exceeds 32 MiB limit")
	}
	reader.presentBits = make([]byte, int(length))
	start := reader.meta.offset + int64(reader.meta.nlen) - length
	if _, err := reader.data.ReadAt(reader.presentBits, start); err != nil {
		return fmt.Errorf("read HFS NULL bitmap: %w", err)
	}
	// Unlike row metadata, HFS fixed sections use an MSB-first presence bit:
	// one means a value, zero means NULL. The bitmap is at the section end.
	var nulls uint32
	for i := uint32(0); i < reader.meta.count; i++ {
		if reader.presentBits[i/8]&(0x80>>(i%8)) == 0 {
			nulls++
		}
	}
	if reader.meta.nullsKnown && nulls != reader.meta.nulls {
		return fmt.Errorf("HFS column %s NULL bitmap count=%d differs from AUX N_NULL=%d", reader.column.Name, nulls, reader.meta.nulls)
	}
	return nil
}

func decodeHugeFixedValue(column columnDef, raw []byte) (any, error) {
	switch normalizeDataType(column.DataType) {
	case "BIT", "BOOL", "BOOLEAN", "TINYINT":
		if len(raw) != 4 {
			return nil, fmt.Errorf("HFS %s requires four bytes", column.DataType)
		}
		value := int32(binary.LittleEndian.Uint32(raw))
		if normalizeDataType(column.DataType) == "TINYINT" {
			if value < -128 || value > 127 {
				return nil, fmt.Errorf("HFS TINYINT out of range: %d", value)
			}
		} else if value != 0 && value != 1 {
			return nil, fmt.Errorf("invalid HFS boolean: %d", value)
		}
		return int8(value), nil
	case "TIME", "TIMESTAMP", "DATETIME", "TIMESTAMP WITH TIME ZONE", "DATETIME WITH TIME ZONE":
		return decodeHugeDateTime(column, raw)
	case "SMALLINT":
		if len(raw) != 4 {
			return nil, fmt.Errorf("HFS SMALLINT requires four bytes")
		}
		value := int32(binary.LittleEndian.Uint32(raw))
		if value < -32768 || value > 32767 {
			return nil, fmt.Errorf("HFS SMALLINT out of range: %d", value)
		}
		return int16(value), nil
	case "DATE":
		if len(raw) != 13 {
			return nil, fmt.Errorf("HFS DATE requires thirteen bytes")
		}
		// Only the observed, unpacked AD date form is accepted. Nonzero time
		// fields or another timezone marker require a separate layout probe.
		for _, b := range raw[4:10] {
			if b != 0 {
				return nil, fmt.Errorf("unverified HFS DATE time payload")
			}
		}
		if binary.LittleEndian.Uint16(raw[10:]) != 1000 || raw[12] != 0 {
			return nil, fmt.Errorf("unverified HFS DATE suffix")
		}
		year, month, day := int(binary.LittleEndian.Uint16(raw)), int(raw[2]), int(raw[3])
		date := time.Date(year, time.Month(month), day, 0, 0, 0, 0, time.UTC)
		if year < 1 || year > 9999 || date.Year() != year || int(date.Month()) != month || date.Day() != day {
			return nil, fmt.Errorf("invalid HFS DATE %d-%d-%d", year, month, day)
		}
		return date.Format("2006-01-02"), nil
	}
	value, end, err := parseFixedDataValuePresent(column, raw, 0)
	if err != nil {
		return nil, err
	}
	if end != len(raw) {
		return nil, fmt.Errorf("fixed decoder consumed %d/%d bytes", end, len(raw))
	}
	return value, nil
}

func decodeHugeDateTime(column columnDef, raw []byte) (string, error) {
	if len(raw) != 13 {
		return "", fmt.Errorf("HFS datetime requires thirteen bytes")
	}
	year, month, day := int(binary.LittleEndian.Uint16(raw)), int(raw[2]), int(raw[3])
	hour, minute, second := int(raw[4]), int(raw[5]), int(raw[6])
	// HFS stores nanoseconds split around the two-byte timezone field.
	ns := uint32(raw[7]) | uint32(raw[8])<<8 | uint32(raw[9])<<16 | uint32(raw[12])<<24
	date := time.Date(year, time.Month(month), day, hour, minute, second, int(ns), time.UTC)
	if year < 1 || year > 9999 || date.Year() != year || int(date.Month()) != month || date.Day() != day || hour > 23 || minute > 59 || second > 59 || ns >= 1e9 {
		return "", fmt.Errorf("invalid HFS datetime")
	}
	typ := normalizeDataType(column.DataType)
	zone := int16(binary.LittleEndian.Uint16(raw[10:]))
	zoned := typ == "TIMESTAMP WITH TIME ZONE" || typ == "DATETIME WITH TIME ZONE"
	if (!zoned && zone != 1000) || (zoned && (zone < -12*60 || zone > 14*60)) {
		return "", fmt.Errorf("unverified HFS timezone %d", zone)
	}
	value := date.Format("2006-01-02 15:04:05")
	if typ == "TIME" {
		if year != 1900 || month != 1 || day != 1 {
			return "", fmt.Errorf("unverified HFS TIME base date")
		}
		value = date.Format("15:04:05")
	}
	precision := timeFractionalPrecision(column.Scale)
	if precision > 9 {
		return "", fmt.Errorf("unsupported HFS datetime precision %d", precision)
	}
	if precision > 0 {
		value += "." + fmt.Sprintf("%09d", ns)[:precision]
	}
	if zoned {
		value += " " + decodeDMTimezone(raw[10:12])
	}
	return value, nil
}
