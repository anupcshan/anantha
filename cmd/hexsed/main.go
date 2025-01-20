package main

import (
	_ "embed"
	"encoding/json"
	"flag"
	"log"
	"os"

	"github.com/anupcshan/anantha/intelhex"
	"github.com/anupcshan/anantha/membuf"
)

var (
	//go:embed replace.json
	replaceJSON []byte
)

func main() {
	in := flag.String("in", "", "Input hex file")
	out := flag.String("out", "", "Output hex file")

	flag.Parse()

	log.SetFlags(log.Lmicroseconds | log.Lshortfile)

	f, err := os.Open(*in)
	if err != nil {
		log.Fatal(err)
	}

	var updateCfg membuf.UpdateConfig
	if err := json.Unmarshal(replaceJSON, &updateCfg); err != nil {
		log.Fatal(err)
	}

	buf := membuf.NewMemBuffer()
	parser := intelhex.NewParser(f, buf)
	for {
		if !parser.HasNext() {
			break
		}

		err := parser.ReadRecord()
		if err != nil {
			log.Fatal(err)
		}
	}
	_ = f.Close()

	buf.Update(&updateCfg, parser.Records)

	outF, err := os.OpenFile(*out, os.O_CREATE|os.O_RDWR|os.O_TRUNC, 0644)
	if err != nil {
		log.Fatal(err)
	}

	encoder := intelhex.NewEncoder(buf, outF, parser.Records)
	if err := encoder.EncodeRecords(); err != nil {
		log.Fatal(err)
	}
	_ = outF.Close()
}
