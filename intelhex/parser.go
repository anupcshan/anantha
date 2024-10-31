package intelhex

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
)

type Record struct {
	Length     uint8
	Offset     uint16
	RecType    uint8
	ReadOffset int64
	Body       []byte
}

type Parser struct {
	r io.Reader
	w io.WriterAt

	outputOffset         int64
	baseAddress          uint32
	disableCompactOutput bool

	Records []Record

	eof bool
}

type ParserOptions struct {
	disableCompactOutput bool
}

type ParserOption func(*ParserOptions)

func WithDisableCompactOutput() ParserOption {
	return func(o *ParserOptions) {
		o.disableCompactOutput = true
	}
}

func NewParser(r io.Reader, w io.WriterAt, opts ...ParserOption) *Parser {
	po := &ParserOptions{}
	for _, opt := range opts {
		opt(po)
	}
	return &Parser{
		r:                    r,
		w:                    w,
		disableCompactOutput: po.disableCompactOutput,
	}
}

// https://en.wikipedia.org/wiki/Intel_HEX#Format
func (p *Parser) ReadRecord() error {
	header := make([]byte, 5)
	if _, err := p.r.Read(header); err != nil {
		return err
	}

	if header[0] != ':' {
		return fmt.Errorf("Unexpected mark byte %x", header[0])
	}

	var length, recType uint8
	var offset uint16

	if err := binary.Read(bytes.NewReader(header[1:2]), binary.BigEndian, &length); err != nil {
		return err
	}
	if err := binary.Read(bytes.NewReader(header[2:4]), binary.BigEndian, &offset); err != nil {
		return err
	}
	if err := binary.Read(bytes.NewReader(header[4:5]), binary.BigEndian, &recType); err != nil {
		return err
	}

	body := make([]byte, length)

	if _, err := p.r.Read(body); err != nil {
		return err
	}

	var readOffset int64
	var copyBody bool

	switch recType {
	case 0:
		readOffset = int64(p.baseAddress) + int64(offset)
		if p.outputOffset == 0 && !p.disableCompactOutput {
			p.outputOffset = -readOffset
		}

		readOffset += p.outputOffset
		if _, err := p.w.WriteAt(body, readOffset); err != nil {
			return err
		}
	case 1:
		p.eof = true
	case 4:
		copyBody = true
		var baseAddrMSB uint16
		if err := binary.Read(bytes.NewReader(body), binary.BigEndian, &baseAddrMSB); err != nil {
			return err
		}
		p.baseAddress = uint32(baseAddrMSB) << 16
	case 5:
		copyBody = true
	default:
		return fmt.Errorf("Unknown record type %d", recType)
	}

	checksum := make([]byte, 1)

	if _, err := p.r.Read(checksum); err != nil {
		return err
	}

	var recordSum uint8
	for _, b := range header[1:] {
		recordSum += uint8(b)
	}
	for _, b := range body {
		recordSum += uint8(b)
	}

	computedChecksum := ^recordSum + 1 // 2's complement

	if uint8(checksum[0]) != computedChecksum {
		return fmt.Errorf("Mismatched checksum")
	}

	if !copyBody {
		body = nil
	}

	p.Records = append(p.Records, Record{
		Length:     length,
		Offset:     offset,
		RecType:    recType,
		ReadOffset: readOffset,
		Body:       body,
	})

	return nil
}

func (p *Parser) HasNext() bool {
	return !p.eof
}
