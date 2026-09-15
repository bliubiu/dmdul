package dm

import (
	"encoding/binary"
	"fmt"
	"os"
)

type hugeColumnFileKey struct {
	colID  uint16
	fileID int32
}

// AUX FILE_ID combines the HFS path index (high byte) and the column file
// sequence (low 24 bits). The filename only contains the sequence. Validate
// the full file identity rather than inferring path order from directory names.
func resolveHugeColumnFiles(dirs []string, table dictionaryObject, sections []hugeColumnSection) (map[hugeColumnFileKey]string, error) {
	files := make(map[hugeColumnFileKey]string)
	for _, section := range sections {
		if section.fileID < 0 || section.count == 0 {
			continue
		}
		key := hugeColumnFileKey{section.colID, section.fileID}
		if files[key] != "" {
			continue
		}
		for _, dir := range dirs {
			path, err := findHugeColumnFile(dir, key.colID, key.fileID)
			if err != nil {
				continue
			}
			stat, err := os.Lstat(path)
			if err != nil {
				return nil, err
			}
			if !stat.Mode().IsRegular() {
				return nil, fmt.Errorf("HFS column file must be a regular offline file: %s", path)
			}
			file, err := os.Open(path)
			if err != nil {
				return nil, err
			}
			var header [16]byte
			_, err = file.ReadAt(header[:], 0)
			file.Close()
			if err != nil {
				return nil, fmt.Errorf("HFS identity %s: %w", path, err)
			}
			if binary.LittleEndian.Uint16(header[:]) != uint16(table.Info2) ||
				binary.LittleEndian.Uint32(header[2:]) != table.SchemaID ||
				binary.LittleEndian.Uint32(header[6:]) != table.ID ||
				binary.LittleEndian.Uint16(header[10:]) != key.colID ||
				binary.LittleEndian.Uint32(header[12:]) != uint32(key.fileID) {
				continue
			}
			if previous := files[key]; previous != "" {
				return nil, fmt.Errorf("ambiguous HFS column file colid=%d file_id=%d: %s and %s", key.colID, key.fileID, previous, path)
			}
			files[key] = path
		}
		if files[key] == "" {
			return nil, fmt.Errorf("no matching HFS file identity for %s.%s colid=%d file_id=%d path_index=%d sequence=%d", table.Owner, table.Name, key.colID, key.fileID, uint32(key.fileID)>>24, uint32(key.fileID)&0xFFFFFF)
		}
	}
	return files, nil
}
