/*
Copyright © 2025 Ben Szabo me@benszabo.co.uk
*/
package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
)

// Version information
var (
	Version   = "dev"
	BuildDate = "unknown"
	Commit    = "none"
)

// versionCmd represents the version command
var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Display the version information",
	Long:  `Display the version, build date, and commit hash of Bifrost.`,
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Printf("Bifrost version: %s\n", Version)
		fmt.Printf("Build date: %s\n", BuildDate)
		fmt.Printf("Commit: %s\n", Commit)
	},
}

func init() {
	rootCmd.AddCommand(versionCmd)
}
