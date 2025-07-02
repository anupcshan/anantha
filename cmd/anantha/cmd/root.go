package cmd

import (
	"os"

	"github.com/spf13/cobra"
)

var rootCmd = &cobra.Command{
	Use:   "anantha",
	Short: "Local Control Toolkit for Carrier Thermostats",
	Long: `Anantha enables local control of Carrier Infinity thermostats by intercepting
DNS requests and redirecting HTTP/MQTT connections. It provides a web dashboard
and Home Assistant integration for thermostat control.

Anantha (അനന്ത) is a Malayalam word meaning "infinite".`,
}

// Execute adds all child commands to the root command and sets flags appropriately.
func Execute() {
	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}

func init() {
	rootCmd.AddCommand(serveCmd)
	rootCmd.AddCommand(editFirmwareCmd)
}
