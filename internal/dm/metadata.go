package dm

import (
	"encoding/binary"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

type DatabaseMetadata struct {
	SystemPath       string
	ControlPath      string
	IniPath          string
	DatabaseName     string
	DatabaseNameSrc  string
	InstanceName     string
	InstanceNameSrc  string
	Port             string
	PortSrc          string
	ExtentSize       uint32
	ExtentSizeSource string
	PageSize         uint32
	PageSizeSource   string
	PageCount        uint32
	PageCountSource  string
	Charset          string
	CharsetSource    string
	CharsetFlag      uint8
	HasCharsetFlag   bool
	IniExtentSize    string
	IniPageSize      string
	IniCharset       string
}

func DefaultControlPathForSystem(systemPath string) string {
	return filepath.Join(systemDir(systemPath), "dm.ctl")
}

func DefaultIniPathForSystem(systemPath string) string {
	return filepath.Join(systemDir(systemPath), "dm.ini")
}

func InspectDatabaseMetadata(systemPath string, controlPath string, iniPath string, charsetPreference string) DatabaseMetadata {
	meta := DatabaseMetadata{
		SystemPath:    systemPath,
		Charset:       defaultIfEmpty(strings.TrimSpace(charsetPreference), "auto"),
		CharsetSource: "decoder setting",
	}

	if controlPath == "" {
		defaultControlPath := DefaultControlPathForSystem(systemPath)
		if info, err := os.Stat(defaultControlPath); err == nil && !info.IsDir() {
			controlPath = defaultControlPath
		}
	}
	meta.ControlPath = controlPath
	meta.IniPath = iniPath

	if header, size, err := readSystemHeader(systemPath); err == nil {
		meta.ExtentSize, meta.ExtentSizeSource = detectSystemExtentSize(header)
		meta.PageSize, meta.PageSizeSource = detectSystemPageSize(header, size)
		meta.PageCount, meta.PageCountSource = detectSystemPageCount(header, size, meta.PageSize)
		if charset, ok := detectSystemCharsetFromFile(systemPath, meta.PageSize); ok {
			meta.Charset = charset.DisplayName
			meta.CharsetSource = charset.Source
			meta.CharsetFlag = charset.Flag
			meta.HasCharsetFlag = true
		}
	}

	if controlPath != "" {
		if ctl, err := InspectControlFile(controlPath); err == nil {
			meta.DatabaseName = ctl.DatabaseName
			meta.DatabaseNameSrc = "dm.ctl"
		}
	}

	if ini, ok := loadDMIni(iniPath); ok {
		meta.InstanceName = ini["INSTANCE_NAME"]
		if meta.InstanceName != "" {
			meta.InstanceNameSrc = "dm.ini"
		}
		meta.Port = ini["PORT_NUM"]
		if meta.Port != "" {
			meta.PortSrc = "dm.ini"
		}
		meta.IniExtentSize = firstIniValue(ini, "EXTENT_SIZE", "EXTENT_SIZE_IN_PAGE")
		meta.IniPageSize = ini["PAGE_SIZE"]
		meta.IniCharset = firstIniValue(ini, "CHARSET", "UNICODE_FLAG")
		if meta.IniCharset != "" && !meta.HasCharsetFlag {
			meta.Charset = meta.IniCharset
			meta.CharsetSource = "dm.ini"
		}
	}

	return meta
}

func (m DatabaseMetadata) ExtentComparison() string {
	return compareIniUint(m.IniExtentSize, uint64(m.ExtentSize), "not set")
}

func (m DatabaseMetadata) PageSizeComparison() string {
	return compareIniUint(m.IniPageSize, uint64(m.PageSize), "not set")
}

func (m DatabaseMetadata) CharsetComparison() string {
	if strings.TrimSpace(m.IniCharset) == "" {
		return "not set"
	}
	if m.HasCharsetFlag {
		if charsetIniMatchesFlag(m.IniCharset, m.CharsetFlag) {
			return "match"
		}
		return "mismatch"
	}
	if strings.EqualFold(normalizeCharsetToken(m.IniCharset), normalizeCharsetToken(m.Charset)) {
		return "match"
	}
	return "configured"
}

type systemCharset struct {
	DisplayName string
	DecoderName string
	Flag        uint8
	Source      string
}

func detectSystemCharsetFromFile(path string, pageSize uint32) (systemCharset, bool) {
	if pageSize == 0 {
		return systemCharset{}, false
	}
	file, err := os.Open(path)
	if err != nil {
		return systemCharset{}, false
	}
	defer file.Close()

	offset := int64(pageSize)*systemControlPage4No + systemUnicodeFlagOffset
	buf := []byte{0}
	if _, err := file.ReadAt(buf, offset); err != nil {
		return systemCharset{}, false
	}
	return systemCharsetFromFlag(buf[0])
}

func detectSystemCharsetFromData(data []byte, pageSize uint32) (systemCharset, bool) {
	if pageSize == 0 {
		return systemCharset{}, false
	}
	offset := int(pageSize)*systemControlPage4No + systemUnicodeFlagOffset
	if offset < 0 || offset >= len(data) {
		return systemCharset{}, false
	}
	return systemCharsetFromFlag(data[offset])
}

func systemCharsetFromFlag(flag byte) (systemCharset, bool) {
	charset := systemCharset{
		Flag:   flag,
		Source: "SYSTEM.DBF page 4 + 0x2D",
	}
	switch flag {
	case 0:
		charset.DisplayName = "GB18030 (UNICODE_FLAG=0)"
		charset.DecoderName = "gb18030"
	case 1:
		charset.DisplayName = "UTF-8 (UNICODE_FLAG=1)"
		charset.DecoderName = "utf-8"
	case 2:
		charset.DisplayName = "EUC-KR (UNICODE_FLAG=2)"
		charset.DecoderName = "euc-kr"
	default:
		charset.DisplayName = fmt.Sprintf("unknown (UNICODE_FLAG=%d)", flag)
	}
	return charset, true
}

func readSystemHeader(path string) ([]byte, int64, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, 0, err
	}
	defer file.Close()

	stat, err := file.Stat()
	if err != nil {
		return nil, 0, err
	}
	header := make([]byte, systemHeaderReadSize)
	n, err := file.Read(header)
	if err != nil {
		return nil, 0, err
	}
	if n < systemHeaderReadSize {
		return nil, 0, fmt.Errorf("SYSTEM.DBF header is too small")
	}
	return header, stat.Size(), nil
}

func loadDMIni(path string) (map[string]string, bool) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, false
	}
	result := make(map[string]string)
	for _, line := range strings.Split(string(raw), "\n") {
		if idx := strings.IndexByte(line, '#'); idx >= 0 {
			line = line[:idx]
		}
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key = strings.ToUpper(strings.TrimSpace(key))
		value = strings.TrimSpace(value)
		if key != "" {
			result[key] = value
		}
	}
	return result, true
}

func firstIniValue(values map[string]string, keys ...string) string {
	for _, key := range keys {
		if value := strings.TrimSpace(values[strings.ToUpper(key)]); value != "" {
			return value
		}
	}
	return ""
}

func compareIniUint(iniValue string, systemValue uint64, missing string) string {
	iniValue = strings.TrimSpace(iniValue)
	if iniValue == "" {
		return missing
	}
	value, err := strconv.ParseUint(iniValue, 10, 64)
	if err != nil {
		return "configured"
	}
	if value == systemValue {
		return "match"
	}
	return "mismatch"
}

func charsetIniMatchesFlag(iniValue string, flag uint8) bool {
	switch normalizeCharsetToken(iniValue) {
	case "0", "GB18030":
		return flag == 0
	case "1", "UTF-8":
		return flag == 1
	case "2", "EUC-KR":
		return flag == 2
	default:
		return false
	}
}

func normalizeCharsetToken(value string) string {
	value = strings.ToUpper(strings.TrimSpace(value))
	value = strings.TrimPrefix(value, "CHARSET=")
	value = strings.TrimPrefix(value, "UNICODE_FLAG=")
	value = strings.TrimSpace(value)
	value = strings.ReplaceAll(value, "_", "-")
	switch value {
	case "UTF8":
		return "UTF-8"
	case "EUCKR", "KR":
		return "EUC-KR"
	default:
		return value
	}
}

func systemDir(systemPath string) string {
	dir := filepath.Dir(systemPath)
	if dir == "." || dir == "" {
		return "."
	}
	return dir
}

func defaultIfEmpty(value string, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}

func readHeaderU32(header []byte, off int) uint32 {
	if len(header) < off+4 {
		return 0
	}
	return binary.LittleEndian.Uint32(header[off:])
}
