package dm

import (
	"bufio"
	"compress/zlib"
	"encoding/binary"
	"fmt"
	"io"
	"os"

	"github.com/golang/snappy"
)

const (
	maxHugeDecodedSectionBytes = 256 << 20
	maxHugeSnappyBlockBytes    = 64 << 20
)

func (reader *hugeColumnSectionReader) close() {
	if reader.scratch != nil {
		name := reader.scratch.Name()
		_ = reader.scratch.Close()
		_ = os.Remove(name)
		reader.scratch = nil
	}
	if reader.file != nil {
		_ = reader.file.Close()
		reader.file = nil
	}
}

func (reader *hugeColumnSectionReader) prepareCompressedSection(header []byte) error {
	flags := binary.LittleEndian.Uint32(header[12:])
	if flags == 0 {
		if reader.meta.cprFlag == "Y" {
			return fmt.Errorf("HFS compression flag differs from AUX")
		}
		return nil
	}
	if flags != 1 {
		return fmt.Errorf("unsupported HFS section encoding flags=0x%X", flags)
	}
	if reader.meta.cprFlag != "Y" {
		return fmt.Errorf("HFS compression flag differs from AUX")
	}
	length := binary.LittleEndian.Uint32(header[8:])
	if length <= uint32(hugeHFSSectionHeaderSize) || length > maxHugeDecodedSectionBytes {
		return fmt.Errorf("HFS decoded section length %d exceeds bounds", length)
	}
	input := io.NewSectionReader(reader.file, reader.meta.offset+hugeHFSSectionHeaderSize,
		int64(reader.meta.nlen)-hugeHFSSectionHeaderSize)
	envelope := make([]byte, 5)
	if _, err := io.ReadFull(input, envelope); err != nil {
		return err
	}
	if envelope[0] != 1 || binary.LittleEndian.Uint32(envelope[1:]) != length-uint32(hugeHFSSectionHeaderSize) {
		return fmt.Errorf("unsupported HFS compression envelope or decoded length mismatch")
	}
	scratch, err := os.CreateTemp("", "dmdul-hfs-*")
	if err != nil {
		return fmt.Errorf("create HFS decompression scratch: %w", err)
	}
	reader.scratch = scratch
	if _, err := scratch.Write(header); err != nil {
		return err
	}
	if err := decodeHugeCompressedBody(input, scratch, length-uint32(hugeHFSSectionHeaderSize)); err != nil {
		return err
	}
	reader.data = scratch
	reader.meta.offset = 0
	reader.meta.nlen = length
	return nil
}

// ZIP sections contain a zlib stream. Snappy sections contain a little-endian
// compressed size followed by one raw Snappy block. Only the observed envelope
// is accepted; unknown transforms (including decimal packing) are not guessed.
func decodeHugeCompressedBody(input io.Reader, output io.Writer, expected uint32) error {
	if expected == 0 || expected > maxHugeDecodedSectionBytes {
		return fmt.Errorf("invalid HFS decoded size %d", expected)
	}
	buffer := bufio.NewReader(input)
	prefix, err := buffer.Peek(2)
	if err != nil {
		return err
	}
	if prefix[0] == 0x78 && (uint16(prefix[0])<<8|uint16(prefix[1]))%31 == 0 {
		stream, err := zlib.NewReader(buffer)
		if err != nil {
			return fmt.Errorf("HFS ZIP header: %w", err)
		}
		defer stream.Close()
		if _, err := io.CopyN(output, stream, int64(expected)); err != nil {
			return fmt.Errorf("HFS ZIP payload: %w", err)
		}
		var extra [1]byte
		n, err := stream.Read(extra[:])
		if n != 0 || err != io.EOF {
			return fmt.Errorf("HFS ZIP size/checksum mismatch: extra=%d error=%v", n, err)
		}
	} else {
		var size [4]byte
		if _, err := io.ReadFull(buffer, size[:]); err != nil {
			return err
		}
		compressed := binary.LittleEndian.Uint32(size[:])
		if compressed == 0 || compressed > maxHugeSnappyBlockBytes || expected > maxHugeSnappyBlockBytes || uint64(compressed) > uint64(snappy.MaxEncodedLen(int(expected))) {
			return fmt.Errorf("HFS Snappy block exceeds 64 MiB limit")
		}
		raw, err := io.ReadAll(io.LimitReader(buffer, int64(compressed)))
		if err != nil {
			return err
		}
		if len(raw) != int(compressed) {
			return io.ErrUnexpectedEOF
		}
		decodedSize, err := snappy.DecodedLen(raw)
		if err != nil || decodedSize != int(expected) {
			return fmt.Errorf("HFS Snappy decoded length mismatch: size=%d expected=%d error=%v", decodedSize, expected, err)
		}
		data, err := snappy.Decode(nil, raw)
		if err != nil {
			return fmt.Errorf("HFS Snappy payload: %w", err)
		}
		if _, err := output.Write(data); err != nil {
			return err
		}
	}
	// The physical section is 4 KiB aligned. Remaining bytes must be padding,
	// not an unrecognized second stream or trailing data silently discarded.
	var padding [4096]byte
	for {
		n, err := buffer.Read(padding[:])
		for _, b := range padding[:n] {
			if b != 0 {
				return fmt.Errorf("nonzero HFS compression padding")
			}
		}
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
	}
}
