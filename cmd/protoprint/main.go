package main

import (
	"fmt"
	"io"
	"log"
	"os"
	"strings"

	"google.golang.org/protobuf/encoding/protowire"
)

func main() {
	b, err := io.ReadAll(os.Stdin)
	if err != nil {
		log.Fatal(err)
	}

	for _, field := range parseUnknown(b) {
		fmt.Printf("%s\n", field)
	}
}

type Field struct {
	Tag Tag
	Val Val
}

func (f Field) String() string {
	return f.string(0)
}

func (f Field) string(indent int) string {
	var prefix string
	for i := 0; i < indent; i++ {
		prefix += "  "
	}

	switch f.Val.Payload.(type) {
	case []Field:
		subfields := f.Val.Payload.([]Field)
		str := &strings.Builder{}

		if len(subfields) == 0 { // Special case
			fmt.Fprintf(str, "%s%d {}", prefix, f.Tag.Num)
			return str.String()
		}

		fmt.Fprintf(str, "%s%d {\n", prefix, f.Tag.Num)

		for idx, sf := range subfields {
			suffix := "\n"
			if idx == len(subfields)-1 {
				suffix = ""
			}
			fmt.Fprintf(str, "%s%s", sf.string(indent+1), suffix)
		}

		fmt.Fprintf(str, "\n%s}", prefix)
		return str.String()
	case []uint8:
		return fmt.Sprintf(`%s%d: "%s"`, prefix, f.Tag.Num, f.Val.Payload)
	default:
		return fmt.Sprintf("%s%d: %v", prefix, f.Tag.Num, f.Val.Payload)
	}
}

type Tag struct {
	Num  int32
	Type protowire.Type
}

type Val struct {
	Payload interface{}
	Length  int
}

// https://stackoverflow.com/a/69510141
func parseUnknown(b []byte) []Field {
	fields := make([]Field, 0)
	for len(b) > 0 {
		n, t, fieldlen := protowire.ConsumeField(b)
		if fieldlen < 1 {
			return nil
		}
		field := Field{
			Tag: Tag{Num: int32(n), Type: t},
		}

		_, _, taglen := protowire.ConsumeTag(b[:fieldlen])
		if taglen < 1 {
			return nil
		}

		var (
			v    interface{}
			vlen int
		)
		switch t {
		case protowire.VarintType:
			v, vlen = protowire.ConsumeVarint(b[taglen:fieldlen])

		case protowire.Fixed64Type:
			v, vlen = protowire.ConsumeFixed64(b[taglen:fieldlen])

		case protowire.BytesType:
			v, vlen = protowire.ConsumeBytes(b[taglen:fieldlen])
			sub := parseUnknown(v.([]byte))
			if sub != nil {
				v = sub
			}

		case protowire.StartGroupType:
			v, vlen = protowire.ConsumeGroup(n, b[taglen:fieldlen])
			sub := parseUnknown(v.([]byte))
			if sub != nil {
				v = sub
			}

		case protowire.Fixed32Type:
			v, vlen = protowire.ConsumeFixed32(b[taglen:fieldlen])
		}

		if vlen < 1 {
			return nil
		}

		field.Val = Val{Payload: v, Length: vlen - taglen}
		// fmt.Printf("%#v\n", field)

		fields = append(fields, field)
		b = b[fieldlen:]
	}
	return fields
}
