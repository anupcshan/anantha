package membuf

import (
	"bytes"
	"fmt"
	"io"
	"log"
	"sort"

	"github.com/anupcshan/anantha/certs"
	"github.com/anupcshan/anantha/intelhex"
)

type UpdateConfig struct {
	SearchBegin string
	SearchEnd   string
	Result      Result
}

type replaceConfig struct {
	nextByte int
	str      []byte
}

func (r *replaceConfig) Emit() byte {
	if len(r.str) <= r.nextByte {
		panic(fmt.Errorf("reading past end of str for %+v", r))
	}

	b := r.str[r.nextByte]
	r.nextByte++

	return b
}

func (r *replaceConfig) IsComplete() bool {
	return len(r.str) <= r.nextByte
}

type sequentialReplacement struct {
	replacements []*replaceConfig
	currentIndex int
}

func (s *sequentialReplacement) Emit() byte {
	if s.currentIndex >= len(s.replacements) {
		panic("Trying to emit beyond last replacement")
	}

	if s.replacements[s.currentIndex].IsComplete() {
		s.currentIndex++
	}
	return s.replacements[s.currentIndex].Emit()
}

func (s *sequentialReplacement) IsComplete() bool {
	if s.currentIndex >= len(s.replacements) {
		return true
	}

	if s.replacements[s.currentIndex].IsComplete() {
		s.currentIndex++
	}

	if s.currentIndex >= len(s.replacements) {
		s.currentIndex--
		return true
	}
	return s.replacements[s.currentIndex].IsComplete()
}

type updateT struct {
	begin  int64
	length int64
	sRepl  *sequentialReplacement
}

type offsetBuffer struct {
	offset int64
	buf    []byte
	update *updateT
}

type memBuffer struct {
	buffers []*offsetBuffer
}

func NewMemBuffer() *memBuffer {
	return &memBuffer{}
}

func (m *memBuffer) findWriteBuffer(off int64) *offsetBuffer {
	for _, buf := range m.buffers {
		if buf.offset+int64(len(buf.buf)) == off {
			return buf
		}
	}

	return nil
}

func (m *memBuffer) WriteAt(p []byte, off int64) (int, error) {
	writeBuf := m.findWriteBuffer(off)
	if writeBuf == nil {
		writeBuf = &offsetBuffer{
			offset: off,
		}
		m.buffers = append(m.buffers, writeBuf)
		sort.Slice(m.buffers, func(i, j int) bool {
			return m.buffers[i].offset < m.buffers[j].offset
		})
	}

	writeBuf.buf = append(writeBuf.buf, p...)
	return len(p), nil
}

func checksum(p []byte) uint8 {
	var csum uint8
	for _, b := range p {
		csum += uint8(b)
	}

	return ^csum + 1
}

func min[T ~int64 | ~int](a, b T) T {
	if a < b {
		return a
	}

	return b
}

func max(a, b int64) int64 {
	if a > b {
		return a
	}

	return b
}

func updateWithPermutations(buf []byte, modifyRange []byte, targetChecksum uint8, update *updateT) error {
	for i := 0; !update.sRepl.IsComplete() && i < len(modifyRange); i++ {
		modifyRange[i] = update.sRepl.Emit()
	}

	// log.Printf("%x vs %x", checksum(buf), targetChecksum)
	if checksum(buf) == targetChecksum {
		return nil
	}

	return fmt.Errorf("checksum mismatch: %x vs %x", checksum(buf), targetChecksum)
}

func (m *memBuffer) ReadAt(p []byte, off int64) (int, error) {
	// log.Printf("Reading %d at offset %d", len(p), off)
	var pickBuffer *offsetBuffer
	for _, buf := range m.buffers {
		if off >= buf.offset {
			if off < buf.offset+int64(len(buf.buf)) {
				pickBuffer = buf
			}
		} else {
			break
		}
	}

	if pickBuffer == nil {
		return 0, fmt.Errorf("no pickbuffer")
	}

	blob := pickBuffer.buf
	offsetInBlob := off - pickBuffer.offset
	maxLen := int64(len(p))
	if maxLen > int64(len(pickBuffer.buf))-offsetInBlob {
		maxLen = int64(len(pickBuffer.buf)) - offsetInBlob
	}

	copy(p, blob[offsetInBlob:offsetInBlob+maxLen])

	origChecksum := checksum(p)

	if pickBuffer.update != nil {
		left := max(offsetInBlob, pickBuffer.update.begin)
		right := min(offsetInBlob+int64(len(p)), pickBuffer.update.begin+pickBuffer.update.length)

		if left < right {
			// log.Printf(
			// 	"Overlap detected: [%d, %d) & [%d, %d) -> [%d, %d)",
			// 	pickBuffer.update.begin,
			// 	pickBuffer.update.begin+pickBuffer.update.length,
			// 	offsetInBlob,
			// 	offsetInBlob+int64(len(p)),
			// 	left,
			// 	right,
			// )

			permute := p[left-offsetInBlob : right-offsetInBlob]

			if err := updateWithPermutations(p, permute, origChecksum, pickBuffer.update); err != nil {
				return 0, err
			}
		}
	}

	// log.Printf("Returning %d bytes from %d", maxLen, nextBufIndex)
	if int(maxLen) < len(p) {
		more, err := m.ReadAt(p[maxLen:], off+maxLen)
		return more + int(maxLen), err
	} else {
		return int(maxLen), nil
	}
}

func (m *memBuffer) Reader() io.Reader {
	var lastByte int64
	for _, buf := range m.buffers {
		curLB := buf.offset + int64(len(buf.buf))
		if curLB > lastByte {
			lastByte = curLB
		}
	}

	// log.Printf("Length: %d", lastByte)

	return io.NewSectionReader(m, 0, lastByte)
}

type Result struct {
	FoundCerts map[int]certs.Cert
}

func (m *memBuffer) Update(updateCfg *UpdateConfig, records []intelhex.Record) {
	for _, buf := range m.buffers {
		begin := bytes.Index(buf.buf, []byte(updateCfg.SearchBegin))
		if begin == -1 {
			continue
		}

		length := bytes.Index(buf.buf[begin:], []byte(updateCfg.SearchEnd)) + len(updateCfg.SearchEnd)

		// log.Printf("Begin: %d (%d%%128), offset: %d, length: %d", begin, begin%128, buf.offset, length)

		firstSliceLen := -1
		for _, rec := range records {
			if rec.ReadOffset >= buf.offset+int64(begin) {
				firstSliceLen = int(rec.ReadOffset - buf.offset - int64(begin))
				log.Printf("Found update section with firstSliceLen: %d", firstSliceLen)
				break
			}
		}

		if firstSliceLen < 0 {
			panic("Can't find valid record corresponding to found string")
		}

		var replacements []*replaceConfig

		replacements = append(replacements, &replaceConfig{
			str: updateCfg.Result.FoundCerts[firstSliceLen].PublicMangled,
		})

		if len(replacements[0].str) == 0 {
			log.Fatalf("No replacement available for firstSliceLen: %d", firstSliceLen)
		}

		buf.update = &updateT{
			begin:  int64(begin),
			length: int64(length),
			sRepl: &sequentialReplacement{
				replacements: replacements,
			},
		}
	}
}

var _ io.WriterAt = (*memBuffer)(nil)
var _ io.ReaderAt = (*memBuffer)(nil)
