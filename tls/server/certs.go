package server

import _ "embed"

//go:embed cert-bundle.pem
var Bundle []byte

//go:embed key.pem
var Key []byte
