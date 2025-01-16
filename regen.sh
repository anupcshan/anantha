#!/bin/bash -eu

cat cmd/cagen/result-Verisign.json | jq '.FoundCerts[] | .Private + " " + .Public' -r | sort | uniq | head -n1 | grep -o '^[^ ]*' | base64 --decode > tls/ca/key.pem
cat cmd/cagen/result-Verisign.json | jq '.FoundCerts[] | .Private + " " + .Public' -r | sort | uniq | head -n1 | grep -o '[^ ]*$' | base64 --decode > tls/ca/cacert.pem

cat cmd/cagen/result-Verisign.json | jq '.FoundCerts[] | .Private + " " + .Public' -r | sort | uniq | tail -n1 | grep -o '^[^ ]*' | base64 --decode > tls/ca/key2.pem
cat cmd/cagen/result-Verisign.json | jq '.FoundCerts[] | .Private + " " + .Public' -r | sort | uniq | tail -n1 | grep -o '[^ ]*$' | base64 --decode > tls/ca/cacert2.pem
# cat cmd/cagen/result-Verisign.json | jq '.FoundCerts[].Public' -r | sort | uniq | head -n1 | base64 --decode > tls/ca/cacert.pem
# cat cmd/cagen/result-Verisign.json | jq '.FoundCerts[].Private' -r | sort | uniq | head -n1 | base64 --decode > tls/ca/key.pem
# 
# cat cmd/cagen/result-Verisign.json | jq '.FoundCerts[].Public' -r | sort | uniq | tail -n1 | base64 --decode > tls/ca/cacert2.pem
# cat cmd/cagen/result-Verisign.json | jq '.FoundCerts[].Private' -r | sort | uniq | tail -n1 | base64 --decode > tls/ca/key2.pem

cd cmd/signcert && CGO_ENABLED=0 go build && ./signcert -key1 ../../tls/ca/key.pem -key2 ../../tls/ca/key2.pem -cert ../../tls/ca/cacert.pem -parent-cert ../../tls/ca/cacert2.pem > ../../tls/ca/cross-cert1.pem && cd -

openssl x509 -req -days 500 -set_serial 01 \
   -in tls/server/server-req.pem \
   -out tls/server/cert.pem \
   -CA tls/ca/cacert.pem \
   -CAkey tls/ca/key.pem

cat tls/server/cert.pem tls/ca/cross-cert1.pem tls/ca/cacert2.pem > tls/server/cert-bundle.pem

set +e
# Verify
echo "===== Verify with cert 1"
openssl verify -verbose -CAfile tls/ca/cacert.pem tls/server/cert-bundle.pem
echo "===== Verify with cert 2"
openssl verify -verbose -CAfile tls/ca/cacert2.pem -untrusted tls/ca/cross-cert1.pem tls/server/cert-bundle.pem