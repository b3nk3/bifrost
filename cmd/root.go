/*
Copyright © 2025 Ben Szabo me@benszabo.co.uk
*/
package cmd

import (
	"os"

	"github.com/spf13/cobra"
)

// rootCmd represents the base command when called without any subcommands
var rootCmd = &cobra.Command{
	Use:   "bifrost",
	Short: "A connector for AWS RDS/Redis instances through bastion hosts",
	Long: `Bifrost is a command-line tool that allows you to connect to AWS RDS and Redis instances utilising AWS SSM Session Manager.
It simplifies the process of establishing a secure connection to your database instances through a bastion host,
making it easier to manage and access your resources in the cloud.`,
	// Uncomment the following line if your bare application
	// has an action associated with it:
	// Run: func(cmd *cobra.Command, args []string) { },
}

// Execute adds all child commands to the root command and sets flags appropriately.
// This is called by main.main(). It only needs to happen once to the rootCmd.
func Execute() {
	err := rootCmd.Execute()
	if err != nil {
		os.Exit(1)
	}
}

func init() {}
