package cmd

import (
	"bytes"
	"context"
	"encoding/xml"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/anupcshan/anantha/cagenresults"
	"github.com/anupcshan/anantha/intelhex"
	"github.com/anupcshan/anantha/membuf"
)

const upstreamManifestURL = "http://www.ota.ing.carrier.com/manifest"

// atomicWriteFile writes data to a file atomically using a temp file in the same directory.
func atomicWriteFile(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".tmp-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()

	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return err
	}
	if err := os.Chmod(tmpName, perm); err != nil {
		os.Remove(tmpName)
		return err
	}
	if err := os.Rename(tmpName, path); err != nil {
		os.Remove(tmpName)
		return err
	}
	return nil
}

// newUpstreamHTTPClient creates an HTTP client that bypasses local DNS hijacking.
func newUpstreamHTTPClient() *http.Client {
	dialer := &net.Dialer{
		Timeout:   30 * time.Second,
		KeepAlive: 30 * time.Second,
		Resolver: &net.Resolver{
			PreferGo: true,
			Dial: func(ctx context.Context, network, address string) (net.Conn, error) {
				d := net.Dialer{Timeout: 10 * time.Second}
				return d.DialContext(ctx, "udp", "1.1.1.1:53")
			},
		},
	}

	return &http.Client{
		Transport: &http.Transport{
			DialContext: dialer.DialContext,
		},
		Timeout: 5 * time.Minute,
	}
}

// FetchAndPrepareUpdate fetches the upstream manifest, finds the update for the
// given model, downloads and mutates the firmware, saving it to firmwareDir.
// The manifest is also saved to firmwareDir/manifest.xml.
func FetchAndPrepareUpdate(model, firmwareDir string) error {
	client := newUpstreamHTTPClient()

	// Fetch upstream manifest
	log.Printf("Fetching upstream manifest for model %s...", model)
	resp, err := client.Get(upstreamManifestURL)
	if err != nil {
		return fmt.Errorf("failed to fetch manifest: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("manifest fetch returned status %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("failed to read manifest: %w", err)
	}

	// Save manifest to file
	manifestPath := filepath.Join(firmwareDir, "manifest.xml")
	if err := atomicWriteFile(manifestPath, body, 0644); err != nil {
		return fmt.Errorf("failed to save manifest: %w", err)
	}

	var updates Updates
	if err := xml.Unmarshal(body, &updates); err != nil {
		return fmt.Errorf("failed to parse manifest: %w", err)
	}

	// Find update for model
	var update *Update
	for _, u := range updates.Updates {
		if u.Model == model {
			update = &u
			break
		}
	}
	if update == nil {
		return fmt.Errorf("no update found for model: %s", model)
	}

	// Extract filename from URL
	urlParts := strings.Split(update.URL, "/")
	filename := urlParts[len(urlParts)-1]
	destPath := filepath.Join(firmwareDir, filename)

	// Skip if already exists
	if _, err := os.Stat(destPath); err == nil {
		log.Printf("Firmware already prepared: %s", destPath)
		return nil
	}

	// Download firmware
	log.Printf("Downloading firmware from %s", update.URL)
	dlResp, err := client.Get(update.URL)
	if err != nil {
		return fmt.Errorf("failed to download firmware: %w", err)
	}
	defer dlResp.Body.Close()

	if dlResp.StatusCode != http.StatusOK {
		return fmt.Errorf("firmware download returned status %d", dlResp.StatusCode)
	}

	firmwareData, err := io.ReadAll(dlResp.Body)
	if err != nil {
		return fmt.Errorf("failed to read firmware: %w", err)
	}

	// Mutate and save firmware
	log.Printf("Mutating firmware...")
	mutatedData, err := mutateFirmware(firmwareData)
	if err != nil {
		return fmt.Errorf("failed to mutate firmware: %w", err)
	}

	if err := atomicWriteFile(destPath, mutatedData, 0644); err != nil {
		return fmt.Errorf("failed to save firmware: %w", err)
	}

	log.Printf("Firmware prepared: %s", destPath)
	return nil
}

func mutateFirmware(input []byte) ([]byte, error) {
	memBuf := membuf.NewMemBuffer()
	parser := intelhex.NewParser(bytes.NewReader(input), memBuf)
	for parser.HasNext() {
		if err := parser.ReadRecord(); err != nil {
			return nil, err
		}
	}

	updateCfg := cagenresults.Verisign
	memBuf.Update(&updateCfg, parser.Records)

	var outBuf bytes.Buffer
	encoder := intelhex.NewEncoder(memBuf, &outBuf, parser.Records)
	if err := encoder.EncodeRecords(); err != nil {
		return nil, err
	}

	return outBuf.Bytes(), nil
}
