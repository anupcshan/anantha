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
	"log"
	"math/big"
	"os"
	"runtime"
	"runtime/debug"
	"sort"
	"strings"
	"sync"
	"sync/atomic"

	"net/http"
	_ "net/http/pprof"

	"github.com/anupcshan/anantha/certs"
	"github.com/bits-and-blooms/bitset"
	"github.com/google/uuid"
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
	fslSets    []*bitset.BitSet
	fslCerts   []map[int]certs.Cert
	stats      struct {
		completedLens         int32
		checkedKeys           int64
		skippedKeys           int64
		matches               int32
		certsWithFSLMatch     [SliceLen]int64
		certPairsWithFSLMatch [SliceLen]int64
		matchesForLens        [SliceLen]int32
		checksForLens         [SliceLen]int64
		numSlicesNotMatched   [MaxSlices]int64
		firstMismatchedSlice  [MaxSlices]int64
	}
}

func (r *Result) AddCert(firstSliceLen int, private []byte, publicMangled []byte, public []byte) error {
	r.lock.Lock()
	defer r.lock.Unlock()
	return r.AddCertLocked(firstSliceLen, private, publicMangled, public)
}

func (r *Result) AddCertLocked(firstSliceLen int, private []byte, publicMangled []byte, public []byte) error {
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

type Set map[int]struct{}

func NewSet() Set {
	return make(map[int]struct{}, 128)
}

func NewRange(start, end int) Set {
	x := NewSet()
	for i := start; i <= end; i++ {
		x.Add(i)
	}

	return x
}

func (s Set) Add(x int) {
	s[x] = struct{}{}
}

func (s Set) Len() int {
	return len(s)
}

func (s Set) AsSlice() []int {
	values := make([]int, 0, len(s))

	for k := range s {
		values = append(values, k)
	}

	sort.Ints(values)

	return values
}

func (s Set) Minus(other Set) Set {
	x := NewSet()
	for k := range s {
		if _, ok := other[k]; !ok {
			x.Add(k)
		}
	}

	return x
}

func (s Set) AddAll(other Set) {
	for k := range other {
		s[k] = struct{}{}
	}
}

func writeResult(fName string, result *Result) error {
	b, err := json.Marshal(result)
	if err != nil {
		return err
	}

	return os.WriteFile(fName, b, 0755)
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
			},
		},
		"Verisign": {
			CertFile:   "Verisign.pem",
			ResultFile: "result-Verisign.json",
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
			},
		},
	}
)

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
		http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
			keySlice := missingKeys()
			w.WriteHeader(http.StatusOK)
			result.lock.RLock()
			count := len(result.fslSets)
			var copiedCertsMatched [SliceLen]int64
			for i := 0; i < SliceLen; i++ {
				copiedCertsMatched[i] = result.stats.certsWithFSLMatch[i]
			}
			var copiedCertPairsMatched [SliceLen]int64
			for i := 0; i < SliceLen; i++ {
				copiedCertPairsMatched[i] = result.stats.certPairsWithFSLMatch[i]
			}
			result.lock.RUnlock()
			var sb strings.Builder
			{
				var found int
				for i := SliceLen - 1; i >= 0 && found < 20; i-- {
					if copiedCertsMatched[i] > 0 {
						fmt.Fprintf(&sb, "[%d]->%d ", i, copiedCertsMatched[i])
						found++
					}
				}
			}
			var sbPairs strings.Builder
			{
				var found int
				for i := SliceLen - 1; i >= 0 && found < 20; i-- {
					if copiedCertPairsMatched[i] > 0 {
						fmt.Fprintf(&sbPairs, "[%d]->%d ", i, copiedCertPairsMatched[i])
						found++
					}
				}
			}
			fmt.Fprintf(
				w,
				"[%d/%d lens] Missing lens: %s, %d sets, topk: %s, toppairs: %s",
				atomic.LoadInt32(&result.stats.completedLens),
				SliceLen,
				humanizeRange(keySlice),
				count,
				sb.String(),
				sbPairs.String(),
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

				if result.FoundCount() == SliceLen && false {
					return nil
				}

				err := certsetup(blob, algo, result, logger)
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

func certsetup(blob []byte, algo Algo, result *Result, logger Logger) error {
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
			CommonName: uuid.NewString(),
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

	pk, _ := x509.MarshalPKCS8PrivateKey(genKey.GetPrivateKey())
	pemEncoded := pem.EncodeToMemory(&pem.Block{
		Type:  "PRIVATE KEY",
		Bytes: pk,
	})

	fslMatched := bitset.New(SliceLen)
	fslCerts := make(map[int]certs.Cert)

	for firstSliceLen := 0; firstSliceLen < SliceLen; firstSliceLen++ {
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
		stuffedInternalBytes, matched := verifyFixedBlobs(firstSliceLen, blob, newBlob, toStuff, result)
		if !matched {
			continue
		}
		if !fixupStuffBytes(firstSliceLen, blob, newBlob, toStuff-stuffedInternalBytes) {
			continue
		}
		fnms := firstNonMatchingSlice(firstSliceLen, blob, newBlob, result)
		if fnms == -1 {
			fslMatched.Set(uint(firstSliceLen))
			fslCerts[firstSliceLen] = certs.Cert{
				Public:        caPEM.Bytes(),
				PublicMangled: newBlob,
				Private:       pemEncoded,
			}
			if err := result.AddCert(firstSliceLen, pemEncoded, newBlob, caPEM.Bytes()); err != nil {
				log.Fatal(err)
			}
		}
	}

	if fslMatched.Count() > 0 {
		result.lock.Lock()
		atomic.AddInt64(&result.stats.certsWithFSLMatch[fslMatched.Count()], 1)
		result.fslSets = append(result.fslSets, fslMatched)
		result.fslCerts = append(result.fslCerts, fslCerts)
		if fslMatched.Count() >= SliceLen-10 {
			log.Printf("Matched %d FSL [%d count]", fslMatched.Count(), len(result.fslSets))
		}

		j := len(result.fslSets) - 1
		for i := 0; i < j; i++ {
			ikCount := result.fslSets[i].UnionCardinality(result.fslSets[j])

			if ikCount < SliceLen {
				atomic.AddInt64(&result.stats.certPairsWithFSLMatch[ikCount], 1)
			}

			if ikCount >= SliceLen {
				result.FoundCerts = make(map[int]certs.Cert)
				for firstSliceLen, cert := range result.fslCerts[i] {
					if err := result.AddCertLocked(firstSliceLen, cert.Private, cert.PublicMangled, cert.Public); err != nil {
						log.Fatal(err)
					}
				}
				for firstSliceLen, cert := range result.fslCerts[j] {
					if err := result.AddCertLocked(firstSliceLen, cert.Private, cert.PublicMangled, cert.Public); err != nil {
						log.Fatal(err)
					}
				}
				log.Fatalf("!!!!!! Len2: %d, i=%d [%d], j=%d [%d]", ikCount, i, result.fslSets[i].Count(), j, result.fslSets[j].Count())
			}
		}
		result.lock.Unlock()
	}

	return nil
}

func verifyFixedBlobs(firstSliceLen int, blob, newBlob []byte, stuffPrefixBytes int, result *Result) (int, bool) {
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

		// logger.Printf("Stuffed %d/%d bytes", stuffedInternalBytes, stuffPrefixBytes)
		if start >= stuffPrefixBytes-stuffedInternalBytes {
			var origChecksum, newChecksum uint8
			for i := start; i < end; i++ {
				origChecksum += blob[i]
				newChecksum += newBlob[i]
			}

			if origChecksum == newChecksum {
				matches++
				continue
			}

			var stuffedThisSlice int
			var crs int

			for {
				stuffedThisSlice++
				var foundAMatch bool
				for crs = 0; crs < stuffedThisSlice/2; crs++ {
					updatedChecksum := newChecksum
					for i := 0; i < stuffedThisSlice; i++ {
						updatedChecksum -= newBlob[start+i]
					}

					for cr := 0; cr < crs; cr++ {
						updatedChecksum += '\r'
					}

					for notcr := 0; notcr < stuffedThisSlice-crs; notcr++ {
						updatedChecksum += '\n'
					}

					if origChecksum == updatedChecksum {
						matches++
						foundAMatch = true
						break
					} else if stuffedThisSlice >= end-start || stuffedThisSlice >= stuffPrefixBytes-stuffedInternalBytes {
						// log.Printf("Unable to stuff this slice %d, prefix %d, internal %d", stuffedThisSlice, stuffPrefixBytes, stuffedInternalBytes)
						atomic.AddInt64(&result.stats.numSlicesNotMatched[slices-matches], 1)
						return 0, false
					}
				}

				if foundAMatch {
					break
				}
			}

			if stuffedThisSlice > 0 {
				// log.Printf("Stuffed %d bytes (copied %d:%d from %d:) (prefix: %d, so far: %d) [%d,%d)", stuffedThisSlice, stuffPrefixBytes-stuffedInternalBytes-stuffedThisSlice, start+stuffedThisSlice, stuffPrefixBytes-stuffedInternalBytes, stuffPrefixBytes, stuffedInternalBytes, start, end)
				copy(newBlob[stuffPrefixBytes-stuffedInternalBytes-stuffedThisSlice:start+stuffedThisSlice], newBlob[stuffPrefixBytes-stuffedInternalBytes:])

				var isCr bool
				for i := start; i < start+stuffedThisSlice; i++ {
					if isCr && crs > 0 {
						newBlob[i] = '\r'
						isCr = false
						crs--
					} else {
						newBlob[i] = '\n'
						isCr = true
					}
				}

				stuffedInternalBytes += stuffedThisSlice
			}
		}
	}

	atomic.AddInt64(&result.stats.numSlicesNotMatched[slices-matches], 1)
	// atomic.AddInt64(&result.stats.firstMismatchedSlice[firstMismatch+1], 1)
	return stuffedInternalBytes, true
}

func fixupStuffBytes(firstSliceLen int, blob, newBlob []byte, stuffPrefixBytes int) bool {
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
			// log.Printf("Attempting to emit avg %x (%x/%d) for %d in range %d-%d", avg, deltaChecksum, bytesToStuff, firstSliceLen, start, stuffPrefixBytes)
			if avg > 255 {
				// log.Printf("Unable to emit avg %d (%d/%d) for %d", avg, deltaChecksum, bytesToStuff, firstSliceLen)
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

				// log.Printf("Fixing up one slice earlier to emit avg %d (%d/%d) for %d", deltaChecksum/SliceLen, deltaChecksum, SliceLen, firstSliceLen)

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
				// log.Printf("Unable to emit deltaChecksum %d for %d", deltaChecksum, firstSliceLen)
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
		// log.Printf("Mismatch overall sum %x != %x (firstSliceLen %d)", origSum, newSum, firstSliceLen)
		atomic.AddInt64(&result.stats.firstMismatchedSlice[MaxSlices-1], 1)
		atomic.AddInt64(&result.stats.numSlicesNotMatched[slices-matches], 1)
		return 0
	}

	atomic.AddInt64(&result.stats.firstMismatchedSlice[firstMismatch+1], 1)
	return firstMismatch
}
