package main

import (
	"crypto/ecdsa"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"flag"
	"log"
	"math/big"
	"os"
)

func main() {
	key1File := flag.String("key1", "", "Key 1")
	key2File := flag.String("key2", "", "Key 2 (used to sign cert with key 1)")
	certTemplateFile := flag.String("cert", "", "Cert template")
	flag.Parse()

	if *key1File == "" || *key2File == "" {
		log.Fatal("Both key1 and key2 flags are required")
	}

	key1Bytes, err := os.ReadFile(*key1File)
	if err != nil {
		log.Fatal(err)
	}

	key2Bytes, err := os.ReadFile(*key2File)
	if err != nil {
		log.Fatal(err)
	}

	certTemplateBytes, err := os.ReadFile(*certTemplateFile)
	if err != nil {
		log.Fatal(err)
	}

	// Decode PEM blocks
	key1Block, _ := pem.Decode(key1Bytes)
	if key1Block == nil {
		log.Fatal("Failed to decode key1 PEM block")
	}

	key2Block, _ := pem.Decode(key2Bytes)
	if key2Block == nil {
		log.Fatal("Failed to decode key2 PEM block")
	}

	// Parse private keys
	key1, err := x509.ParsePKCS8PrivateKey(key1Block.Bytes)
	if err != nil {
		log.Fatal("Failed to parse key1:", err)
	}

	key2, err := x509.ParsePKCS8PrivateKey(key2Block.Bytes)
	if err != nil {
		log.Fatal("Failed to parse key2:", err)
	}

	certTemplateBlock, _ := pem.Decode(certTemplateBytes)
	certTemplate, err := x509.ParseCertificate(certTemplateBlock.Bytes)
	if err != nil {
		log.Fatal(err)
	}

	// Create certificate template
	template := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject: pkix.Name{
			CommonName: "A",
		},
		NotBefore: certTemplate.NotBefore,
		NotAfter:  certTemplate.NotAfter,

		KeyUsage:              certTemplate.KeyUsage,
		BasicConstraintsValid: true,
		IsCA:                  true,
		SignatureAlgorithm:    x509.ECDSAWithSHA256,
	}

	// Create certificate using key1's public key and sign with key2
	derBytes, err := x509.CreateCertificate(rand.Reader, template, template, key1.(*ecdsa.PrivateKey).Public(), key2)
	if err != nil {
		log.Fatal("Failed to create certificate:", err)
	}

	// Encode certificate to PEM
	certPEM := pem.EncodeToMemory(&pem.Block{
		Type:  "CERTIFICATE",
		Bytes: derBytes,
	})

	// Write certificate to stdout
	os.Stdout.Write(certPEM)
}
