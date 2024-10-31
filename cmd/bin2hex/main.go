package main

import (
	"encoding/json"
	"flag"
	"log"
	"os"

	"github.com/anupcshan/anantha/intelhex"
)

func main() {
	in := flag.String("in", "", "Input bin file")
	out := flag.String("out", "", "Output hex file")

	flag.Parse()

	log.SetFlags(log.Lmicroseconds | log.Lshortfile)

	f, err := os.Open(*in)
	if err != nil {
		log.Fatal(err)
	}

	recordsF, err := os.Open(*in + ".records")
	if err != nil {
		log.Fatal(err)
	}

	var records []intelhex.Record
	dec := json.NewDecoder(recordsF)
	if err := dec.Decode(&records); err != nil {
		log.Fatal(err)
	}
	_ = recordsF.Close()

	log.Printf("Read %d records", len(records))

	outF, err := os.OpenFile(*out, os.O_CREATE|os.O_RDWR|os.O_TRUNC, 0644)
	if err != nil {
		log.Fatal(err)
	}

	encoder := intelhex.NewEncoder(f, outF, records)
	if err := encoder.EncodeRecords(); err != nil {
		log.Fatal(err)
	}

	_ = outF.Close()
}
