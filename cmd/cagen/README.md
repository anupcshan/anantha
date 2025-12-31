# cagen: Certificate Authority Generator

cagen is a brute-force tool that generates custom CA certificates capable of replacing
the original CA certificates in Carrier Infinity thermostat firmware while preserving
firmware checksum validation.

## The Problem

Carrier Infinity thermostats (firmware v4.17+) communicate with Carrier's cloud via
AWS IoT MQTT. The AWS IoT client libraries embedded in the firmware have a hardcoded
list of trusted CA certificates (Amazon Root CA, Verisign, Starfield).

To redirect thermostat traffic to a local Anantha server, we need to replace these
certificates in the firmware with our own CA. However, the firmware validates data
integrity using checksums computed over 128-byte slices. Simply swapping certificates
breaks this validation.

## Firmware Checksum Validation

The firmware is stored in Intel HEX format, which has two levels of checksums:

1. **8-bit checksums per record**: Each 128-byte record has a `uint8` checksum
2. **16-bit checksum for the whole file**: A `uint16` sum of all bytes

```
Record 0: bytes[0:128]     → uint8 checksum
Record 1: bytes[128:256]   → uint8 checksum
Record 2: bytes[256:384]   → uint8 checksum
...
Whole file                 → uint16 checksum
```

For a certificate replacement to work, the new certificate must match **both**:
- Every 8-bit record checksum
- The 16-bit whole-file checksum

## The firstSliceLen Problem

The certificate blob doesn't necessarily start at a 128-byte boundary in the firmware.
It can start at any offset within a slice. This offset is called `firstSliceLen`:

```
firstSliceLen = 0:    |cert bytes.........|cert bytes.........|
                      |<--- slice 0 ----->|<--- slice 1 ----->|

firstSliceLen = 50:   |....|cert bytes....|cert bytes.........|
                      |<-->|<-- slice 0 ->|<--- slice 1 ----->|
                       50 bytes from
                       preceding data
```

The same certificate bytes produce **different slice checksums** depending on this
alignment. A certificate that works at `firstSliceLen=0` may fail at `firstSliceLen=50`.

Since `firstSliceLen` can be any value from 0 to 127, we need certificates that work
for all 128 possible alignments.

## The Stuffing Algorithm

When a generated certificate doesn't match the original's checksum for a slice, cagen
attempts to "stuff" the certificate with whitespace characters (`\r` and `\n`) to
adjust the checksum.

The generated certificate is smaller than the original, so we need padding. Here's the
step-by-step transformation:

**Step 1: Initial placement** - Copy original blob, then overlay new cert at the end:

```
Original:  |-----BEGIN CERTIFICATE-----(original cert data)-----END CERT-----|
           |<---------------------------- N bytes ---------------------------->|

After:     |-----BEGIN CERTIF...(original junk)...|-----BEGIN...(new cert)...|
           |<------------ toStuff --------------->|<-------- shorter -------->|
           |<---------------------------- N bytes ---------------------------->|
```

**Step 2: Header corruption** - Break the `-----BEGIN` pattern in the padding area so
PEM parsers don't match the leftover header:

```
Before:    |-----BEGIN CERTIF...(original junk)...|-----BEGIN...(new cert)...|
After:     |.----BEGIN CERTIF...(original junk)...|-----BEGIN...(new cert)...|
            ^
            corrupted (++/--)
```

**Step 3: verifyFixedBlobs** - Work backwards through 128-byte slices, inserting `\r`/`\n`
whitespace at slice boundaries to balance checksums. Certificate content shifts left:

```
Before:    |.----...(junk)..................|-----BEGIN CERTIFICATE-----(cert)...|
           |<----------- toStuff ---------->|                                     |

After:     |.----.(junk)..|\r\n|--BEGIN CE|\r\n|RTIFICATE---|\r\n|(cert data)...|
           |<-- shrunk -->|    |<-- cert shifted left, broken across slices -->|
                          ^    ^            ^
                          whitespace inserted at slice boundaries
```

**Step 4: fixupStuffBytes** - Calculate arbitrary byte values for remaining padding area
to hit the exact checksum needed for slices that span the padding/cert boundary:

```
Before:    |.----.(junk)..|\r\n|--BEGIN CE|\r\n|...
           |<- still junk>|

After:     |xAxBxCxDxExFxG|\r\n|--BEGIN CE|\r\n|...
           |<- calculated>|
           values chosen so slice checksums match original
```

**Step 5: firstNonMatchingSlice** - Verify all slice checksums match. If any don't match,
this `firstSliceLen` fails and we try the next one.

The stuffing algorithm searches for combinations of `\r` (0x0D = 13) and `\n` (0x0A = 10)
that produce the exact checksum needed. The 3-value difference between them provides
flexibility to hit different targets.

This works because PEM-encoded certificates ignore extra whitespace.

### Why the `\r`/`\n` pattern matters

The thermostat firmware uses mbedtls for TLS. mbedtls's base64 decoder has specific
rules for whitespace (see `library/base64.c` in mbedtls):

- `\n` alone is valid (standalone LF, always skipped)
- `\r\n` together is valid (CRLF pair, skipped as a unit)
- `\r` alone is **invalid** (causes `MBEDTLS_ERR_BASE64_INVALID_CHARACTER`)

This means:
- Consecutive `\n\n\n...` is fine (each is a valid standalone LF)
- Consecutive `\r\r` would fail (first `\r` not followed by `\n`)

The code generates either pure `\n` sequences or alternating `\n\r\n\r...` patterns,
ensuring every `\r` is immediately followed by `\n` to form a valid CRLF pair.

## The Len2 Strategy

Finding a **single** certificate that works for all 128 `firstSliceLen` values is
extremely unlikely due to the cryptographic randomness in certificate generation.

Instead, cagen uses a two-certificate strategy:

1. **Generate certificates and track partial coverage**: Each generated certificate
   is tested against all 128 `firstSliceLen` values. The set of matching values is
   recorded in a bitset.

2. **Find complementary pairs**: After each generation, check if any two certificates
   together cover all 128 values:
   ```
   if cert_i.coverage ∪ cert_j.coverage = {0, 1, 2, ..., 127}
   ```

3. **Store both certificates**: When a complete pair is found, save both certificates.
   Each `firstSliceLen` uses whichever certificate from the pair covers it.

This approach is necessary because:
- Due to how certificates are generated, it's relatively common (<0.01% of attempts) to find
  a single cert covering 118-120 out of 128 values
- However, the **overlap between certificates is extremely high** - most certs cover nearly
  the same set of `firstSliceLen` values
- The search requires generating a large number of high-coverage certs until two are found
  with complementary coverage (low overlap) that together span all 128 values

## Cross-Signing for Serving

With two CA certificates, the server needs to work regardless of which certificate
the thermostat's firmware contains. This is solved with **cross-signing**.

### The Certificate Chain

```
┌─────────────────────────────────────────────────────────────┐
│                     Thermostat Firmware                      │
│                                                              │
│   Depending on firstSliceLen, contains either:               │
│   • A certificate from CA1's coverage set, OR                │
│   • A certificate from CA2's coverage set                    │
└─────────────────────────────────────────────────────────────┘
                              │
                              │ trusts
                              ▼
┌─────────────────────────────────────────────────────────────┐
│                        CA1 or CA2                            │
│                     (embedded in firmware)                   │
└─────────────────────────────────────────────────────────────┘
                              │
        ┌─────────────────────┴─────────────────────┐
        │                                           │
        ▼                                           ▼
┌───────────────────┐                    ┌───────────────────┐
│       CA1         │                    │       CA2         │
│   (self-signed)   │                    │   (self-signed)   │
└───────────────────┘                    └───────────────────┘
        │                                           │
        │                                           │
        ▼                                           ▼
┌───────────────────┐                    ┌───────────────────┐
│   Server Cert     │◄───────────────────│   Cross-Cert      │
│  (signed by CA1)  │   signed by CA2    │ (CA1 signed by    │
│                   │   creating trust   │     CA2's key)    │
└───────────────────┘       bridge       └───────────────────┘
```

### How It Works

1. **Generate cross-certificate**: CA1's certificate is signed by CA2's private key,
   creating `cross-cert1`

2. **Sign server cert with CA1**: The server's TLS certificate is signed by CA1

3. **Build certificate bundle**: Server presents:
   ```
   server-cert.pem + cross-cert1.pem + ca2.pem
   ```

4. **Trust validation**:
   - If thermostat trusts CA1 → validates server cert directly
   - If thermostat trusts CA2 → validates cross-cert1 → validates server cert

This single server configuration works for **all 128 possible firmware alignments**.

## File Structure

```
cmd/cagen/
├── main.go              # The brute-force certificate generator
└── result-*.json        # Generated certificates (embedded in binary)

cagenresults/
├── result.go            # Embeds results into the Anantha binary
└── result-Verisign.json # Pre-generated Verisign replacement certs

tls/ca/
├── key.pem              # CA1 private key (extracted from results)
├── cacert.pem           # CA1 certificate
├── key2.pem             # CA2 private key
├── cacert2.pem          # CA2 certificate
└── cross-cert1.pem      # CA1 signed by CA2

tls/server/
├── key.pem              # Server private key
├── cert.pem             # Server cert (signed by CA1)
└── cert-bundle.pem      # Full chain for TLS serving
```

## Usage

### Generating New Certificates

```bash
# Generate certificates for Verisign CA replacement (CPU-intensive!)
go run ./cmd/cagen -algo=Verisign

# Other supported algorithms
go run ./cmd/cagen -algo=CA1        # AmazonRootCA1
go run ./cmd/cagen -algo=Starfield  # Starfield CA
go run ./cmd/cagen -algo=256        # Amazon ECDSA P-256
go run ./cmd/cagen -algo=384        # Amazon ECDSA P-384
```

### Why Verisign?

At 1732 bytes, Verisign is the largest CA certificate in the firmware. Larger certificates
provide more room for whitespace stuffing, increasing the probability of finding valid
checksum-matching certificates quickly.

| Certificate | Size |
|-------------|------|
| amzn-cert-256 | 656 bytes |
| amzn-cert-384 | 737 bytes |
| AmazonRootCA1 | 1188 bytes |
| Starfield | 1468 bytes |
| **Verisign** | **1732 bytes** |

The tool runs on all CPU cores and saves progress to `result-{algo}.json`.
A built-in HTTP server on port 6060 shows real-time progress.

### Regenerating TLS Certificates

After generating new CA certificates:

```bash
./regen.sh
```

This extracts the two CA keys, creates cross-certificates, and signs the server cert.

### Patching Firmware

End users don't need to run cagen. The pre-generated certificates are embedded
in the Anantha binary:

```bash
anantha edit-firmware -in original.hex -out patched.hex
```

## Algorithm Details

### Main Loop (`certsetup`)

```
1. Parse the original CA certificate to copy validity dates and key usage
2. Generate a new ECDSA key pair with random serial number and UUID common name
3. Create and sign the new certificate
4. Calculate padding needed: toStuff = len(original) - len(new)
5. For each firstSliceLen (0-127):
   a. Create a copy of the original blob
   b. Place the new certificate at position [toStuff:]
   c. Try to stuff whitespace to match checksums (verifyFixedBlobs)
   d. Fix up the padding bytes (fixupStuffBytes)
   e. Verify all slices match (firstNonMatchingSlice)
   f. If successful, record this certificate for this firstSliceLen
6. Track the bitset of successful firstSliceLen values
7. Check if any pair of certificates covers all 128 values
```

### Checksum Matching (`verifyFixedBlobs`)

Works backwards through slices, attempting to insert `\r`/`\n` pairs to adjust
checksums. The mix of carriage returns and newlines provides flexibility since
they have different byte values (0x0D vs 0x0A).

### Padding Fixup (`fixupStuffBytes`)

Handles the slice that spans the padding/certificate boundary. Calculates the
exact byte values needed in the padding region to achieve the target checksum.

## Performance

- Runs on all CPU cores using `errgroup`
- GC tuned for throughput (`debug.SetGCPercent(1000)`)
- Progress saved incrementally to allow resuming
- Typically finds a complete solution in under 15 minutes on a 6-core/12-thread CPU (e.g., Ryzen 3600)
