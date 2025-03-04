package util

import (
	"bytes"
	"errors"
	"math"
	"sync"

	"braces.dev/errtrace"
)

var bytesRdrPool = sync.Pool{
	New: func() any { return bytes.NewReader(nil) },
}

func GetBytesReader(b []byte) *bytes.Reader {
	r := bytesRdrPool.Get().(*bytes.Reader) //nolint:forcetypeassert
	r.Reset(b)
	return r
}

func FreeBytesReader(r *bytes.Reader) {
	r.Reset(nil)
	bytesRdrPool.Put(r)
}

var bytesBufPool = &sync.Pool{
	New: func() any { return bytes.NewBuffer(make([]byte, 0, 64)) },
}

func GetBytesBuffer() *bytes.Buffer {
	return bytesBufPool.Get().(*bytes.Buffer) //nolint:forcetypeassert
}

func FreeBytesBuffer(b *bytes.Buffer) {
	b.Reset()
	if b.Cap() > math.MaxUint16 {
		return
	}
	bytesBufPool.Put(b)
}

const maxVarintBytes = 10

var (
	ErrUnexpectedEOF    = errors.New("unexpected end of data")
	ErrMalformedUvarint = errors.New("malformed uvarint")
)

func SizePrefixedString[T ~string | ~[]byte](val T) int {
	return SizeUVarInt(uint64(len(val))) + len(val)
}

func AppendPrefixedString[T ~string | ~[]byte](buf []byte, val T) []byte {
	buf = AppendUVarInt(buf, uint64(len(val)))
	return append(buf, val...)
}

func SizeUVarInt(val uint64) int {
	size := 1
	for val >= 0x80 {
		size++
		val >>= 7
	}
	return size
}

func AppendUVarInt(buf []byte, val uint64) []byte {
	for val >= 0x80 {
		buf = append(buf, byte(val)|0x80)
		val >>= 7
	}
	return append(buf, byte(val))
}

func ConsumePrefixedString(data []byte) (string, []byte, error) {
	length, consumed, err := readUVarInt(data)
	if err != nil {
		return "", nil, errtrace.Wrap(err)
	}
	if consumed > len(data) {
		return "", nil, errtrace.Wrap(ErrUnexpectedEOF)
	}
	if length > uint64(len(data[consumed:])) {
		return "", nil, errtrace.Wrap(ErrUnexpectedEOF)
	}
	start := consumed
	end := start + int(length)
	return string(data[start:end]), data[end:], nil
}

func ConsumeUVarInt(data []byte) (uint64, []byte, error) {
	val, consumed, err := readUVarInt(data)
	if err != nil {
		return 0, nil, errtrace.Wrap(err)
	}
	if consumed > len(data) {
		return 0, nil, errtrace.Wrap(ErrUnexpectedEOF)
	}
	return val, data[consumed:], nil
}

func readUVarInt(data []byte) (uint64, int, error) {
	var (
		value uint64
		shift uint
	)

	for i, b := range data {
		if i == maxVarintBytes {
			return 0, i + 1, errtrace.Wrap(ErrMalformedUvarint)
		}

		value |= uint64(b&0x7f) << shift
		if b&0x80 == 0 {
			return value, i + 1, nil
		}
		shift += 7
	}

	return 0, len(data), errtrace.Wrap(ErrUnexpectedEOF)
}
