/*
Copyright © 2025 Ben Szabo me@benszabo.co.uk
*/
package cmd

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/b3nk3/bifrost/internal/config"
	"github.com/b3nk3/bifrost/internal/sso"
	"github.com/b3nk3/bifrost/internal/ui"
	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/aws/aws-sdk-go-v2/service/elasticache"
	"github.com/aws/aws-sdk-go-v2/service/rds"
	"github.com/spf13/cobra"
)

// connectCmd represents the connect command
var connectCmd = &cobra.Command{
	Use:   "connect",
	Short: "Initiate a connection to an AWS RDS/Redis instance",
	Long: `Initiate a connection to an AWS RDS/Redis instance through a bastion host with AWS SSM Session Manager.
	
For example:
bifrost connect --env dev --service rds --port 3306`,
	Run: func(cmd *cobra.Command, args []string) {
		prompt := ui.NewPrompt()
		cfgManager := config.NewManager()

		profileFlag, _ := cmd.Flags().GetString("profile")
		ssoProfileFlag, _ := cmd.Flags().GetString("sso-profile")
		accountIdFlag, _ := cmd.Flags().GetString("account-id")
		roleNameFlag, _ := cmd.Flags().GetString("role-name")
		regionFlag, _ := cmd.Flags().GetString("region")
		environmentFlag, _ := cmd.Flags().GetString("env")
		serviceTypeFlag, _ := cmd.Flags().GetString("service")
		portFlag, _ := cmd.Flags().GetString("port")
		bastionInstanceIDFlag, _ := cmd.Flags().GetString("bastion-instance-id")
		keepAliveFlag, _ := cmd.Flags().GetBool("keep-alive")
		keepAliveInterval, _ := cmd.Flags().GetDuration("keep-alive-interval")

		// Check if using connection profile (from flag or selection)
		var selectedProfile *config.ConnectionProfile
		if profileFlag != "" {
			// Load specific connection profile
			profile, err := cfgManager.GetConnectionProfile(profileFlag)
			if err != nil {
				fmt.Printf("Error loading connection profile '%s': %v\n", profileFlag, err)
				os.Exit(1)
			}
			selectedProfile = profile
			fmt.Printf("🔗 Using connection profile: %s\n", profileFlag)
		} else {
			// Check for available connection profiles and offer selection
			cfg, err := cfgManager.Load()
			if err != nil {
				fmt.Printf("Error loading config: %v\n", err)
				os.Exit(1)
			}

			if len(cfg.ConnectionProfiles) > 0 {
				// Add manual setup option with clear distinction
				profileNames := make([]string, 0, len(cfg.ConnectionProfiles)+1)
				profileNames = append(profileNames, "⚙️ Manual setup")
				for name := range cfg.ConnectionProfiles {
					profileNames = append(profileNames, "🔗 "+name)
				}

				selected, err := prompt.Select("Select connection profile or manual setup", profileNames)
				if err != nil {
					fmt.Printf("Error selecting profile: %v\n", err)
					os.Exit(1)
				}

				if selected != "⚙️ Manual setup" {
					// Remove the emoji prefix to get actual profile name
					profileName := selected[5:] // Remove "🔗 " prefix
					profile, err := cfgManager.GetConnectionProfile(profileName)
					if err != nil {
						fmt.Printf("Error loading connection profile '%s': %v\n", profileName, err)
						os.Exit(1)
					}
					selectedProfile = profile
					fmt.Printf("🔗 Using connection profile: %s\n", profileName)
				}
			}
		}

		// Use connection profile values as defaults (if available)
		if selectedProfile != nil {
			if ssoProfileFlag == "" && selectedProfile.SSOProfile != "" {
				ssoProfileFlag = selectedProfile.SSOProfile
			}
			if accountIdFlag == "" && selectedProfile.AccountID != "" {
				accountIdFlag = selectedProfile.AccountID
			}
			if roleNameFlag == "" && selectedProfile.RoleName != "" {
				roleNameFlag = selectedProfile.RoleName
			}
			if regionFlag == "" && selectedProfile.Region != "" {
				regionFlag = selectedProfile.Region
			}
			if environmentFlag == "" && selectedProfile.Environment != "" {
				environmentFlag = selectedProfile.Environment
			}
			if serviceTypeFlag == "" && selectedProfile.ServiceType != "" {
				serviceTypeFlag = selectedProfile.ServiceType
			}
			if portFlag == "" && selectedProfile.Port != "" {
				portFlag = selectedProfile.Port
			}
			if bastionInstanceIDFlag == "" && selectedProfile.BastionInstanceID != "" {
				bastionInstanceIDFlag = selectedProfile.BastionInstanceID
			}
		}

		// Prompt for SSO profile if not provided
		if ssoProfileFlag == "" {
			// Try to get default SSO profile (if only one exists)
			defaultProfile, err := cfgManager.GetDefaultSSOProfile()
			if err != nil {
				fmt.Printf("Error loading config: %v\n", err)
				os.Exit(1)
			}

			if defaultProfile != "" {
				ssoProfileFlag = defaultProfile
				fmt.Printf("🔐 Using SSO profile: %s\n", ssoProfileFlag)
			} else {
				// Load config to show available profiles
				cfg, err := cfgManager.Load()
				if err != nil {
					fmt.Printf("Error loading config: %v\n", err)
					os.Exit(1)
				}

				if len(cfg.SSOProfiles) == 0 {
					fmt.Println("No SSO profiles found. Please create one with 'bifrost auth configure'")
					os.Exit(1)
				}

				profileNames := make([]string, 0, len(cfg.SSOProfiles))
				for name := range cfg.SSOProfiles {
					profileNames = append(profileNames, name)
				}

				selected, err := prompt.Select("Select SSO profile", profileNames)
				if err != nil {
					fmt.Printf("Error selecting profile: %v\n", err)
					os.Exit(1)
				}
				ssoProfileFlag = selected
			}
		}

		// Prompt for region if not provided
		if regionFlag == "" {
			result, err := prompt.Input("AWS region (where your RDS/Redis instances are)", nil)
			if err != nil {
				fmt.Printf("Error: %v\n", err)
				os.Exit(1)
			}
			regionFlag = result
		}

		// 1. Check AWS credentials
		awsCfg, accountIdFlag, roleNameFlag, err := getAWSConfig(ssoProfileFlag, regionFlag, accountIdFlag, roleNameFlag)
		if err != nil {
			fmt.Printf("Error: %v\n", err)
			os.Exit(1)
		}

		// 0. Check for environment and service type arguments

		if environmentFlag == "" {
			result, err := prompt.Select("Select environment", []string{"dev", "stg", "prd"})
			if err != nil {
				fmt.Printf("Prompt failed %v\n", err)
				return
			}
			environmentFlag = result
		} else if environmentFlag != "dev" && environmentFlag != "stg" && environmentFlag != "prd" {
			fmt.Println("Invalid environment. Please choose either 'dev', 'stg', or 'prd'.")
			return
		}
		fmt.Printf("🌍 Environment: %s\n", environmentFlag)

		if serviceTypeFlag == "" {
			result, err := prompt.Select("Select service type", []string{"rds", "redis"})
			if err != nil {
				fmt.Printf("Prompt failed %v\n", err)
				return
			}
			serviceTypeFlag = result
		} else if serviceTypeFlag != "rds" && serviceTypeFlag != "redis" {
			fmt.Println("Invalid service type. Please choose either 'rds' or 'redis'.")
			return
		}
		fmt.Printf("🛠️ Service type: %s\n", serviceTypeFlag)

		if portFlag == "" {
			result, err := prompt.Input("Enter local port to use for forwarding", validatePort)
			if err != nil {
				fmt.Printf("Prompt failed %v\n", err)
				return
			}
			portFlag = result
		} else if err := validatePort(portFlag); err != nil {
			fmt.Println(err)
			return
		}
		fmt.Printf("🌐 Port: %s\n", portFlag)

		// 2. Retrieve bastion instance ID
		var bastionID string
		if bastionInstanceIDFlag != "" {
			bastionID = bastionInstanceIDFlag
			fmt.Printf("🏰 Using configured bastion instance: %s\n", bastionID)
		} else {
			var err error
			bastionID, err = getBastionInstanceID(awsCfg, environmentFlag)
			if err != nil {
				fmt.Printf("Error retrieving bastion instance ID: %v\n", err)
				os.Exit(1)
			}
		}

		// Get endpoint based on service type
		var endpoint string
		var port int32
		if serviceTypeFlag == "redis" {
			endpoint, port, err = getRedisEndpoint(awsCfg, environmentFlag)
		} else {
			endpoint, port, err = getRDSEndpoint(awsCfg, environmentFlag)
		}
		if err != nil {
			fmt.Printf("Error retrieving endpoint: %v\n", err)
			os.Exit(1)
		}

		// 4. Offer to save as profile if manual setup was used (before starting SSM session)
		if selectedProfile == nil { // Only for manual setup
			offerToSaveProfile(cfgManager, prompt, ssoProfileFlag, accountIdFlag, roleNameFlag, regionFlag, environmentFlag, serviceTypeFlag, portFlag, bastionInstanceIDFlag)
		}

		fmt.Printf("🔌 Forwarding `%s` to 127.0.0.1:%s (use this as host in your app or client)\n", serviceTypeFlag, portFlag)
		fmt.Printf("📝 Press Ctrl+C to stop the connection\n\n")

		// 5. Set up port forwarding using SSM with keep alive
		if keepAliveFlag {
			fmt.Printf("💓 Keep alive enabled (interval: %v)\n", keepAliveInterval)
		}
		err = startSSMPortForwardingWithKeepAlive(awsCfg, bastionID, endpoint, port, portFlag, regionFlag, serviceTypeFlag, keepAliveFlag, keepAliveInterval)
		if err != nil {
			fmt.Printf("Error starting SSM session: %v\n", err)
			os.Exit(1)
		}

	},
}

func init() {
	rootCmd.AddCommand(connectCmd)

	connectCmd.Flags().StringP("env", "e", "", "AWS environment")
	connectCmd.Flags().StringP("service", "s", "", "Service type (rds or redis)")
	connectCmd.Flags().StringP("port", "p", "", "Local port to use for forwarding")
	connectCmd.Flags().StringP("account-id", "a", "", "AWS account ID")
	connectCmd.Flags().StringP("role-name", "r", "", "AWS role name")
	connectCmd.Flags().String("sso-profile", "", "SSO profile to use for authentication")
	connectCmd.Flags().String("region", "", "AWS region where workloads are deployed")
	connectCmd.Flags().StringP("profile", "P", "", "Connection profile to use")
	connectCmd.Flags().String("bastion-instance-id", "", "EC2 instance ID of bastion host (skips discovery)")
	connectCmd.Flags().Bool("keep-alive", true, "Enable keep alive to maintain SSM connection")
	connectCmd.Flags().Duration("keep-alive-interval", 30*time.Second, "Interval between keep alive checks")
}

// Check and load AWS credentials using SSO profile
func getAWSConfig(ssoProfileName, region, accountId, roleName string) (aws.Config, string, string, error) {
	ctx := context.Background()
	cfgManager := config.NewManager()
	prompt := ui.NewPrompt()

	// Get SSO profile
	ssoProfile, err := cfgManager.GetSSOProfile(ssoProfileName)
	if err != nil {
		return aws.Config{}, "", "", fmt.Errorf("failed to get SSO profile '%s': %v", ssoProfileName, err)
	}

	// Initialize SSO client
	ssoClient := sso.NewClient(ssoProfile.SSORegion, ssoProfile.StartURL)

	// Authenticate and get token
	token, err := ssoClient.Authenticate(ctx)
	if err != nil {
		return aws.Config{}, "", "", fmt.Errorf("authentication failed: %v", err)
	}

	// List accounts if account ID not provided
	if accountId == "" {
		accounts, err := ssoClient.ListAccounts(ctx, token)
		if err != nil {
			return aws.Config{}, "", "", fmt.Errorf("failed to list accounts: %v", err)
		}

		// Select account
		_, accountId, err = prompt.SelectAccount(accounts)
		if err != nil {
			return aws.Config{}, "", "", fmt.Errorf("failed to select account: %v", err)
		}
	}
	fmt.Printf("🪪 Account ID: %s\n", accountId)

	// List roles if role name not provided
	if roleName == "" {
		roles, err := ssoClient.ListAccountRoles(ctx, token, accountId)
		if err != nil {
			return aws.Config{}, "", "", fmt.Errorf("failed to list roles: %v", err)
		}

		// Select role
		roleName, err = prompt.SelectRole(roles)
		if err != nil {
			return aws.Config{}, "", "", fmt.Errorf("failed to select role: %v", err)
		}
	}
	fmt.Printf("👤 Role: %s\n", roleName)

	// Get role credentials
	roleCreds, err := ssoClient.GetRoleCredentials(ctx, token, accountId, roleName)
	if err != nil {
		return aws.Config{}, "", "", fmt.Errorf("failed to get role credentials: %v", err)
	}

	// Create AWS config with the role credentials and region
	awsCfg, err := awsconfig.LoadDefaultConfig(ctx,
		awsconfig.WithRegion(region),
		awsconfig.WithCredentialsProvider(credentials.NewStaticCredentialsProvider(
			*roleCreds.RoleCredentials.AccessKeyId,
			*roleCreds.RoleCredentials.SecretAccessKey,
			*roleCreds.RoleCredentials.SessionToken,
		)),
	)
	if err != nil {
		return aws.Config{}, "", "", fmt.Errorf("failed to create AWS config: %v", err)
	}

	return awsCfg, accountId, roleName, nil
}

// Find the bastion instance ID
func getBastionInstanceID(cfg aws.Config, environment string) (string, error) {
	svc := ec2.NewFromConfig(cfg)

	// Filter for instances with a tag "Name" containing "bastion"
	input := &ec2.DescribeInstancesInput{
		Filters: []types.Filter{
			{
				Name: aws.String("tag:Name"),
				Values: []string{
					"*bastion*",
				},
			},
			{
				Name: aws.String("tag:env"),
				Values: []string{
					"*" + environment + "*",
				},
			},
			{
				Name: aws.String("instance-state-name"),
				Values: []string{
					"running",
				},
			},
		},
	}

	result, err := svc.DescribeInstances(context.Background(), input)
	if err != nil {
		return "", err
	}

	// Find the first running bastion instance
	for _, reservation := range result.Reservations {
		for _, instance := range reservation.Instances {
			return *instance.InstanceId, nil
		}
	}

	return "", fmt.Errorf("no running bastion instance found")
}

type database struct {
	Name     string
	Endpoint string
	Port     int32
}

// Get the RDS database endpoint
func getRDSEndpoint(cfg aws.Config, environment string) (string, int32, error) {
	svc := rds.NewFromConfig(cfg)
	prompt := ui.NewPrompt()

	// Get all DB instances
	result, err := svc.DescribeDBInstances(context.Background(), &rds.DescribeDBInstancesInput{})
	if err != nil {
		return "", 0, err
	}

	// filter based on `env` tag
	databases := []database{}
	for _, db := range result.DBInstances {
		if db.TagList != nil {
			for _, tag := range db.TagList {
				if tag.Key != nil && *tag.Key == "env" && tag.Value != nil && strings.Contains(*tag.Value, environment) {
					databases = append(databases, database{
						Name:     *db.DBInstanceIdentifier,
						Endpoint: *db.Endpoint.Address,
						Port:     *db.Endpoint.Port,
					})
				}
			}
		}
	}

	if len(databases) == 1 {
		fmt.Printf("🎯 Connecting to RDS instance: %s\n", databases[0].Name)
		return databases[0].Endpoint, int32(databases[0].Port), nil
	} else if len(databases) > 1 {
		dbNames := make([]string, len(databases))
		for i, db := range databases {
			dbNames[i] = db.Name
		}

		selected, err := prompt.Select("Select RDS instance", dbNames)
		if err != nil {
			return "", 0, fmt.Errorf("prompt failed %v", err)
		}

		// Find the selected database
		for _, db := range databases {
			if db.Name == selected {
				return db.Endpoint, int32(db.Port), nil
			}
		}
	}

	return "", 0, fmt.Errorf("no RDS instances found for environment %s", environment)
}

// Get the Redis cluster endpoint
func getRedisEndpoint(cfg aws.Config, environment string) (string, int32, error) {
	svc := elasticache.NewFromConfig(cfg)
	prompt := ui.NewPrompt()

	ctx := context.Background()
	result, err := svc.DescribeReplicationGroups(ctx, &elasticache.DescribeReplicationGroupsInput{})
	if err != nil {
		return "", 0, err
	}

	type redisCluster struct {
		Name     string
		Endpoint string
		Port     int32
	}

	clusters := []redisCluster{}

	for _, cluster := range result.ReplicationGroups {
		if cluster.ARN == nil {
			continue
		}
		// Fetch tags for this replication group
		tagsOut, err := svc.ListTagsForResource(ctx, &elasticache.ListTagsForResourceInput{
			ResourceName: cluster.ARN,
		})
		if err != nil {
			continue // skip clusters we can't tag-inspect
		}
		for _, tag := range tagsOut.TagList {
			if tag.Key != nil && *tag.Key == "env" && tag.Value != nil && strings.Contains(*tag.Value, environment) {
				// Ensure NodeGroups is non-empty and PrimaryEndpoint is not nil
				if len(cluster.NodeGroups) == 0 || cluster.NodeGroups[0].PrimaryEndpoint == nil {
					continue
				}
				clusters = append(clusters, redisCluster{
					Name:     *cluster.ReplicationGroupId,
					Endpoint: *cluster.NodeGroups[0].PrimaryEndpoint.Address,
					Port:     int32(*cluster.NodeGroups[0].PrimaryEndpoint.Port),
				})
				break // found env tag, no need to check more tags
			}
		}
	}

	if len(clusters) == 1 {
		fmt.Printf("🎯 Connecting to Redis cluster: %s\n", clusters[0].Name)
		return clusters[0].Endpoint, clusters[0].Port, nil
	} else if len(clusters) > 1 {
		clusterNames := make([]string, len(clusters))
		for i, c := range clusters {
			clusterNames[i] = c.Name
		}
		selected, err := prompt.Select("Select Redis cluster", clusterNames)
		if err != nil {
			return "", 0, fmt.Errorf("prompt failed %v", err)
		}
		for _, c := range clusters {
			if c.Name == selected {
				return c.Endpoint, c.Port, nil
			}
		}
	}

	return "", 0, fmt.Errorf("no Redis clusters found for environment %s", environment)
}

// Start SSM port forwarding session with keep alive functionality
func startSSMPortForwardingWithKeepAlive(cfg aws.Config, instanceID, endpoint string, port int32, localPort string, workloadRegion string, serviceType string, keepAlive bool, keepAliveInterval time.Duration) error {
	// Construct the SSM command
	ssmArgs := []string{
		"ssm", "start-session",
		"--target", instanceID,
		"--region", workloadRegion,
		"--document-name", "AWS-StartPortForwardingSessionToRemoteHost",
		"--parameters", fmt.Sprintf("host=%s,portNumber=%d,localPortNumber=%s", endpoint, port, localPort),
	}

	// Create command
	cmd := exec.Command("aws", ssmArgs...)

	// Get AWS credentials from the config
	creds, err := cfg.Credentials.Retrieve(context.Background())
	if err != nil {
		return fmt.Errorf("failed to get credentials from config: %w", err)
	}

	// Set AWS credentials from the config
	cmd.Env = append(os.Environ(),
		"AWS_ACCESS_KEY_ID="+creds.AccessKeyID,
		"AWS_SECRET_ACCESS_KEY="+creds.SecretAccessKey,
		"AWS_SESSION_TOKEN="+creds.SessionToken,
		"AWS_REGION="+workloadRegion,
	)

	// Connect stdin/stdout/stderr
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	// Set up signal handling for graceful shutdown
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Handle interrupt signals
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	// Start the SSM session in a goroutine
	errChan := make(chan error, 1)
	go func() {
		errChan <- cmd.Run()
	}()

	// Start keep alive functionality if enabled (wait for SSM tunnel to be ready)
	if keepAlive {
		go startKeepAliveWhenReady(ctx, localPort, keepAliveInterval)
	}

	// Wait for either the command to finish, an error, or a signal
	select {
	case err := <-errChan:
		return err
	case <-sigChan:
		fmt.Println("\n🛑 Shutting down connection...")
		cancel()

		// Terminate the SSM process
		if cmd.Process != nil {
			if err := cmd.Process.Signal(syscall.SIGTERM); err != nil {
				fmt.Printf("Warning: failed to send termination signal: %v\n", err)
			}
		}

		// Wait a bit for graceful shutdown
		time.Sleep(1 * time.Second)
		return nil
	}
}

// Start keep alive when SSM tunnel becomes ready (no arbitrary delay)
func startKeepAliveWhenReady(ctx context.Context, localPort string, interval time.Duration) {
	// Poll until the SSM tunnel is ready (check every 500ms for up to 30 seconds)
	maxAttempts := 60 // 30 seconds with 500ms intervals
	for i := 0; i < maxAttempts; i++ {
		select {
		case <-ctx.Done():
			return
		default:
		}

		if err := performKeepAlive(localPort); err == nil {
			// Connection successful, start regular keep alive
			startKeepAlive(ctx, localPort, interval)
			return
		}

		// Wait 500ms before retrying
		select {
		case <-ctx.Done():
			return
		case <-time.After(500 * time.Millisecond):
		}
	}

	// If we get here, the tunnel never became ready
	fmt.Printf("⚠️ Keep alive disabled - SSM tunnel did not become ready within 30 seconds\n")
}

// Keep alive functionality
func startKeepAlive(ctx context.Context, localPort string, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := performKeepAlive(localPort); err != nil {
				// Log error but continue - keep alive failures shouldn't stop the connection
				fmt.Printf("⚠️ Keep alive check failed: %v\n", err)
			}
		}
	}
}

// Perform a keep alive check by attempting a TCP connection to the local port
func performKeepAlive(localPort string) error {
	// Simple TCP connection test to keep the SSM tunnel alive
	conn, err := net.DialTimeout("tcp", fmt.Sprintf("localhost:%s", localPort), 5*time.Second)
	if err != nil {
		return fmt.Errorf("failed to connect to local port %s: %w", localPort, err)
	}
	defer func() {
		_ = conn.Close() // Ignore error - this is cleanup
	}()

	// Connection successful - SSM tunnel is alive
	return nil
}


func validatePort(input string) error {
	inputPort, err := strconv.Atoi(input)
	if err != nil {
		return fmt.Errorf("invalid port number: %s", input)
	}
	if inputPort < 1 || inputPort > 65535 {
		return fmt.Errorf("port number must be between 1 and 65535")
	}
	// Check if the port is already in use
	if isPortInUse(inputPort) {
		return fmt.Errorf("port %d is already in use", inputPort)
	}
	return nil
}

func isPortInUse(port int) bool {
	conn, err := net.Listen("tcp", fmt.Sprintf(":%d", port))
	if err != nil {
		return true
	}
	if err := conn.Close(); err != nil {
		// Log the error but don't affect the port check result
		fmt.Fprintf(os.Stderr, "Warning: failed to close connection: %v\n", err)
	}
	return false
}

// offerToSaveProfile prompts the user to save the manual connection configuration as a profile
func offerToSaveProfile(cfgManager *config.Manager, prompt *ui.Prompt, ssoProfile, accountID, roleName, region, environment, serviceType, port, bastionInstanceID string) {
	fmt.Println() // Add some spacing

	// Ask if they want to save the configuration
	confirmed, err := prompt.Confirm("Would you like to save this configuration as a connection profile for future use?")
	if err != nil || !confirmed {
		return
	}

	// Prompt for profile name
	defaultName := fmt.Sprintf("%s-%s", environment, serviceType)
	profileName, err := prompt.Input("Profile name", nil, defaultName)
	if err != nil {
		fmt.Printf("Error getting profile name: %v\n", err)
		return
	}

	// Ask where to save (local vs global)
	saveLocation, err := prompt.Select("Where would you like to save this profile?", []string{"📁 Local (.bifrost.config.yaml)", "🌍 Global (~/.bifrost/config.yaml)"})
	if err != nil {
		fmt.Printf("Error selecting save location: %v\n", err)
		return
	}

	// Create connection profile
	connectionProfile := config.ConnectionProfile{
		SSOProfile:       ssoProfile,
		AccountID:        accountID,
		RoleName:         roleName,
		Region:           region,
		Environment:      environment,
		ServiceType:      serviceType,
		Port:             port,
		BastionInstanceID: bastionInstanceID,
	}

	// Save the profile
	var saveErr error
	if saveLocation == "🌍 Global (~/.bifrost/config.yaml)" {
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
		fmt.Printf("❌ Error saving profile: %v\n", saveErr)
		return
	}

	fmt.Printf("💡 You can now use this profile with: bifrost connect --profile %s\n", profileName)
}
