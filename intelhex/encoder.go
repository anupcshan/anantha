package intelhex

import (
	"bytes"
	"encoding/binary"
	"io"
)

type Encoder struct {
	r io.ReaderAt
	w io.Writer

	Records []Record
}

func NewEncoder(r io.ReaderAt, w io.Writer, records []Record) *Encoder {
	return &Encoder{
		r:       r,
		w:       w,
		Records: records,
	}
}

func (e *Encoder) EncodeRecords() error {
	for _, record := range e.Records {
		if err := e.encodeRecord(record); err != nil {
			return err
		}
	}

	return nil
}

func (e *Encoder) encodeRecord(r Record) error {
	header := new(bytes.Buffer)
	if err := binary.Write(header, binary.BigEndian, r.Length); err != nil {
		return err
	}
	if err := binary.Write(header, binary.BigEndian, r.Offset); err != nil {
		return err
	}
	if err := binary.Write(header, binary.BigEndian, r.RecType); err != nil {
		return err
	}

	body := make([]byte, r.Length)

	switch r.RecType {
	case 0:
		if _, err := e.r.ReadAt(body, r.ReadOffset); err != nil {
			return err
		}
	case 4, 5:
		body = r.Body
	}

	if _, err := e.w.Write([]byte{':'}); err != nil {
		return err
	}
	if _, err := e.w.Write(header.Bytes()); err != nil {
		return err
	}
	if _, err := e.w.Write(body); err != nil {
		return err
	}

	var recordSum uint8
	for _, b := range header.Bytes() {
		recordSum += uint8(b)
	}
	for _, b := range body {
		recordSum += uint8(b)
	}
	computedChecksum := ^recordSum + 1 // 2's complement
	if err := binary.Write(e.w, binary.BigEndian, computedChecksum); err != nil {
		return err
	}

	return nil
}
