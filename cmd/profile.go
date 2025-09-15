/*
Copyright © 2025 Ben Szabo me@benszabo.co.uk
*/
package cmd

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/b3nk3/bifrost/internal/config"
	"github.com/b3nk3/bifrost/internal/ui"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

// profileCmd represents the profile command
var profileCmd = &cobra.Command{
	Use:   "profile",
	Short: "Manage connection profiles",
	Long:  `Manage connection profiles that combine SSO authentication with connection settings.`,
}

var profileCreateCmd = &cobra.Command{
	Use:   "create",
	Short: "Create a new connection profile",
	Long: `Create a new connection profile that combines SSO authentication with connection settings.
Profiles are saved locally (.bifrost.config.yaml) by default, use --global for system-wide profiles.

Examples:
  bifrost profile create --name dev-rds --sso-profile work --env dev --service rds
  bifrost profile create --name prod-redis --global --sso-profile work --account-id 123456789 --role-name AdminRole`,
	Run: func(cmd *cobra.Command, args []string) {
		cfgManager := config.NewManager()
		prompt := ui.NewPrompt()

		profileName, _ := cmd.Flags().GetString("name")
		ssoProfile, _ := cmd.Flags().GetString("sso-profile")
		accountID, _ := cmd.Flags().GetString("account-id")
		roleName, _ := cmd.Flags().GetString("role-name")
		region, _ := cmd.Flags().GetString("region")
		environment, _ := cmd.Flags().GetString("env")
		serviceType, _ := cmd.Flags().GetString("service")
		port, _ := cmd.Flags().GetString("port")
		bastionInstanceID, _ := cmd.Flags().GetString("bastion-id")
		global, _ := cmd.Flags().GetBool("global")

		// Load config to check available SSO profiles
		cfg, err := cfgManager.Load()
		if err != nil {
			fmt.Printf("Error loading config: %v\n", err)
			os.Exit(1)
		}

		// Prompt for profile name if not provided
		if profileName == "" {
			result, err := prompt.Input("Connection profile name", nil)
			if err != nil {
				fmt.Printf("Error: %v\n", err)
				os.Exit(1)
			}
			profileName = result
		}

		// Prompt for SSO profile if not provided
		if ssoProfile == "" {
			if len(cfg.SSOProfiles) == 0 {
				fmt.Println("No SSO profiles found. Please create one with 'bifrost auth configure'")
				os.Exit(1)
			}

			// Try to get default SSO profile (if only one exists)
			if defaultProfile, err := cfgManager.GetDefaultSSOProfile(); err == nil && defaultProfile != "" {
				ssoProfile = defaultProfile
				fmt.Printf("🔐 Using SSO profile: %s\n", ssoProfile)
			} else {
				profileNames := make([]string, 0, len(cfg.SSOProfiles))
				for name := range cfg.SSOProfiles {
					profileNames = append(profileNames, name)
				}

				selected, err := prompt.Select("Select SSO profile", profileNames)
				if err != nil {
					fmt.Printf("Error selecting profile: %v\n", err)
					os.Exit(1)
				}
				ssoProfile = selected
			}
		}

		// Validate SSO profile exists
		if _, exists := cfg.SSOProfiles[ssoProfile]; !exists {
			fmt.Printf("SSO profile '%s' not found. Available profiles:\n", ssoProfile)
			for name := range cfg.SSOProfiles {
				fmt.Printf("  • %s\n", name)
			}
			os.Exit(1)
		}

		// Prompt for region if not provided
		if region == "" {
			result, err := prompt.Input("AWS region (where your RDS/Redis instances are)", nil)
			if err != nil {
				fmt.Printf("Error: %v\n", err)
				os.Exit(1)
			}
			region = result
		}

		// Prompt for environment if not provided
		if environment == "" {
			result, err := prompt.Select("Select environment", []string{"dev", "stg", "prd"})
			if err != nil {
				fmt.Printf("Error: %v\n", err)
				os.Exit(1)
			}
			environment = result
		}

		// Prompt for service type if not provided
		if serviceType == "" {
			result, err := prompt.Select("Select service type", []string{"rds", "redis"})
			if err != nil {
				fmt.Printf("Error: %v\n", err)
				os.Exit(1)
			}
			serviceType = result
		}

		// Prompt for account ID if not provided
		if accountID == "" {
			result, err := prompt.Input("AWS Account ID", nil)
			if err != nil {
				fmt.Printf("Error: %v\n", err)
				os.Exit(1)
			}
			accountID = result
		}

		// Prompt for role name if not provided
		if roleName == "" {
			result, err := prompt.Input("AWS Role Name (e.g., PowerUserAccess)", nil)
			if err != nil {
				fmt.Printf("Error: %v\n", err)
				os.Exit(1)
			}
			roleName = result
		}

		// Prompt for port if not provided
		if port == "" {
			defaultPort := "3306" // MySQL default
			if serviceType == "redis" {
				defaultPort = "6379"
			}
			result, err := prompt.Input(fmt.Sprintf("Local port (default: %s)", defaultPort), nil)
			if err != nil {
				fmt.Printf("Error: %v\n", err)
				os.Exit(1)
			}
			if result == "" {
				result = defaultPort
			}
			port = result
		}

		// Prompt for bastion instance ID if not provided
		if bastionInstanceID == "" {
			result, err := prompt.Input("Bastion Instance ID", nil)
			if err != nil {
				fmt.Printf("Error: %v\n", err)
				os.Exit(1)
			}
			bastionInstanceID = result
		}

		// Prompt for RDS/Redis resource names based on service type
		var rdsInstanceName, redisClusterName string
		switch serviceType {
		case "rds":
			result, err := prompt.Input("RDS DB Instance Name", nil)
			if err != nil {
				fmt.Printf("Error: %v\n", err)
				os.Exit(1)
			}
			rdsInstanceName = result
		case "redis":
			result, err := prompt.Input("Redis Cluster Name (replication group ID)", nil)
			if err != nil {
				fmt.Printf("Error: %v\n", err)
				os.Exit(1)
			}
			redisClusterName = result
		}

		// Create connection profile
		connectionProfile := config.ConnectionProfile{
			SSOProfile:        ssoProfile,
			AccountID:         accountID,
			RoleName:          roleName,
			Region:            region,
			Environment:       environment,
			ServiceType:       serviceType,
			Port:              port,
			BastionInstanceID: bastionInstanceID,
			RDSInstanceName:   rdsInstanceName,
			RedisClusterName:  redisClusterName,
		}

		// Save the profile (local by default, global if specified)
		var saveErr error
		if global {
			saveErr = cfgManager.AddConnectionProfile(profileName, connectionProfile)
			if saveErr == nil {
				fmt.Printf("✅ Connection profile '%s' saved to global config\n", profileName)
			}
		} else {
			saveErr = cfgManager.AddLocalConnectionProfile(profileName, connectionProfile)
			if saveErr == nil {
				fmt.Printf("✅ Connection profile '%s' saved to local config (.bifrost.config.yaml)\n", profileName)
			}
		}

		if saveErr != nil {
			fmt.Printf("Error saving connection profile: %v\n", saveErr)
			os.Exit(1)
		}

		fmt.Println("You can now use it with: bifrost connect --profile " + profileName)
	},
}

var profileListCmd = &cobra.Command{
	Use:   "list",
	Short: "List all connection profiles",
	Long:  `List all configured connection profiles (both global and local).`,
	Run: func(cmd *cobra.Command, args []string) {
		cfgManager := config.NewManager()
		cfg, err := cfgManager.Load()
		if err != nil {
			fmt.Printf("Error loading config: %v\n", err)
			os.Exit(1)
		}

		if len(cfg.ConnectionProfiles) == 0 {
			fmt.Println("No connection profiles configured. Use 'bifrost profile create' to create one.")
			return
		}

		fmt.Println("🔗 Connection Profiles:")
		for name, profile := range cfg.ConnectionProfiles {
			fmt.Printf("  • %s\n", name)
			fmt.Printf("    SSO Profile: %s\n", profile.SSOProfile)
			fmt.Printf("    Environment: %s\n", profile.Environment)
			fmt.Printf("    Service: %s\n", profile.ServiceType)
			fmt.Printf("    Region: %s\n", profile.Region)
			if profile.AccountID != "" {
				fmt.Printf("    Account ID: %s\n", profile.AccountID)
			}
			if profile.RoleName != "" {
				fmt.Printf("    Role: %s\n", profile.RoleName)
			}
			if profile.Port != "" {
				fmt.Printf("    Port: %s\n", profile.Port)
			}
			if profile.BastionInstanceID != "" {
				fmt.Printf("    Bastion: %s\n", profile.BastionInstanceID)
			}
			// Only show service-specific resource names
			if profile.ServiceType == "rds" && profile.RDSInstanceName != "" {
				fmt.Printf("    RDS Instance: %s\n", profile.RDSInstanceName)
			}
			if profile.ServiceType == "redis" && profile.RedisClusterName != "" {
				fmt.Printf("    Redis Cluster: %s\n", profile.RedisClusterName)
			}
			fmt.Println()
		}
	},
}

var profileDeleteCmd = &cobra.Command{
	Use:   "delete",
	Short: "Delete a connection profile",
	Long:  `Delete a connection profile by name.`,
	Run: func(cmd *cobra.Command, args []string) {
		cfgManager := config.NewManager()
		prompt := ui.NewPrompt()

		profileName, _ := cmd.Flags().GetString("name")

		// Load config
		cfg, err := cfgManager.Load()
		if err != nil {
			fmt.Printf("Error loading config: %v\n", err)
			os.Exit(1)
		}

		// Prompt for profile name if not provided
		if profileName == "" {
			if len(cfg.ConnectionProfiles) == 0 {
				fmt.Println("No connection profiles found.")
				return
			}

			profileNames := make([]string, 0, len(cfg.ConnectionProfiles))
			for name := range cfg.ConnectionProfiles {
				profileNames = append(profileNames, name)
			}

			selected, err := prompt.Select("Select profile to delete", profileNames)
			if err != nil {
				fmt.Printf("Error selecting profile: %v\n", err)
				os.Exit(1)
			}
			profileName = selected
		}

		// Check if profile exists
		if _, exists := cfg.ConnectionProfiles[profileName]; !exists {
			fmt.Printf("Connection profile '%s' not found\n", profileName)
			os.Exit(1)
		}

		// Confirm deletion
		confirmed, err := prompt.Confirm(fmt.Sprintf("Are you sure you want to delete profile '%s'?", profileName))
		if err != nil || !confirmed {
			fmt.Println("Deletion cancelled")
			return
		}

		// Check if profile exists in local config first
		localConfigFile := ".bifrost.config.yaml"
		if _, err := os.Stat(localConfigFile); err == nil {
			// Load local config to check if profile exists there
			localConfig := &config.LocalConfig{ConnectionProfiles: make(map[string]config.ConnectionProfile)}
			localViper := viper.New()
			localViper.SetConfigType("yaml")
			localViper.SetConfigFile(localConfigFile)

			if err := localViper.ReadInConfig(); err == nil {
				if err := localViper.Unmarshal(localConfig); err == nil {
					if _, existsLocally := localConfig.ConnectionProfiles[profileName]; existsLocally {
						// Delete from local config
						delete(localConfig.ConnectionProfiles, profileName)
						if err := cfgManager.SaveLocal(localConfig.ConnectionProfiles); err != nil {
							fmt.Printf("Error saving local config: %v\n", err)
							os.Exit(1)
						}
						fmt.Printf("✅ Connection profile '%s' deleted from local config (.bifrost.config.yaml)\n", profileName)
						return
					}
				}
			}
		}

		// If not found locally, delete from global config
		// Load fresh global config to avoid deleting local profiles
		globalConfig := &config.Config{
			SSOProfiles:        make(map[string]config.SSOProfile),
			ConnectionProfiles: make(map[string]config.ConnectionProfile),
		}

		// Load only global config
		homeDir, err := os.UserHomeDir()
		if err != nil {
			fmt.Printf("Error getting home directory: %v\n", err)
			os.Exit(1)
		}

		globalConfigFile := filepath.Join(homeDir, ".bifrost", "config.yaml")
		globalViper := viper.New()
		globalViper.SetConfigType("yaml")
		globalViper.SetConfigFile(globalConfigFile)

		if err := globalViper.ReadInConfig(); err != nil {
			fmt.Printf("Error reading global config: %v\n", err)
			os.Exit(1)
		}

		if err := globalViper.Unmarshal(globalConfig); err != nil {
			fmt.Printf("Error parsing global config: %v\n", err)
			os.Exit(1)
		}

		// Check if profile exists in global config
		if _, existsGlobally := globalConfig.ConnectionProfiles[profileName]; !existsGlobally {
			fmt.Printf("Connection profile '%s' not found in global config\n", profileName)
			os.Exit(1)
		}

		// Delete from global config
		delete(globalConfig.ConnectionProfiles, profileName)
		if err := cfgManager.Save(globalConfig); err != nil {
			fmt.Printf("Error saving global config: %v\n", err)
			os.Exit(1)
		}

		fmt.Printf("✅ Connection profile '%s' deleted from global config\n", profileName)
	},
}

func init() {
	rootCmd.AddCommand(profileCmd)
	profileCmd.AddCommand(profileCreateCmd)
	profileCmd.AddCommand(profileListCmd)
	profileCmd.AddCommand(profileDeleteCmd)

	// Create command flags
	profileCreateCmd.Flags().StringP("name", "n", "", "Connection profile name")
	profileCreateCmd.Flags().String("sso-profile", "", "SSO profile to use")
	profileCreateCmd.Flags().StringP("account-id", "a", "", "AWS account ID")
	profileCreateCmd.Flags().StringP("role-name", "r", "", "AWS role name")
	profileCreateCmd.Flags().String("region", "", "AWS region where workloads are deployed")
	profileCreateCmd.Flags().StringP("env", "e", "", "Environment (dev, stg, prd)")
	profileCreateCmd.Flags().StringP("service", "s", "", "Service type (rds, redis)")
	profileCreateCmd.Flags().StringP("port", "p", "", "Default local port")
	profileCreateCmd.Flags().String("bastion-id", "", "Bastion instance ID (optional)")
	profileCreateCmd.Flags().Bool("global", false, "Save to global config instead of local (.bifrost.config.yaml)")

	// Delete command flags
	profileDeleteCmd.Flags().StringP("name", "n", "", "Connection profile name to delete")
}
