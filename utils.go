package mnemosyne

import (
	"bytes"
	"compress/zlib"
	"io"
)

// compressZlib compresses data using zlib
func compressZlib(input []byte) ([]byte, error) {
	var buf bytes.Buffer
	w := zlib.NewWriter(&buf)

	if _, err := w.Write(input); err != nil {
		w.Close()
		return nil, err
	}

	if err := w.Close(); err != nil {
		return nil, err
	}

	return buf.Bytes(), nil
}

// decompressZlib decompresses zlib compressed data
func decompressZlib(input []byte) ([]byte, error) {
	var out bytes.Buffer
	r, err := zlib.NewReader(bytes.NewBuffer(input))
	if err != nil {
		return nil, err
	}

	if _, err := io.Copy(&out, r); err != nil {
		r.Close()
		return nil, err
	}

	if err := r.Close(); err != nil {
		return nil, err
	}

	return out.Bytes(), nil
}

// Backward compatibility functions
func CompressZlib(input []byte) []byte {
	compressed, _ := compressZlib(input)
	return compressed
}

func DecompressZlib(input []byte) []byte {
	decompressed, _ := decompressZlib(input)
	return decompressed
}
