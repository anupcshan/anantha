package main

import (
	"encoding/json"
	"flag"
	"io"
	"log"
	"os"

	"github.com/anupcshan/anantha/intelhex"
	"github.com/anupcshan/anantha/membuf"
)

func main() {
	in := flag.String("in", "", "Input hex file")
	out := flag.String("out", "", "Output bin file")

	flag.Parse()

	log.SetFlags(log.Lmicroseconds | log.Lshortfile)

	f, err := os.Open(*in)
	if err != nil {
		log.Fatal(err)
	}

	var outF io.WriterAt
	var opts []intelhex.ParserOption
	mbuf := membuf.NewMemBuffer()

	if *out == "" {
		outF = mbuf
		opts = append(opts, intelhex.WithDisableCompactOutput())
	} else {
		var err error
		outF, err = os.OpenFile(*out, os.O_CREATE|os.O_RDWR|os.O_TRUNC, 0644)
		if err != nil {
			log.Fatal(err)
		}
	}

	parser := intelhex.NewParser(f, outF, opts...)
	for {
		if !parser.HasNext() {
			break
		}

		err := parser.ReadRecord()
		if err != nil {
			log.Fatal(err)
		}
	}

	if *out != "" {
		_ = outF.(io.Closer).Close()

		recordsF, err := os.OpenFile(*out+".records", os.O_CREATE|os.O_RDWR|os.O_TRUNC, 0644)
		if err != nil {
			log.Fatal(err)
		}

		enc := json.NewEncoder(recordsF)
		if err := enc.Encode(parser.Records); err != nil {
			log.Fatal(err)
		}
		_ = recordsF.Close()
	} else {
		_, _ = io.Copy(os.Stdout, mbuf.Reader())
	}
}
