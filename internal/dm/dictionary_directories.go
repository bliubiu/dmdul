package dm

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// Directories are global objects (DBA_DIRECTORIES.OWNER is SYS), not schemas.
type DictionaryDirectory struct {
	ID   uint32
	Name string
	Path string
}

func scanDictionaryDirectories(objects map[uint32]dictionaryObject) []DictionaryDirectory {
	result := make([]DictionaryDirectory, 0)
	for _, obj := range objects {
		if obj.Type == "DIR" && obj.Name != "" && obj.Valid != "N" {
			result = append(result, DictionaryDirectory{ID: obj.ID, Name: obj.Name, Path: obj.DirectoryPath})
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Name < result[j].Name })
	return result
}

func parseDirectoryPath(page []byte, pos int, decoder textDecoder) string {
	if pos < 0 {
		return ""
	}
	_, next, err := readShortDataBytes(page, pos) // INFO5
	if err != nil {
		return ""
	}
	raw, _, err := readShortDataBytes(page, next) // INFO6
	if err != nil {
		return ""
	}
	path, ok := decoder.decode(raw)
	if !ok || !safeDirectoryPath(path) {
		return ""
	}
	return path
}

func safeDirectoryPath(path string) bool {
	if path == "" {
		return false
	}
	for _, r := range path {
		if r < 32 || r == 127 {
			return false
		}
	}
	return true
}

func directoryDDL(dir DictionaryDirectory) (string, error) {
	if dir.Name == "" || !safeDirectoryPath(dir.Path) {
		return "", fmt.Errorf("directory %s path was not recovered; inspect directories.tsv", dir.Name)
	}
	return "CREATE OR REPLACE DIRECTORY " + quoteIdent(dir.Name) + " AS '" + strings.ReplaceAll(dir.Path, "'", "''") + "';", nil
}

func renderDirectories(out *strings.Builder, dirs []DictionaryDirectory) {
	if len(dirs) > 0 {
		out.WriteString("-- Directories: review server paths before import; directories are not created on disk.\n")
	}
	for _, dir := range dirs {
		sql, err := directoryDDL(dir)
		if err != nil {
			fmt.Fprintf(out, "-- WARNING: directory path missing for %s\n", quoteIdent(dir.Name))
			continue
		}
		out.WriteString(sql + "\n\n")
	}
}

func writeDictionaryDirectories(path string, dirs []DictionaryDirectory) error {
	rows := make([][]string, 0, len(dirs))
	for _, dir := range dirs {
		rows = append(rows, []string{formatUint32Field(dir.ID), dir.Name, dir.Path})
	}
	return writeTSV(path, []string{"directory_id", "directory_name", "directory_path"}, rows)
}

func readDictionaryDirectories(path string) ([]DictionaryDirectory, error) {
	rows, err := readTSV(path)
	if err != nil {
		return nil, err
	}
	result := make([]DictionaryDirectory, 0)
	for i, row := range rows {
		if i == 0 && len(row) > 0 && row[0] == "directory_id" {
			continue
		}
		if len(row) != 3 || row[1] == "" || (row[2] != "" && !safeDirectoryPath(row[2])) {
			return nil, fmt.Errorf("directories.tsv row %d: invalid directory", i+1)
		}
		id, err := strconv.ParseUint(row[0], 10, 32)
		if err != nil || id == 0 {
			return nil, fmt.Errorf("directories.tsv row %d: invalid id", i+1)
		}
		result = append(result, DictionaryDirectory{ID: uint32(id), Name: row[1], Path: row[2]})
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Name < result[j].Name })
	return result, nil
}
