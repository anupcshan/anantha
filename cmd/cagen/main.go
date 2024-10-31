package main

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"flag"
	"fmt"
	"io"
	"log"
	"math"
	"math/big"
	"os"
	"runtime"
	"runtime/debug"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"net/http"
	_ "net/http/pprof"

	"github.com/anupcshan/anantha/certs"
	"github.com/pkg/errors"
	"golang.org/x/exp/maps"
	"golang.org/x/sync/errgroup"
)

const (
	SliceLen     = 128
	MaxSlices    = 15
	MaxFreeBytes = 500
)

type Result struct {
	lock  sync.RWMutex
	fName string

	FoundCerts map[int]certs.Cert
	stats      struct {
		completedLens        int32
		checkedKeys          int64
		skippedKeys          int64
		matches              int32
		matchesForLens       [SliceLen]int32
		checksForLens        [SliceLen]int64
		numSlicesNotMatched  [MaxSlices]int64
		firstMismatchedSlice [MaxSlices]int64
	}
}

func (r *Result) AddCert(firstSliceLen int, private []byte, publicMangled []byte, public []byte) error {
	r.lock.Lock()
	defer r.lock.Unlock()

	atomic.AddInt32(&r.stats.matchesForLens[firstSliceLen], 1)
	atomic.AddInt32(&r.stats.matches, 1)
	if _, ok := r.FoundCerts[firstSliceLen]; ok {
		// Don't overwrite existing result
		return nil
	}

	atomic.AddInt32(&r.stats.completedLens, 1)
	r.FoundCerts[firstSliceLen] = certs.Cert{
		Public:        public,
		PublicMangled: publicMangled,
		Private:       private,
	}

	return writeResult(r.fName, r)
}

func (r *Result) FoundCount() int {
	r.lock.RLock()
	defer r.lock.RUnlock()
	return len(r.FoundCerts)
}

func (r *Result) FoundKeys() []int {
	r.lock.RLock()
	defer r.lock.RUnlock()

	return maps.Keys(r.FoundCerts)
}

func (r *Result) Found(firstSliceLen int) bool {
	r.lock.RLock()
	defer r.lock.RUnlock()

	_, ok := r.FoundCerts[firstSliceLen]
	return ok
}

func writeResult(fName string, result *Result) error {
	f, err := os.OpenFile(fName, os.O_RDWR|os.O_CREATE, 0755)
	if err != nil {
		return err
	}
	enc := json.NewEncoder(f)
	enc.SetIndent("", "  ")
	if err := enc.Encode(result); err != nil {
		return err
	}

	return f.Close()
}

func loadResult(fName string) (*Result, error) {
	f, err := os.Open(fName)
	if err != nil {
		if os.IsNotExist(err) {
			return &Result{
				FoundCerts: make(map[int]certs.Cert),
				fName:      fName,
			}, nil
		}
		return nil, err
	}

	var result Result
	dec := json.NewDecoder(f)
	if err := dec.Decode(&result); err != nil {
		return nil, err
	}
	_ = f.Close()

	result.stats.completedLens = int32(len(result.FoundCerts))
	result.fName = fName
	return &result, nil
}

func humanizeRange(r []int) string {
	if len(r) == 0 {
		return ""
	}

	var b strings.Builder

	var startRange = r[0]
	var endRange = r[0]
	for _, v := range r[1:] {
		if v == endRange+1 {
			endRange = v
			continue
		} else {
			if b.Len() > 0 {
				fmt.Fprint(&b, ", ")
			}
			if startRange != endRange {
				fmt.Fprintf(&b, "%d-%d", startRange, endRange)
			} else {
				fmt.Fprintf(&b, "%d", startRange)
			}
			startRange = v
			endRange = v
		}
	}

	if b.Len() > 0 {
		fmt.Fprint(&b, ", ")
	}
	if startRange != endRange {
		fmt.Fprintf(&b, "%d-%d", startRange, endRange)
	} else {
		fmt.Fprintf(&b, "%d", startRange)
	}

	return b.String()
}

type Key interface {
	GetPublicKey() any
	GetPrivateKey() any
}

type ECDSAPrivateKey struct {
	key *ecdsa.PrivateKey
}

func (e ECDSAPrivateKey) GetPublicKey() any {
	return &e.key.PublicKey
}

func (e ECDSAPrivateKey) GetPrivateKey() any {
	return e.key
}

type RSAPrivateKey struct {
	key *rsa.PrivateKey
}

func (r RSAPrivateKey) GetPublicKey() any {
	return &r.key.PublicKey
}

func (r RSAPrivateKey) GetPrivateKey() any {
	return r.key
}

type Algo struct {
	CertFile             string
	ResultFile           string
	GenerateKey          func() (Key, error)
	StuffExtraExtensions func() []pkix.Extension
	SignatureAlgorithm   x509.SignatureAlgorithm
}

var (
	Algos = map[string]Algo{
		"256": {
			CertFile:   "amzn-cert-256.pem",
			ResultFile: "result-256.json",
			GenerateKey: func() (Key, error) {
				caPrivKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
				if err != nil {
					return nil, err
				}
				return ECDSAPrivateKey{key: caPrivKey}, nil
			},
			StuffExtraExtensions: func() []pkix.Extension { return nil },
		},
		"384": {
			CertFile:   "amzn-cert-384.pem",
			ResultFile: "result-384.json",
			GenerateKey: func() (Key, error) {
				caPrivKey, err := ecdsa.GenerateKey(elliptic.P384(), rand.Reader)
				if err != nil {
					return nil, err
				}
				return ECDSAPrivateKey{key: caPrivKey}, nil
			},
			StuffExtraExtensions: func() []pkix.Extension { return nil },
		},
		"CA1": {
			CertFile:   "AmazonRootCA1.pem",
			ResultFile: "result-CA1.json",
			GenerateKey: func() (Key, error) {
				// caPrivKey, err := rsa.GenerateKey(rand.Reader, 2048)
				// if err != nil {
				// 	return nil, err
				// }
				// return RSAPrivateKey{key: caPrivKey}, nil
				caPrivKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
				if err != nil {
					return nil, err
				}
				return ECDSAPrivateKey{key: caPrivKey}, nil
			},
			SignatureAlgorithm: x509.ECDSAWithSHA256,
			StuffExtraExtensions: func() []pkix.Extension {
				return nil
				// value := make([]byte, 319) // 319 for P384, 380 for P256, 248 for RSA1024
				// _, _ = rand.Read(value)
				// return []pkix.Extension{
				// 	{
				// 		Id: asn1.ObjectIdentifier{
				// 			1, 3,
				// 		},
				// 		Value: value,
				// 	},
				// }
			},
		},
		"Starfield": {
			CertFile:   "Starfield.pem",
			ResultFile: "result-Starfield.json",
			GenerateKey: func() (Key, error) {
				caPrivKey, err := rsa.GenerateKey(rand.Reader, 2048)
				if err != nil {
					return nil, err
				}
				return RSAPrivateKey{key: caPrivKey}, nil
				// caPrivKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
				// if err != nil {
				// 	return nil, err
				// }
				// return ECDSAPrivateKey{key: caPrivKey}, nil
			},
			// SignatureAlgorithm: x509.ECDSAWithSHA256,
			StuffExtraExtensions: func() []pkix.Extension {
				return nil
			},
		},
		"Verisign": {
			CertFile:   "Verisign.pem",
			ResultFile: "result-Verisign.json",
			GenerateKey: func() (Key, error) {
				caPrivKey, err := rsa.GenerateKey(rand.Reader, 2048)
				if err != nil {
					return nil, err
				}
				return RSAPrivateKey{key: caPrivKey}, nil
			},
			// SignatureAlgorithm: x509.ECDSAWithSHA256,
			StuffExtraExtensions: func() []pkix.Extension {
				return nil
			},
		},
	}
)

func humanizeDuration(minutes float64) string {
	if minutes < 60 {
		return fmt.Sprintf("%.2f mins", minutes)
	} else if minutes < 60*24 {
		return fmt.Sprintf("%.2f hrs", minutes/60)
	} else if minutes < 60*24*7 {
		return fmt.Sprintf("%.2f days", minutes/(60*24))
	} else if minutes < 60*24*365 {
		return fmt.Sprintf("%.2f wks", minutes/(60*24*7))
	} else {
		return fmt.Sprintf("%.2f yrs", minutes/(60*24*265))
	}
}

func main() {
	algoName := flag.String("algo", "256", "Algorithm (256 or 384 or CA1)")
	flag.Parse()

	debug.SetGCPercent(1000) // Trade off memory for less GC
	log.SetFlags(log.Lshortfile | log.Lmicroseconds)

	algo, algoOk := Algos[*algoName]
	if !algoOk {
		log.Fatalf("Unknown algo %s", *algoName)
	}

	blob := Must(os.ReadFile(algo.CertFile))
	result := Must(loadResult(algo.ResultFile))
	log.Printf("Loaded result with %d completed certs", len(result.FoundCerts))

	eg, egCtx := errgroup.WithContext(context.Background())

	logger := newThrottlingLogger(log.Default())

	missingKeys := func() []int {
		keySlice := result.FoundKeys()
		keys := map[int]struct{}{}
		for _, key := range keySlice {
			keys[key] = struct{}{}
		}
		keySlice = []int{}
		for i := 0; i < SliceLen; i++ {
			if _, ok := keys[i]; !ok {
				keySlice = append(keySlice, i)
			}
		}

		return keySlice
	}

	go func() {
		startTime := time.Now()
		tick := time.NewTicker(time.Second)
		for range tick.C {
			since := time.Since(startTime)
			keySlice := missingKeys()

			checkedKeys := float64(atomic.LoadInt64(&result.stats.checkedKeys))

			var lensNotMatched string
			var fastestMinutes float64 = math.MaxFloat64
			var exponent float64 = 1
			for i := 0; i < MaxSlices; i++ {
				if i > 0 {
					exponent *= 256
				}
				matched := atomic.LoadInt64(&result.stats.numSlicesNotMatched[i])
				if matched > 0 {
					if i > 0 {
						rate := since.Minutes() / (float64(matched) / exponent)
						if rate < fastestMinutes {
							fastestMinutes = rate
						}
					}
					partial := fmt.Sprintf("%d:%d", i, matched)
					if len(lensNotMatched) > 0 {
						lensNotMatched = lensNotMatched + " " + partial
					} else {
						lensNotMatched = partial
					}
				}
			}

			lensNotMatched = fmt.Sprintf("%s fst: %s", lensNotMatched, humanizeDuration(fastestMinutes))

			var firstMismatchedSlice string
			for i := 0; i < MaxSlices; i++ {
				matched := atomic.LoadInt64(&result.stats.firstMismatchedSlice[i])
				if matched > 0 {
					partial := fmt.Sprintf("%d:%.2f", i, float64(matched)/checkedKeys)
					if len(firstMismatchedSlice) > 0 {
						firstMismatchedSlice = firstMismatchedSlice + " " + partial
					} else {
						firstMismatchedSlice = partial
					}
				}
			}

			// var slicesChecked string
			// for i := 0; i < SliceLen; i++ {
			// 	checked := atomic.LoadInt64(&result.stats.checksForLens[i])
			// 	if checked > 0 {
			// 		partial := fmt.Sprintf("%d:%d", i, checked)
			// 		if len(slicesChecked) > 0 {
			// 			slicesChecked = slicesChecked + " " + partial
			// 		} else {
			// 			slicesChecked = partial
			// 		}
			// 	}
			// }

			fmt.Printf(
				"\r%.2f/s (%.2f skip/s) [%d/%d lens] [%.2f certs/min] Partial mismatches: [%s] First mismatched: [%s] Missing lens: %s     ",
				checkedKeys/since.Seconds(),
				float64(atomic.LoadInt64(&result.stats.skippedKeys))/since.Seconds(),
				atomic.LoadInt32(&result.stats.completedLens),
				SliceLen,
				float64(atomic.LoadInt32(&result.stats.matches))/since.Minutes(),
				lensNotMatched,
				firstMismatchedSlice,
				humanizeRange(keySlice),
			)
		}
	}()

	go func() {
		http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
			keySlice := missingKeys()
			w.WriteHeader(http.StatusOK)
			fmt.Fprintf(
				w,
				"[%d/%d lens] Missing lens: %s",
				atomic.LoadInt32(&result.stats.completedLens),
				SliceLen,
				humanizeRange(keySlice),
			)
		})
		log.Fatal(http.ListenAndServe(":6060", nil))
	}()

	for i := 0; i < runtime.NumCPU(); i++ {
		eg.Go(func() error {
			for {
				select {
				case <-egCtx.Done():
					return nil
				default:
				}

				if result.FoundCount() == SliceLen {
					return nil
				}

				err := certsetup(blob, algo, SliceLen, result, logger)
				if err != nil {
					return err
				}
			}
		})
	}

	log.Println(eg.Wait())
}

func Must[T any](t T, err error) T {
	if err != nil {
		panic(err)
	}
	return t
}

type throttlingLogger struct {
	*log.Logger

	seenMsgsLock sync.Mutex
	seenMsgs     map[string]struct{}
}

func newThrottlingLogger(w *log.Logger) *throttlingLogger {
	return &throttlingLogger{
		Logger:   w,
		seenMsgs: make(map[string]struct{}),
	}
}

func (t *throttlingLogger) Printf(format string, v ...any) {
	msg := fmt.Sprintf(format, v...)

	t.seenMsgsLock.Lock()
	defer t.seenMsgsLock.Unlock()

	if _, ok := t.seenMsgs[msg]; ok {
		return
	}

	t.seenMsgs[msg] = struct{}{}
	t.Logger.Printf(format, v...)
}

type Logger interface {
	Printf(format string, v ...any)
}

func indicateUpdated(ipToByteFlippedIndex *[MaxFreeBytes]map[int]struct{}, ipIdx int, blobIdx int) {
	mp := ipToByteFlippedIndex[ipIdx]
	if mp == nil {
		mp = make(map[int]struct{})
		ipToByteFlippedIndex[ipIdx] = mp
	}

	mp[blobIdx] = struct{}{}
}

type ipByte struct {
	ipIdx int
}

func generateOutputByteToInputIPIndex(ipToByteFlippedIndex [MaxFreeBytes]map[int]struct{}) map[int][]ipByte {
	index := make(map[int]map[ipByte]struct{})
	for ipIdx := 0; ipIdx < len(ipToByteFlippedIndex); ipIdx++ {
		for touched := range ipToByteFlippedIndex[ipIdx] {
			t := index[touched]
			if t == nil {
				t = make(map[ipByte]struct{})
				index[touched] = t
			}

			t[ipByte{ipIdx}] = struct{}{}
		}
	}

	result := make(map[int][]ipByte)

	for touched, causes := range index {
		if len(causes) > 10 {
			// Most likely a signature byte, which is mutated by a lot of sources
			continue
		}

		for cause := range causes {
			result[touched] = append(result[touched], cause)
		}
	}

	return result
}

func analyze(blob []byte, algo Algo) (map[int][]ipByte, error) {
	block, _ := pem.Decode(blob)
	ca, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return nil, err
	}

	ca.PublicKey = nil
	if algo.SignatureAlgorithm != x509.UnknownSignatureAlgorithm {
		ca.SignatureAlgorithm = algo.SignatureAlgorithm
	}

	genKey, err := algo.GenerateKey()
	if err != nil {
		return nil, err
	}

	ca.ExtraExtensions = algo.StuffExtraExtensions()

	stableRand := make([]byte, 1*1024*1024)
	_, _ = rand.Reader.Read(stableRand)

	var ipToByteFlippedIndex [MaxFreeBytes]map[int]struct{}

	if len(ca.ExtraExtensions) == 0 {
		return map[int][]ipByte{}, nil
	}

	for ipIdx := 0; ipIdx < len(ca.ExtraExtensions[0].Value); ipIdx++ {
		for val := 0; val < 256; val = val<<1 + 1 {
			ca.ExtraExtensions[0].Value[ipIdx] = byte(val)

			caBytes, err := x509.CreateCertificate(newSignerRandReader(stableRand), ca, ca, genKey.GetPublicKey(), genKey.GetPrivateKey())
			if err != nil {
				return nil, errors.Wrap(err, "failed to create certificate")
			}

			caPEM := new(bytes.Buffer)
			_ = pem.Encode(caPEM, &pem.Block{
				Type:  "CERTIFICATE",
				Bytes: caBytes,
			})

			if caPEM.Len() != len(blob) {
				log.Printf("Bad length, expected %d got %d", len(blob), caPEM.Len())
				continue
			}

			blob = caPEM.Bytes()

			ca.ExtraExtensions[0].Value[ipIdx] = byte(val + 1)

			caBytes2, err := x509.CreateCertificate(newSignerRandReader(stableRand), ca, ca, genKey.GetPublicKey(), genKey.GetPrivateKey())
			if err != nil {
				return nil, errors.Wrap(err, "failed to create certificate")
			}
			caPEM2 := new(bytes.Buffer)
			_ = pem.Encode(caPEM2, &pem.Block{
				Type:  "CERTIFICATE",
				Bytes: caBytes2,
			})

			if caPEM2.Len() != len(blob) {
				log.Printf("Bad length, expected %d got %d", len(blob), caPEM2.Len())
				continue
			}
			blob2 := caPEM2.Bytes()
			for blobIdx := 0; blobIdx < len(blob); blobIdx++ {
				if blob[blobIdx] != blob2[blobIdx] {
					indicateUpdated(&ipToByteFlippedIndex, ipIdx, blobIdx)
				}
			}
		}
	}

	return generateOutputByteToInputIPIndex(ipToByteFlippedIndex), nil
}

func certsetup(blob []byte, algo Algo, sliceLen int, result *Result, logger Logger) error {
	block, _ := pem.Decode(blob)
	parsedCA, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return err
	}

	serialNumberLimit := new(big.Int).Lsh(big.NewInt(1), 32)
	serialNumber, err := rand.Int(rand.Reader, serialNumberLimit)
	if err != nil {
		log.Fatalf("Failed to generate serial number: %v", err)
	}

	ca := &x509.Certificate{
		SerialNumber: serialNumber,
		Subject: pkix.Name{
			CommonName: "A",
		},
		NotBefore: parsedCA.NotBefore,
		NotAfter:  parsedCA.NotAfter,

		KeyUsage:              parsedCA.KeyUsage,
		BasicConstraintsValid: true,
		IsCA:                  true,
	}

	if algo.SignatureAlgorithm != x509.UnknownSignatureAlgorithm {
		ca.SignatureAlgorithm = algo.SignatureAlgorithm
	}

	genKey, err := algo.GenerateKey()
	if err != nil {
		return err
	}

	// stableRand := make([]byte, 1*1024*1024)
	// _, _ = rand.Reader.Read(stableRand)

	// create the CA
	caBytes, err := x509.CreateCertificate(rand.Reader, ca, ca, genKey.GetPublicKey(), genKey.GetPrivateKey())
	if err != nil {
		return errors.Wrap(err, "failed to create certificate")
	}

	// pem encode
	caPEM := new(bytes.Buffer)
	_ = pem.Encode(caPEM, &pem.Block{
		Type:  "CERTIFICATE",
		Bytes: caBytes,
	})

	toStuff := len(blob) - caPEM.Len()
	if toStuff < 0 {
		atomic.AddInt64(&result.stats.skippedKeys, 1)
		logger.Printf("Expected %d, got %d", len(blob), caPEM.Len())
		return nil
	}

	for firstSliceLen := 0; firstSliceLen < SliceLen; firstSliceLen++ {
		if result.Found(firstSliceLen) {
			continue
		}
		atomic.AddInt64(&result.stats.checkedKeys, 1)
		atomic.AddInt64(&result.stats.checksForLens[firstSliceLen], 1)

		newBlob := make([]byte, len(blob))
		copy(newBlob, blob)
		copy(newBlob[toStuff:], caPEM.Bytes())
		logger.Printf("Stuffing %d bytes", toStuff)
		if firstSliceLen > 2 {
			newBlob[0]++
			newBlob[1]--
		} else {
			newBlob[3]++
			newBlob[4]--
		}
		stuffedInternalBytes, matched := verifyFixedBlobs(firstSliceLen, blob, newBlob, toStuff, result, logger)
		if !matched {
			continue
		}
		if !fixupStuffBytes(firstSliceLen, blob, newBlob, toStuff-stuffedInternalBytes, result) {
			log.Println("Unable to fixup")
			continue
		} else {
			log.Printf("Fixed up for %d!", firstSliceLen)
		}
		fnms := firstNonMatchingSlice(firstSliceLen, blob, newBlob, result)
		if fnms == -1 {
			pk, _ := x509.MarshalPKCS8PrivateKey(genKey.GetPrivateKey())
			pemEncoded := pem.EncodeToMemory(&pem.Block{
				Type:  "PRIVATE KEY",
				Bytes: pk,
			})

			if err := result.AddCert(firstSliceLen, pemEncoded, newBlob, caPEM.Bytes()); err != nil {
				log.Fatal(err)
			}
		}
	}

	return nil
}

func verifyFixedBlobs(firstSliceLen int, blob []byte, newBlob []byte, stuffPrefixBytes int, result *Result, logger Logger) (int, bool) {
	start := 0
	matches := 0
	slices := 0
	stuffedInternalBytes := 0

	for end := firstSliceLen; start < len(blob); end += SliceLen {
		if end >= len(blob) {
			break
		}
		start = end
		slices++
	}

	for ; start >= 0; start -= SliceLen {
		end := start + SliceLen
		if end > len(blob) {
			end = len(blob)
		}

		if start >= stuffPrefixBytes-stuffedInternalBytes {
			var origChecksum, newChecksum uint8
			for i := start; i < end; i++ {
				origChecksum += blob[i]
				newChecksum += newBlob[i]
			}

			var stuffedThisSlice int

			for {
				if origChecksum == newChecksum {
					matches++
					break
				} else if stuffedThisSlice >= end-start || stuffedThisSlice >= stuffPrefixBytes-stuffedInternalBytes {
					// log.Printf("Unable to stuff this slice %d, prefix %d, internal %d", stuffedThisSlice, stuffPrefixBytes, stuffedInternalBytes)
					atomic.AddInt64(&result.stats.numSlicesNotMatched[slices-matches], 1)
					return 0, false
				}

				newChecksum -= newBlob[start+stuffedThisSlice]
				newChecksum += '\n'
				stuffedThisSlice++
			}

			if stuffedThisSlice > 0 {
				// log.Printf("Stuffed %d bytes (copied %d:%d from %d:) (prefix: %d, so far: %d) [%d,%d)", stuffedThisSlice, stuffPrefixBytes-stuffedInternalBytes-stuffedThisSlice, start+stuffedThisSlice, stuffPrefixBytes-stuffedInternalBytes, stuffPrefixBytes, stuffedInternalBytes, start, end)
				copy(newBlob[stuffPrefixBytes-stuffedInternalBytes-stuffedThisSlice:start+stuffedThisSlice], newBlob[stuffPrefixBytes-stuffedInternalBytes:])

				for i := start; i < start+stuffedThisSlice; i++ {
					newBlob[i] = '\n'
				}

				stuffedInternalBytes += stuffedThisSlice
			}
		}
	}

	atomic.AddInt64(&result.stats.numSlicesNotMatched[slices-matches], 1)
	// atomic.AddInt64(&result.stats.firstMismatchedSlice[firstMismatch+1], 1)
	return stuffedInternalBytes, true
}

func fixupStuffBytes(firstSliceLen int, blob []byte, newBlob []byte, stuffPrefixBytes int, result *Result) bool {
	start := 0

	for end := firstSliceLen; start < len(blob); end += SliceLen {
		if end > len(blob) {
			end = len(blob)
		}

		if start < stuffPrefixBytes && end > stuffPrefixBytes {
			var deltaChecksum uint16
			bytesToStuff := uint16(stuffPrefixBytes - start)
			for i := start; i < len(blob); i++ {
				deltaChecksum += uint16(blob[i])
				if i >= stuffPrefixBytes {
					deltaChecksum -= uint16(newBlob[i])
				}
			}

			avg := deltaChecksum / bytesToStuff
			log.Printf("Attempting to emit avg %x (%x/%d) for %d in range %d-%d", avg, deltaChecksum, bytesToStuff, firstSliceLen, start, stuffPrefixBytes)
			if avg > 255 {
				log.Printf("Unable to emit avg %d (%d/%d) for %d", avg, deltaChecksum, bytesToStuff, firstSliceLen)
				for i := start; i < stuffPrefixBytes-1; i++ {
					newBlob[i] = 255
					deltaChecksum -= 255
				}

				newBlob[stuffPrefixBytes-1] = byte(deltaChecksum % 256)
				deltaChecksum -= uint16(newBlob[stuffPrefixBytes-1])

				start -= SliceLen
				end -= SliceLen

				if start < 0 {
					return false
				}

				log.Printf("Fixing up one slice earlier to emit avg %d (%d/%d) for %d", deltaChecksum/SliceLen, deltaChecksum, SliceLen, firstSliceLen)

				for i := start; i < end; i++ {
					newBlob[i] += byte(deltaChecksum / SliceLen)
				}
				return true
			}

			for i := start; i < stuffPrefixBytes-1; i++ {
				newBlob[i] = byte(avg)
				deltaChecksum -= avg
			}

			if deltaChecksum > 255 {
				log.Printf("Unable to emit deltaChecksum %d for %d", deltaChecksum, firstSliceLen)
				return false
			}
			newBlob[stuffPrefixBytes-1] = byte(deltaChecksum)

			return true
		}
		start = end
	}

	return false
}

func firstNonMatchingSlice(firstSliceLen int, blob []byte, newBlob []byte, result *Result) int {
	start := 0
	matches := 0
	slices := 0
	firstMismatch := -1

	var origSum, newSum uint16

	for end := firstSliceLen; start < len(blob); end += SliceLen {
		if end > len(blob) {
			end = len(blob)
		}

		var origChecksum, newChecksum uint8
		for i := start; i < end; i++ {
			origChecksum += uint8(blob[i])
			newChecksum += uint8(newBlob[i])

			origSum += uint16(blob[i])
			newSum += uint16(newBlob[i])
		}

		if origChecksum == newChecksum {
			matches++
		} else {
			log.Printf("Mismatch in %d, %d (%x != %x, range %d-%d) (firstSliceLen %d)", matches, slices, origChecksum, newChecksum, start, end, firstSliceLen)
			if firstMismatch == -1 {
				firstMismatch = matches
			}
		}
		slices++
		start = end
	}

	if slices != matches {
		atomic.AddInt64(&result.stats.numSlicesNotMatched[slices-matches], 1)
	}

	if firstMismatch == -1 && origSum != newSum {
		log.Printf("Mismatch overall sum %x != %x (firstSliceLen %d)", origSum, newSum, firstSliceLen)
		atomic.AddInt64(&result.stats.firstMismatchedSlice[MaxSlices-1], 1)
		atomic.AddInt64(&result.stats.numSlicesNotMatched[slices-matches], 1)
		return 0
	}

	atomic.AddInt64(&result.stats.firstMismatchedSlice[firstMismatch+1], 1)
	return firstMismatch
}

func permute(blob []byte, ca *x509.Certificate, result *Result, stableRand []byte, genKey Key, firstSliceLen int, minMatches int, logger Logger, byteIndex map[int][]ipByte) error {
	if result.Found(firstSliceLen) {
		return nil
	}

	start := firstSliceLen + SliceLen*(minMatches-1)
	end := start + SliceLen

	for blobIdx := start; blobIdx < end; blobIdx++ {
		for _, update := range byteIndex[blobIdx] {
			if result.Found(firstSliceLen) {
				return nil
			}

			save := ca.ExtraExtensions[0].Value[update.ipIdx]

			var foundAtLeastOneLonger bool

			for i := byte(0); i < 255 && !foundAtLeastOneLonger; i++ {
				ca.ExtraExtensions[0].Value[update.ipIdx] = save + i

				caBytes, err := x509.CreateCertificate(newSignerRandReader(stableRand), ca, ca, genKey.GetPublicKey(), genKey.GetPrivateKey())
				if err != nil {
					return errors.Wrap(err, "failed to create certificate")
				}

				// pem encode
				caPEM := new(bytes.Buffer)
				_ = pem.Encode(caPEM, &pem.Block{
					Type:  "CERTIFICATE",
					Bytes: caBytes,
				})

				if len(blob) != caPEM.Len() {
					atomic.AddInt64(&result.stats.skippedKeys, 1)
					logger.Printf("Expected %d, got %d", len(blob), caPEM.Len())
				} else {
					atomic.AddInt64(&result.stats.checkedKeys, 1)
					atomic.AddInt64(&result.stats.checksForLens[firstSliceLen], 1)

					fnms := firstNonMatchingSlice(firstSliceLen, blob, caPEM.Bytes(), result)
					if fnms == -1 {
						pk, _ := x509.MarshalPKCS8PrivateKey(genKey.GetPrivateKey())
						pemEncoded := pem.EncodeToMemory(&pem.Block{
							Type:  "PRIVATE KEY",
							Bytes: pk,
						})

						if err := result.AddCert(firstSliceLen, pemEncoded, caPEM.Bytes(), caPEM.Bytes()); err != nil {
							return errors.Wrap(err, "Could not add cert")
						}
					}

					if fnms > minMatches {
						// Worth pursing further
						if err := permute(blob, ca, result, stableRand, genKey, firstSliceLen, fnms, logger, byteIndex); err != nil {
							return errors.Wrap(err, "Error sub-permute")
						}
						foundAtLeastOneLonger = true
					}
				}
			}
			ca.ExtraExtensions[0].Value[update.ipIdx] = save
		}
	}

	return nil
}

type signerRandReader struct {
	r        io.Reader
	notFirst bool
}

func newSignerRandReader(b []byte) *signerRandReader {
	return &signerRandReader{
		r: bytes.NewReader(b),
	}
}

func (s *signerRandReader) Read(b []byte) (int, error) {
	if !s.notFirst {
		s.notFirst = true
		if len(b) == 1 {
			b[0] = '\x00'
			return 1, nil
		}
	}

	return s.r.Read(b)
}
