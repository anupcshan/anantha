package cagenresults

import (
	_ "embed"
	"encoding/json"

	"github.com/anupcshan/anantha/membuf"
)

var (
	//go:embed result-Verisign.json
	Results  []byte
	Verisign membuf.UpdateConfig
)

func init() {
	var result membuf.Result
	if err := json.Unmarshal(Results, &result); err != nil {
		panic(err)
	}

	Verisign = membuf.UpdateConfig{
		Result:      result,
		SearchBegin: "-----BEGIN CERTIFICATE-----\u000aMIIE0zCCA7ugAwIBAgIQGNrRniZ96LtKIVjNzGs7SjANBgkqhkiG9w0BAQUFADCB",
		SearchEnd:   "-----END CERTIFICATE-----\u000a",
	}
}
