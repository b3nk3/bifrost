/*
Copyright © 2025 Ben Szabo me@benszabo.co.uk
*/
package cmd

import (
	"context"
	"fmt"
	"os"

	"github.com/b3nk3/bifrost/internal/config"
	"github.com/b3nk3/bifrost/internal/sso"
	"github.com/b3nk3/bifrost/internal/ui"
	"github.com/spf13/cobra"
)

// authCmd represents the auth command
var authCmd = &cobra.Command{
	Use:   "auth",
	Short: "Manage SSO authentication profiles",
	Long:  `Manage SSO authentication profiles for different AWS environments.`,
}

var authLoginCmd = &cobra.Command{
	Use:   "login",
	Short: "Login to AWS SSO using an existing profile",
	Long: `Login to AWS SSO using an existing profile. If no profile is specified, you'll be prompted to select one.

Examples:
  bifrost auth login --profile work
  bifrost auth login`,
	Run: func(cmd *cobra.Command, args []string) {
		cfgManager := config.NewManager()
		prompt := ui.NewPrompt()

		profileName, _ := cmd.Flags().GetString("profile")

		// Load existing profiles
		cfg, err := cfgManager.Load()
		if err != nil {
			fmt.Printf("Error loading config: %v\n", err)
			os.Exit(1)
		}

		// Check if there are any profiles
		if len(cfg.SSOProfiles) == 0 {
			fmt.Println("No SSO profiles found. Use 'bifrost auth configure' to create one.")
			os.Exit(1)
		}

		// If no profile specified, let user select
		if profileName == "" {
			profileNames := make([]string, 0, len(cfg.SSOProfiles))
			for name := range cfg.SSOProfiles {
				profileNames = append(profileNames, name)
			}

			selected, err := prompt.Select("Select SSO profile to login with", profileNames)
			if err != nil {
				fmt.Printf("Error selecting profile: %v\n", err)
				os.Exit(1)
			}
			profileName = selected
		}

		// Get the selected profile
		ssoProfile, err := cfgManager.GetSSOProfile(profileName)
		if err != nil {
			fmt.Printf("Error: %v\n", err)
			os.Exit(1)
		}

		// Perform authentication
		fmt.Printf("🔐 Authenticating with profile '%s'...\n", profileName)

		ctx := context.Background()
		ssoClient := sso.NewClient(ssoProfile.SSORegion, ssoProfile.StartURL)

		// Authenticate and get token
		_, err = ssoClient.Authenticate(ctx)
		if err != nil {
			fmt.Printf("Authentication failed: %v\n", err)
			os.Exit(1)
		}

		fmt.Printf("✅ Successfully authenticated with profile '%s'\n", profileName)
	},
}

var authConfigureCmd = &cobra.Command{
	Use:   "configure",
	Short: "Create or update SSO profile configuration",
	Long: `Create or update SSO profile configuration (SSO URL and region).

Examples:
  bifrost auth configure --profile work --sso-url https://company.awsapps.com/start --sso-region us-east-1
  bifrost auth configure --profile work`,
	Run: func(cmd *cobra.Command, args []string) {
		cfgManager := config.NewManager()
		prompt := ui.NewPrompt()

		profileName, _ := cmd.Flags().GetString("profile")
		ssoURL, _ := cmd.Flags().GetString("sso-url")
		ssoRegion, _ := cmd.Flags().GetString("sso-region")
		noAutoDetect, _ := cmd.Flags().GetBool("no-auto-detect")

		// Prompt for profile name if not provided
		if profileName == "" {
			result, err := prompt.Input("Profile name", nil)
			if err != nil {
				fmt.Printf("Error: %v\n", err)
				os.Exit(1)
			}
			profileName = result
		}

		// Check if profile exists and get current values
		existingProfile, _ := cfgManager.GetSSOProfile(profileName)

		// Prompt for SSO URL if not provided
		if ssoURL == "" {
			defaultValue := ""
			if existingProfile != nil {
				defaultValue = existingProfile.StartURL
			}
			result, err := prompt.Input("SSO Start URL (e.g. https://a-123456789.awsapps.com/start)", nil, defaultValue)
			if err != nil {
				fmt.Printf("Error: %v\n", err)
				os.Exit(1)
			}
			ssoURL = result
		}

		// Prompt for SSO region if not provided
		if ssoRegion == "" {
			defaultValue := ""
			if existingProfile != nil {
				defaultValue = existingProfile.SSORegion
			} else if ssoURL != "" && !noAutoDetect {
				// Try to auto-detect region from SSO URL
				fmt.Printf("🔍 Auto-detecting SSO region from URL...\n")
				if detectedRegion, err := sso.ExtractRegionFromSSO(ssoURL); err == nil {
					defaultValue = detectedRegion
					fmt.Printf("✅ Detected SSO region: %s\n", detectedRegion)
				} else {
					fmt.Printf("⚠️ Could not auto-detect region: %v\n", err)
				}
			}

			result, err := prompt.Input("SSO region (e.g. us-east-1)", nil, defaultValue)
			if err != nil {
				fmt.Printf("Error: %v\n", err)
				os.Exit(1)
			}
			ssoRegion = result
		}

		// Create SSO profile
		ssoProfile := config.SSOProfile{
			StartURL:  ssoURL,
			SSORegion: ssoRegion,
		}

		// Save the profile
		if err := cfgManager.AddSSOProfile(profileName, ssoProfile); err != nil {
			fmt.Printf("Error saving profile: %v\n", err)
			os.Exit(1)
		}

		fmt.Printf("✅ SSO profile '%s' configured\n", profileName)
		fmt.Println("Use 'bifrost auth login' to authenticate with this profile.")
	},
}

var authListCmd = &cobra.Command{
	Use:   "list",
	Short: "List all SSO profiles",
	Long:  `List all configured SSO authentication profiles.`,
	Run: func(cmd *cobra.Command, args []string) {
		cfgManager := config.NewManager()
		cfg, err := cfgManager.Load()
		if err != nil {
			fmt.Printf("Error loading config: %v\n", err)
			os.Exit(1)
		}

		if len(cfg.SSOProfiles) == 0 {
			fmt.Println("No SSO profiles configured. Use 'bifrost auth configure' to create one.")
			return
		}

		fmt.Println("📋 SSO Profiles:")
		for name, profile := range cfg.SSOProfiles {
			fmt.Printf("  • %s\n", name)
			fmt.Printf("    SSO URL: %s\n", profile.StartURL)
			fmt.Printf("    Region: %s\n", profile.SSORegion)
			fmt.Println()
		}
	},
}

var authLogoutCmd = &cobra.Command{
	Use:   "logout",
	Short: "Clear cached SSO tokens",
	Long:  `Clear cached SSO tokens for all profiles.`,
	Run: func(cmd *cobra.Command, args []string) {
		// Clear token cache
		if err := sso.ClearTokenCache(); err != nil {
			fmt.Printf("Error clearing token cache: %v\n", err)
			os.Exit(1)
		}
		fmt.Println("✅ Token cache cleared")
	},
}

func init() {
	rootCmd.AddCommand(authCmd)
	authCmd.AddCommand(authLoginCmd)
	authCmd.AddCommand(authConfigureCmd)
	authCmd.AddCommand(authListCmd)
	authCmd.AddCommand(authLogoutCmd)

	// Login command flags
	authLoginCmd.Flags().StringP("profile", "p", "", "Profile name")

	// Configure command flags
	authConfigureCmd.Flags().StringP("profile", "p", "", "Profile name")
	authConfigureCmd.Flags().String("sso-url", "", "SSO Start URL")
	authConfigureCmd.Flags().String("sso-region", "", "SSO region")
	authConfigureCmd.Flags().Bool("no-auto-detect", false, "Disable automatic region detection from SSO URL")
}
