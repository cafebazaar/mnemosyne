package mnemosyne

import (
	"bytes"
	"compress/zlib"
	"io"
	"strings"
)

func MakeKey(keys ...string) string {
	return strings.Join(keys, ";")
}

// func CompressLz4(input []byte) []byte {
// 	var buf bytes.Buffer
// 	w := lz4.NewWriter(&buf)
// 	w.Write(input)
// 	w.Close()
// 	compressed := buf.Bytes()
// 	return compressed
// }
// func DecompressLz4(input []byte) []byte {
// 	var out bytes.Buffer
// 	r := lz4.NewReader(bytes.NewBuffer(input))
// 	io.Copy(&out, r)
// 	original := out.Bytes()
// 	return original
// }

func CompressZlib(input []byte) []byte {
	var buf bytes.Buffer
	w := zlib.NewWriter(&buf)
	w.Write(input)
	w.Close()
	compressed := buf.Bytes()
	return compressed
}
func DecompressZlib(input []byte) []byte {
	var out bytes.Buffer
	r, _ := zlib.NewReader(bytes.NewBuffer(input))
	io.Copy(&out, r)
	r.Close()
	original := out.Bytes()
	return original
}
