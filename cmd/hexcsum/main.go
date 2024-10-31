package main

import (
	"flag"
	"io"
	"log"
	"os"

	"github.com/anupcshan/anantha/intelhex"
)

type checksumWriter struct {
	checksum uint16
}

func (c *checksumWriter) WriteAt(bytes []byte, _ int64) (int, error) {
	for _, b := range bytes {
		c.checksum += uint16(b)
	}
	return len(bytes), nil
}

type fullChecksumWriter struct {
	checksum uint16
}

func (c *fullChecksumWriter) Write(bytes []byte) (int, error) {
	for _, b := range bytes {
		c.checksum += uint16(b)
	}
	return len(bytes), nil
}

func main() {
	flag.Parse()

	log.SetFlags(log.Lmicroseconds | log.Lshortfile)

	f, err := os.Open(flag.Arg(0))
	if err != nil {
		log.Fatal(err)
	}

	defer f.Close()

	fCh := &fullChecksumWriter{}

	w := &checksumWriter{}

	parser := intelhex.NewParser(io.TeeReader(f, fCh), w)
	for {
		if !parser.HasNext() {
			break
		}

		err := parser.ReadRecord()
		if err != nil {
			log.Fatal(err)
		}

		fCh.checksum -= 0x3A
	}

	log.Printf("Full checksum: %04x", fCh.checksum)
}
