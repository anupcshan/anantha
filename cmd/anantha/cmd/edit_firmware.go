package cmd

import (
	"log"
	"os"

	"github.com/anupcshan/anantha/cagenresults"
	"github.com/anupcshan/anantha/intelhex"
	"github.com/anupcshan/anantha/membuf"
	"github.com/spf13/cobra"
)

var editFirmwareCmd = &cobra.Command{
	Use:   "edit-firmware",
	Short: "Patch firmware hex files with CA certificates",
	Long: `Patch firmware hex files by replacing CA certificates with Anantha's CA certificate.

This tool modifies Intel HEX firmware files to replace existing trusted CA certificates
with Anantha's CA certificate, enabling MQTT traffic interception. The generated CA
certificate matches the checksum of the replaced certificate.

Example:
  anantha edit-firmware -in original/BINF0456.hex -out updated/BINF0456.hex`,
	RunE: runEditFirmware,
}

func init() {
	editFirmwareCmd.Flags().StringP("in", "i", "", "Input hex file (required)")
	editFirmwareCmd.Flags().StringP("out", "o", "", "Output hex file (required)")
	//nolint:errcheck
	editFirmwareCmd.MarkFlagRequired("in")
	//nolint:errcheck
	editFirmwareCmd.MarkFlagRequired("out")
}

func runEditFirmware(cmd *cobra.Command, args []string) error {
	inputFile, _ := cmd.Flags().GetString("in")
	outputFile, _ := cmd.Flags().GetString("out")

	log.SetFlags(log.Lmicroseconds | log.Lshortfile)

	f, err := os.Open(inputFile)
	if err != nil {
		return err
	}
	defer f.Close()

	var updateCfg = cagenresults.Verisign

	buf := membuf.NewMemBuffer()
	parser := intelhex.NewParser(f, buf)
	for parser.HasNext() {
		err := parser.ReadRecord()
		if err != nil {
			return err
		}
	}

	buf.Update(&updateCfg, parser.Records)

	outF, err := os.OpenFile(outputFile, os.O_CREATE|os.O_RDWR|os.O_TRUNC, 0644)
	if err != nil {
		return err
	}
	defer outF.Close()

	encoder := intelhex.NewEncoder(buf, outF, parser.Records)
	if err := encoder.EncodeRecords(); err != nil {
		return err
	}

	log.Printf("Successfully patched firmware: %s -> %s", inputFile, outputFile)
	return nil
}
