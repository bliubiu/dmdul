package storage

import (
	"encoding/hex"
	"fmt"
	"io"
	"os"
)

const defaultSampleSize = 128

type InspectResult struct {
	Path   string
	Size   int64
	Sample []byte
}

func InspectFile(path string, sampleSize int) (*InspectResult, error) {
	if sampleSize <= 0 {
		sampleSize = defaultSampleSize
	}

	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open file: %w", err)
	}
	defer file.Close()

	stat, err := file.Stat()
	if err != nil {
		return nil, fmt.Errorf("stat file: %w", err)
	}

	sample := make([]byte, sampleSize)
	n, err := io.ReadFull(file, sample)
	if err != nil && err != io.ErrUnexpectedEOF && err != io.EOF {
		return nil, fmt.Errorf("read sample: %w", err)
	}
	sample = sample[:n]

	return &InspectResult{
		Path:   path,
		Size:   stat.Size(),
		Sample: sample,
	}, nil
}

func (r *InspectResult) HexDump() string {
	return hex.Dump(r.Sample)
}
