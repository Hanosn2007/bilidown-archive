package util

import (
	"bytes"
	"compress/flate"
	"compress/gzip"
	"compress/zlib"
	"io"
	"strings"
)

func DecodeCompressedData(data []byte, encoding string) ([]byte, error) {
	encoding = strings.ToLower(strings.TrimSpace(encoding))
	switch encoding {
	case "gzip":
		return readGzip(data)
	case "deflate":
		return readDeflate(data)
	case "":
		if bytes.HasPrefix(bytes.TrimSpace(data), []byte("<")) {
			return data, nil
		}
		if decoded, err := readDeflate(data); err == nil {
			return decoded, nil
		}
		if decoded, err := readGzip(data); err == nil {
			return decoded, nil
		}
	}
	return data, nil
}

func readGzip(data []byte) ([]byte, error) {
	reader, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	defer reader.Close()
	return io.ReadAll(reader)
}

func readDeflate(data []byte) ([]byte, error) {
	reader, err := zlib.NewReader(bytes.NewReader(data))
	if err == nil {
		defer reader.Close()
		return io.ReadAll(reader)
	}
	rawReader := flate.NewReader(bytes.NewReader(data))
	defer rawReader.Close()
	return io.ReadAll(rawReader)
}
