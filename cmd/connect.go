/*
Copyright © 2025 Ben Szabo me@benszabo.co.uk
*/
package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"strings"

	"github.com/LottieHQ/bifrost/internal/config"
	"github.com/LottieHQ/bifrost/internal/discovery"
	"github.com/LottieHQ/bifrost/internal/portcache"
	"github.com/LottieHQ/bifrost/internal/sso"
	"github.com/LottieHQ/bifrost/internal/ui"
	"github.com/atotto/clipboard"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/feature/rds/auth"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/aws/aws-sdk-go-v2/service/elasticache"
	"github.com/aws/aws-sdk-go-v2/service/rds"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	"github.com/aws/aws-sdk-go-v2/service/ssm/types"
	ssosdk "github.com/aws/aws-sdk-go-v2/service/sso"
	"github.com/aws/aws-sdk-go-v2/service/ssooidc"
	"github.com/aws/aws-sdk-go-v2/service/sts"
	"github.com/spf13/cobra"
)

const defaultSSORegion = "us-east-1"
const defaultSSOStartURL = "https://d-906785ee68.awsapps.com/start"

// connectCmd represents the connect command
var connectCmd = &cobra.Command{
	Use:   "connect",
	Short: "Initiate a connection to an AWS RDS/Redis instance",
	Long: `Initiate a connection to an AWS RDS/Redis instance through a bastion host with AWS SSM Session Manager.

For example:
bifrost connect --service rds --port 3306 --bastion-instance-id i-1234567890abcdef0`,
	Run: func(cmd *cobra.Command, args []string) {
		prompt := ui.NewPrompt()
		cfgManager := config.NewManager()

		profileFlag, _ := cmd.Flags().GetString("profile")
		ssoProfileFlag, _ := cmd.Flags().GetString("sso-profile")
		accountIdFlag, _ := cmd.Flags().GetString("account-id")
		roleNameFlag, _ := cmd.Flags().GetString("role-name")
		regionFlag, _ := cmd.Flags().GetString("region")
		serviceTypeFlag, _ := cmd.Flags().GetString("service")
		portFlag, _ := cmd.Flags().GetString("port")
		bastionInstanceIDFlag, _ := cmd.Flags().GetString("bastion-instance-id")
		keepAliveFlag, _ := cmd.Flags().GetBool("keep-alive")
		keepAliveInterval, _ := cmd.Flags().GetDuration("keep-alive-interval")
		refreshFlag, _ := cmd.Flags().GetBool("refresh")

		// Default region to eu-west-2 if not provided
		if regionFlag == "" {
			regionFlag = "eu-west-2"
		}

		// --- Profile flag: use existing profile flow directly ---
		if profileFlag != "" {
			profile, err := cfgManager.GetConnectionProfile(profileFlag)
			if err != nil {
				fmt.Printf("Error loading connection profile '%s': %v\n", profileFlag, err)
				os.Exit(1)
			}
			fmt.Printf("🔗 Using connection profile: %s\n", profileFlag)
			runProfileConnect(profile, prompt, regionFlag, keepAliveFlag, keepAliveInterval)
			return
		}

		// --- SSO authenticate (for discovery + later use) ---
		var ssoRegion string
		var ssoClient *sso.Client
		if ssoProfileFlag != "" {
			// Explicit profile override
			profile, err := cfgManager.GetSSOProfile(ssoProfileFlag)
			if err != nil {
				fmt.Printf("Error: %v\n", err)
				os.Exit(1)
			}
			ssoRegion = profile.SSORegion
			ssoClient = sso.NewClient(profile.SSORegion, profile.StartURL)
		} else {
			// Default Lottie SSO endpoint
			ssoRegion = defaultSSORegion
			ssoClient = sso.NewClient(ssoRegion, defaultSSOStartURL)
		}
		ctx := context.Background()
		token, err := ssoClient.Authenticate(ctx)
		if err != nil {
			fmt.Printf("Authentication failed: %v\n", err)
			os.Exit(1)
		}

		manualMode := accountIdFlag != "" || serviceTypeFlag != ""
		if !manualMode {
			accounts, err := ssoClient.ListAccounts(ctx, token)
			if sso.IsUnauthorizedErr(err) {
				fmt.Println("⚠️ Cached SSO token was rejected, re-authenticating...")
				token, err = ssoClient.Reauthenticate(ctx)
				if err != nil {
					fmt.Printf("Authentication failed: %v\n", err)
					os.Exit(1)
				}
				accounts, err = ssoClient.ListAccounts(ctx, token)
			}
			if err != nil {
				fmt.Printf("Error listing accounts: %v\n", err)
				os.Exit(1)
			}

			// Load or refresh discovery cache
			var resources []discovery.Resource
			if !refreshFlag {
				cache, _ := discovery.LoadCache(ssoProfileFlag)
				if discovery.IsCacheValid(cache) {
					resources = cache.Resources
				}
			}
			if resources == nil {
				fmt.Println("🔍 Discovering resources...")
				resources, err = discovery.Discover(ctx, ssoRegion, token, accounts.AccountList, regionFlag)
				if err != nil {
					fmt.Printf("Warning: discovery failed: %v\n", err)
				}
				if len(resources) > 0 {
					_ = discovery.SaveCache(ssoProfileFlag, resources)
				}
			}

			if len(resources) > 0 {
				// Get saved connection profile names
				cfg, _ := cfgManager.Load()
				var profileNames []string
				if cfg != nil {
					for name := range cfg.ConnectionProfiles {
						profileNames = append(profileNames, name)
					}
				}

				selected, selectedProfileName, err := prompt.SelectResource(resources, profileNames)
				if err != nil {
					fmt.Printf("Error: %v\n", err)
					os.Exit(1)
				}

				// Handle saved profile selection
				if selectedProfileName != "" {
					profile, err := cfgManager.GetConnectionProfile(selectedProfileName)
					if err != nil {
						fmt.Printf("Error loading profile '%s': %v\n", selectedProfileName, err)
						os.Exit(1)
					}
					fmt.Printf("🔗 Using connection profile: %s\n", selectedProfileName)
					runProfileConnect(profile, prompt, regionFlag, keepAliveFlag, keepAliveInterval)
					return
				}

				// Handle discovered resource selection
				if selected != nil {
					runDiscoveredConnect(selected, ssoRegion, token, prompt, regionFlag, keepAliveFlag, keepAliveInterval)
					return
				}

				// Manual setup selected — fall through
			}
		}

		// --- Manual flow (existing behavior) ---
		session, err := getAWSConfigWithToken(ssoProfileFlag, ssoRegion, token, regionFlag, accountIdFlag, roleNameFlag, prompt)
		if err != nil {
			fmt.Printf("Error: %v\n", err)
			os.Exit(1)
		}
		awsCfg := session.Config

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
			defaultPort := "5432"
			if serviceTypeFlag == "redis" {
				defaultPort = "6379"
			}
			result, err := prompt.Input("Enter local port to use for forwarding", validatePort, defaultPort)
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

		// Resolve the target resource first so we know its VPC, then pick a bastion
		// that lives in that VPC.
		var endpoint string
		var port int32
		var resourceVPC string
		if serviceTypeFlag == "redis" {
			var clusterName string
			clusterName, err = promptForRedis(awsCfg, prompt)
			if err == nil {
				endpoint, port, resourceVPC, err = getRedisEndpoint(awsCfg, clusterName)
			}
		} else {
			var dbName string
			dbName, err = promptForRDS(awsCfg, prompt)
			if err == nil {
				endpoint, port, resourceVPC, err = getRDSEndpoint(awsCfg, dbName)
			}
		}
		if err != nil {
			fmt.Printf("Error: %v\n", err)
			os.Exit(1)
		}

		bastionInstanceIDFlag = resolveBastionID(awsCfg, bastionInstanceIDFlag, resourceVPC, prompt)

		iamEnabled := serviceTypeFlag == "rds" && session.isKubernetesAccount()
		runConnection(awsCfg, endpoint, port, portFlag, bastionInstanceIDFlag, regionFlag, iamEnabled, session.Username, session.AccountID, prompt, keepAliveFlag, keepAliveInterval)
	},
}

func init() {
	rootCmd.AddCommand(connectCmd)

	connectCmd.Flags().StringP("service", "s", "", "Service type (rds or redis)")
	connectCmd.Flags().StringP("port", "p", "", "Local port to use for forwarding")
	connectCmd.Flags().StringP("account-id", "a", "", "AWS account ID")
	connectCmd.Flags().StringP("role-name", "r", "", "AWS role name")
	connectCmd.Flags().String("sso-profile", "", "SSO profile to use for authentication")
	connectCmd.Flags().String("region", "", "AWS region where workloads are deployed")
	connectCmd.Flags().StringP("profile", "P", "", "Connection profile to use")
	connectCmd.Flags().String("bastion-instance-id", "", "EC2 instance ID of bastion host (required)")
	connectCmd.Flags().Bool("keep-alive", true, "Enable keep alive to maintain SSM connection")
	connectCmd.Flags().Duration("keep-alive-interval", 30*time.Second, "Interval between keep alive checks")
	connectCmd.Flags().Bool("refresh", false, "Force re-discovery of available resources (ignore cache)")
}

// runDiscoveredConnect handles connection to a discovered resource.
func runDiscoveredConnect(
	res *discovery.Resource,
	ssoRegion string,
	token *ssooidc.CreateTokenOutput,
	prompt *ui.Prompt,
	region string,
	keepAlive bool,
	keepAliveInterval time.Duration,
) {
	fmt.Printf("🪪 Account: %s\n", res.AccountName)
	fmt.Printf("🎯 %s: %s (%s)\n", res.ServiceType, res.Name, res.Engine)

	// Get credentials for the resource's account
	ctx := context.Background()
	awsCfg, err := discovery.BuildAWSConfig(ctx, ssoRegion, token, res.AccountID, res.RoleName, region)
	if err != nil {
		fmt.Printf("Error creating AWS config: %v\n", err)
		os.Exit(1)
	}

	// Default to the last port the user forwarded to for this resource, falling
	// back to the remote port if there's no cached choice.
	defaultPort := fmt.Sprintf("%d", res.Port)
	portLabel := "Local port"
	if cached, ok := portcache.Get(res.AccountID, res.Name); ok {
		defaultPort = cached
		portLabel = "Local port (last used)"
	}
	portFlag, err := prompt.Input(portLabel, validatePort, defaultPort)
	if err != nil {
		fmt.Printf("Error: %v\n", err)
		os.Exit(1)
	}
	if err := portcache.Set(res.AccountID, res.Name, portFlag); err != nil {
		fmt.Printf("⚠️  Could not persist port choice: %v\n", err)
	}

	// Validate bastion — must be in the resource's VPC.
	bastionID := resolveBastionID(awsCfg, res.BastionID, res.VpcID, prompt)

	// Resolve username for IAM auth
	username := getUsernameFromSTS(awsCfg)
	iamEnabled := res.IAMAuthEnabled && res.ServiceType == "rds"

	runConnection(awsCfg, res.Endpoint, res.Port, portFlag, bastionID, region, iamEnabled, username, res.AccountID, prompt, keepAlive, keepAliveInterval)
}

// runProfileConnect handles connection using a saved profile.
func runProfileConnect(
	profile *config.ConnectionProfile,
	prompt *ui.Prompt,
	regionFlag string,
	keepAlive bool,
	keepAliveInterval time.Duration,
) {
	ssoProfileFlag := profile.SSOProfile
	accountIdFlag := profile.AccountID
	roleNameFlag := profile.RoleName
	if profile.Region != "" {
		regionFlag = profile.Region
	}

	session, err := getAWSConfig(ssoProfileFlag, regionFlag, accountIdFlag, roleNameFlag)
	if err != nil {
		fmt.Printf("Error: %v\n", err)
		os.Exit(1)
	}
	awsCfg := session.Config

	serviceType := profile.ServiceType
	portFlag := profile.Port

	var endpoint string
	var port int32
	var resourceVPC string
	if serviceType == "redis" && profile.RedisClusterName != "" {
		fmt.Printf("🔗 Using Redis cluster: %s\n", profile.RedisClusterName)
		endpoint, port, resourceVPC, err = getRedisEndpoint(awsCfg, profile.RedisClusterName)
	} else if profile.RDSInstanceName != "" {
		fmt.Printf("🔗 Using RDS instance: %s\n", profile.RDSInstanceName)
		endpoint, port, resourceVPC, err = getRDSEndpoint(awsCfg, profile.RDSInstanceName)
	} else {
		fmt.Println("Error: profile is missing resource name")
		os.Exit(1)
	}
	if err != nil {
		fmt.Printf("Error: %v\n", err)
		os.Exit(1)
	}

	bastionID := resolveBastionID(awsCfg, profile.BastionInstanceID, resourceVPC, prompt)

	iamEnabled := serviceType == "rds" && session.isKubernetesAccount()
	runConnection(awsCfg, endpoint, port, portFlag, bastionID, regionFlag, iamEnabled, session.Username, session.AccountID, prompt, keepAlive, keepAliveInterval)
}

// runConnection starts the SSM tunnel, waits for it to be ready, then optionally
// prompts for IAM auth and generates a token. This order makes it clear to the
// user that the tunnel is open before asking about authentication.
func runConnection(awsCfg aws.Config, endpoint string, port int32, localPort string, bastionID string, region string, iamAuthEnabled bool, username string, accountID string, prompt *ui.Prompt, keepAlive bool, keepAliveInterval time.Duration) {
	// Start SSM tunnel in background
	tunnel := startSSMTunnel(awsCfg, bastionID, endpoint, port, localPort, region)
	defer tunnel.Cancel()
	defer tunnel.terminate(awsCfg)

	// Ctrl+C inside a prompt doesn't raise SIGINT (the terminal is in raw mode),
	// and the prompt exits the process, skipping the defers above. Tear the
	// tunnel down explicitly so the plugin and SSM session don't outlive us.
	prompt.OnAbort(func() {
		tunnel.stop()
		tunnel.terminate(awsCfg)
	})

	// Wait for tunnel to be ready
	fmt.Println("\n⏳ Waiting for tunnel...")
	ready, err := waitForTunnelReady(tunnel.Ctx, localPort, tunnel.ErrChan)
	if err != nil {
		fmt.Printf("❌ %v\n", err)
		os.Exit(1)
	}
	if !ready {
		fmt.Println("❌ Tunnel did not become ready within 30 seconds")
		os.Exit(1)
	}

	fmt.Printf("\n✅ Tunnel open on localhost:%s\n", localPort)
	fmt.Println()

	// Now that the tunnel is visibly open, offer IAM token generation
	if iamAuthEnabled && username != "" {
		fmt.Println("With this tunnel you can connect using any database credentials.")
		fmt.Println("This database supports IAM authentication — you can optionally")
		fmt.Println("generate a 15-minute short-lived token by selecting an option below.")
		fmt.Println()
		iamAuthUser := promptForAuthMethod(username, accountID, prompt)
		if iamAuthUser != "" {
			token, err := auth.BuildAuthToken(
				context.Background(),
				fmt.Sprintf("%s:%d", endpoint, port),
				region,
				iamAuthUser,
				awsCfg.Credentials,
			)
			if err != nil {
				fmt.Printf("❌ Failed to generate IAM auth token: %v\n", err)
			} else {
				fmt.Printf("\n🔑 IAM token for %s (expires in ~15 min):\n\n", iamAuthUser)
				safeUser := strings.ReplaceAll(iamAuthUser, "'", "'\\''")
				psqlCmd := fmt.Sprintf("PGPASSWORD='%s' psql -h localhost -p %s -U '%s' -d postgres", token, localPort, safeUser)
				fmt.Printf("  %s\n\n", psqlCmd)
				if err := clipboard.WriteAll(token); err == nil {
					fmt.Println("📋 Token copied to clipboard — use as the password in your database client")
				}
				fmt.Println()
			}
		}
	}

	fmt.Printf("📝 Press Ctrl+C to stop the connection\n\n")

	// Start keep alive
	if keepAlive {
		fmt.Printf("💓 Keep alive enabled (interval: %v)\n", keepAliveInterval)
		go startKeepAlive(tunnel.Ctx, localPort, keepAliveInterval)
	}

	// Wait for SSM to finish or signal
	select {
	case err := <-tunnel.ErrChan:
		if err != nil {
			fmt.Printf("Error: SSM session ended: %v\n", err)
			os.Exit(1)
		}
	case <-tunnel.SigChan:
		tunnel.stop()
	}
}

// ssmTunnel holds the state of an active SSM tunnel so it can be cleaned up.
type ssmTunnel struct {
	Cmd       *exec.Cmd
	SessionID string
	Region    string
	ErrChan   chan error
	Ctx       context.Context
	Cancel    context.CancelFunc
	SigChan   chan os.Signal
}

// stop shuts down the session-manager-plugin process, escalating to SIGKILL if
// it doesn't exit promptly after SIGTERM.
func (t *ssmTunnel) stop() {
	fmt.Println("\n🛑 Shutting down connection...")
	t.Cancel()
	if t.Cmd.Process != nil {
		_ = t.Cmd.Process.Signal(syscall.SIGTERM)
	}
	select {
	case err := <-t.ErrChan:
		_ = err // the tunnel is being torn down; its exit error is expected
	case <-time.After(300 * time.Millisecond):
		if t.Cmd.Process != nil {
			_ = t.Cmd.Process.Kill()
		}
	}
}

// terminate cleanly shuts down the SSM session via the API.
func (t *ssmTunnel) terminate(cfg aws.Config) {
	if t.SessionID == "" {
		return
	}
	ssmSvc := ssm.NewFromConfig(cfg, func(o *ssm.Options) { o.Region = t.Region })
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	_, _ = ssmSvc.TerminateSession(ctx, &ssm.TerminateSessionInput{
		SessionId: aws.String(t.SessionID),
	})
}

// startSSMTunnel starts an SSM port-forwarding session by calling the
// StartSession API directly and then invoking session-manager-plugin.
// This removes the runtime dependency on the aws CLI (and Python).
func startSSMTunnel(cfg aws.Config, instanceID, endpoint string, port int32, localPort string, region string) *ssmTunnel {
	if _, err := exec.LookPath("session-manager-plugin"); err != nil {
		fmt.Println("❌ session-manager-plugin is not installed.")
		fmt.Println("💡 Install it with: brew install --cask session-manager-plugin")
		os.Exit(1)
	}

	ssmSvc := ssm.NewFromConfig(cfg, func(o *ssm.Options) { o.Region = region })

	docName := "AWS-StartPortForwardingSessionToRemoteHost"
	input := &ssm.StartSessionInput{
		Target:       aws.String(instanceID),
		DocumentName: aws.String(docName),
		Parameters: map[string][]string{
			"host":            {endpoint},
			"portNumber":      {fmt.Sprintf("%d", port)},
			"localPortNumber": {localPort},
		},
	}

	sess, err := ssmSvc.StartSession(context.Background(), input)
	if err != nil {
		fmt.Printf("Failed to start SSM session: %v\n", err)
		os.Exit(1)
	}

	// Marshal only the fields the plugin expects — the SDK structs include
	// extra fields (ResultMetadata, Reason) that can confuse the plugin.
	sessJSON, _ := json.Marshal(map[string]string{
		"SessionId":  aws.ToString(sess.SessionId),
		"StreamUrl":  aws.ToString(sess.StreamUrl),
		"TokenValue": aws.ToString(sess.TokenValue),
	})
	inputJSON, _ := json.Marshal(map[string]any{
		"Target":       aws.ToString(input.Target),
		"DocumentName": aws.ToString(input.DocumentName),
		"Parameters":   input.Parameters,
	})

	ssmEndpoint := fmt.Sprintf("https://ssm.%s.amazonaws.com", region)

	// session-manager-plugin <session-json> <region> StartSession <profile> <input-json> <endpoint>
	cmd := exec.Command("session-manager-plugin",
		string(sessJSON),
		region,
		"StartSession",
		"",
		string(inputJSON),
		ssmEndpoint,
	)

	// The plugin needs AWS credentials to sign the WebSocket connection.
	creds, err := cfg.Credentials.Retrieve(context.Background())
	if err != nil {
		fmt.Printf("Failed to get credentials: %v\n", err)
		os.Exit(1)
	}
	cmd.Env = append(os.Environ(),
		"AWS_ACCESS_KEY_ID="+creds.AccessKeyID,
		"AWS_SECRET_ACCESS_KEY="+creds.SecretAccessKey,
		"AWS_SESSION_TOKEN="+creds.SessionToken,
		"AWS_REGION="+region,
	)

	cmd.Stdin = os.Stdin
	cmd.Stdout = nil // suppress SSM session output (Starting session, Waiting for connections, etc.)
	cmd.Stderr = os.Stderr

	ctx, cancel := context.WithCancel(context.Background())

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	errChan := make(chan error, 1)
	go func() {
		errChan <- cmd.Run()
	}()

	return &ssmTunnel{
		Cmd:       cmd,
		SessionID: aws.ToString(sess.SessionId),
		Region:    region,
		ErrChan:   errChan,
		Ctx:       ctx,
		Cancel:    cancel,
		SigChan:   sigChan,
	}
}

// waitForTunnelReady polls the local port until the SSM tunnel is accepting
// connections, or returns early if the plugin process exits.
func waitForTunnelReady(ctx context.Context, localPort string, errChan <-chan error) (bool, error) {
	for range 60 { // 30 seconds at 500ms intervals
		select {
		case <-ctx.Done():
			return false, nil
		case err := <-errChan:
			return false, fmt.Errorf("session-manager-plugin exited: %w", err)
		default:
		}
		if err := performKeepAlive(localPort); err == nil {
			return true, nil
		}
		select {
		case <-ctx.Done():
			return false, nil
		case err := <-errChan:
			return false, fmt.Errorf("session-manager-plugin exited: %w", err)
		case <-time.After(500 * time.Millisecond):
		}
	}
	return false, nil
}

var superuserGroups = map[string]string{
	"904233092296": "infra-aws-database-admins-stg",
	"733573666215": "infra-aws-database-admins-prd",
}

func superuserWarning(accountID string) string {
	group, ok := superuserGroups[accountID]
	if !ok {
		return "⚠️  No su- database users are provisioned on this account, only on kubernetes_non_prod " +
			"and kubernetes_prod. A token will still be generated but authentication will fail."
	}
	return fmt.Sprintf("⚠️  Superuser on this account requires the %s Google group "+
		"(Ravenna request \"Database superuser access\"). If you are not a member, a token will "+
		"still be generated but the su- database user won't exist and authentication will fail.\n\n"+
		"https://groups.google.com/a/lottie.org/g/%s/members", group, group)
}

// promptForAuthMethod asks the user to pick IAM / IAM Admin / Password.
func promptForAuthMethod(username, accountID string, prompt *ui.Prompt) string {
	authOptions := []string{
		fmt.Sprintf("🔑 IAM (%s)", username),
		fmt.Sprintf("🔑 IAM Superuser (su-%s)", username),
		"⏭️  Skip — I'll use my own credentials",
	}
	authMethod, err := prompt.Select("Generate a token", authOptions)
	if err != nil {
		fmt.Printf("Error selecting auth method: %v\n", err)
		os.Exit(1)
	}
	switch {
	case strings.HasPrefix(authMethod, "🔑 IAM Superuser"):
		fmt.Println(superuserWarning(accountID))
		fmt.Println()
		return "su-" + username
	case strings.HasPrefix(authMethod, "🔑 IAM ("):
		return username
	}
	return ""
}

// resolveBastionID picks a bastion host that can actually reach the target
// resource. targetVPC is the VPC the resource lives in (empty if unknown). When
// the VPC is known, only same-VPC bastions are considered: an account can host
// several VPCs (dev/staging/prod), and tunnelling through a bastion in another VPC
// silently connects the user to the wrong network. If the VPC is known but no
// online bastion exists in it, this aborts rather than risk a wrong connection.
func resolveBastionID(awsCfg aws.Config, bastionID string, targetVPC string, prompt *ui.Prompt) string {
	candidates, err := listSSMManagedInstances(awsCfg)
	if err != nil {
		fmt.Printf("Error listing SSM instances: %v\n", err)
		os.Exit(1)
	}

	if len(candidates) == 0 {
		fmt.Println("No SSM managed instances found.")
		os.Exit(1)
	}

	// vpcConfirmed is true only when we positively matched a bastion to the
	// resource's VPC. When it's false we can't guarantee a same-network host, so we
	// never auto-select silently — the user must consciously choose.
	vpcConfirmed := false
	if targetVPC != "" {
		var matched, unknown []bastionChoice
		for _, c := range candidates {
			switch c.VpcID {
			case targetVPC:
				matched = append(matched, c)
			case "":
				unknown = append(unknown, c)
			}
		}
		switch {
		case len(matched) > 0:
			candidates = preferTagged(matched)
			vpcConfirmed = true
		case len(unknown) > 0:
			// Couldn't confirm bastion VPCs (e.g. EC2 describe failed) — don't guess.
			fmt.Println("⚠️  Could not confirm bastion VPCs; pick the one matching your target environment.")
			candidates = unknown
		default:
			fmt.Printf("❌ No online bastion found in the resource's VPC (%s).\n", targetVPC)
			fmt.Println("   Aborting to avoid tunnelling through a bastion in the wrong network.")
			os.Exit(1)
		}
	} else {
		// Resource VPC unknown (e.g. missing describe permission). We can't filter,
		// so warn loudly and force a deliberate choice rather than risk the wrong VPC.
		fmt.Println("⚠️  Could not determine the resource's VPC; cannot guarantee a same-network bastion.")
		fmt.Println("   Pick the bastion matching your target environment.")
	}

	// Honour an explicitly-configured bastion only if it's a valid same-VPC candidate.
	if bastionID != "" {
		for _, c := range candidates {
			if c.InstanceID == bastionID {
				fmt.Printf("🏰 Using bastion: %s\n", bastionID)
				return bastionID
			}
		}
		if targetVPC != "" {
			fmt.Printf("⚠️  Bastion %s is not a bastion in the resource's VPC; selecting a matching one...\n", bastionID)
		} else {
			fmt.Printf("⚠️  Bastion %s is not available, discovering...\n", bastionID)
		}
	}

	// Only auto-select without prompting when the VPC was positively confirmed.
	if len(candidates) == 1 && vpcConfirmed {
		fmt.Printf("🏰 Auto-selected bastion: %s\n", candidates[0].DisplayName)
		return candidates[0].InstanceID
	}

	displayNames := make([]string, len(candidates))
	byDisplay := make(map[string]string, len(candidates))
	for i, c := range candidates {
		displayNames[i] = c.DisplayName
		byDisplay[c.DisplayName] = c.InstanceID
	}
	selected, err := prompt.Select("Select bastion instance", displayNames)
	if err != nil {
		fmt.Printf("Error: %v\n", err)
		os.Exit(1)
	}
	return byDisplay[selected]
}

// promptForRDS prompts for an RDS instance name or lists available ones.
func promptForRDS(awsCfg aws.Config, prompt *ui.Prompt) (string, error) {
	dbName, err := prompt.Input("Enter RDS DB instance name (or leave empty to browse)", nil)
	if err != nil {
		return "", err
	}
	if dbName != "" {
		return dbName, nil
	}
	instances, err := listRDSInstances(awsCfg)
	if err != nil {
		return "", err
	}
	if len(instances) == 0 {
		return "", fmt.Errorf("no RDS instances found")
	}
	return prompt.Select("Select RDS instance", instances)
}

// promptForRedis prompts for a Redis cluster name or lists available ones.
func promptForRedis(awsCfg aws.Config, prompt *ui.Prompt) (string, error) {
	clusterName, err := prompt.Input("Enter Redis cluster name (or leave empty to browse)", nil)
	if err != nil {
		return "", err
	}
	if clusterName != "" {
		return clusterName, nil
	}
	clusters, err := listRedisClusters(awsCfg)
	if err != nil {
		return "", err
	}
	if len(clusters) == 0 {
		return "", fmt.Errorf("no Redis clusters found")
	}
	return prompt.Select("Select Redis cluster", clusters)
}

// getAWSConfigWithToken uses an already-obtained SSO token to get AWS credentials.
func getAWSConfigWithToken(ssoProfileName, ssoRegion string, token *ssooidc.CreateTokenOutput, region, accountId, roleName string, prompt *ui.Prompt) (*awsSession, error) {
	ctx := context.Background()
	ssoSvc := ssosdk.NewFromConfig(aws.Config{Region: ssoRegion})

	accounts, err := ssoSvc.ListAccounts(ctx, &ssosdk.ListAccountsInput{
		AccessToken: token.AccessToken,
	})
	if sso.IsUnauthorizedErr(err) {
		fmt.Println("⚠️ Cached SSO token was rejected, re-authenticating...")
		token, err = reauthenticate(ctx, ssoProfileName)
		if err != nil {
			return nil, fmt.Errorf("re-authentication failed: %v", err)
		}
		accounts, err = ssoSvc.ListAccounts(ctx, &ssosdk.ListAccountsInput{
			AccessToken: token.AccessToken,
		})
	}
	if err != nil {
		return nil, fmt.Errorf("failed to list accounts: %v", err)
	}

	var accountName string
	if accountId == "" {
		_, accountId, err = prompt.SelectAccount(accounts)
		if err != nil {
			return nil, fmt.Errorf("failed to select account: %v", err)
		}
	}
	for _, acc := range accounts.AccountList {
		if *acc.AccountId == accountId {
			accountName = *acc.AccountName
			break
		}
	}
	fmt.Printf("🪪 Account: %s (%s)\n", accountName, accountId)

	if roleName == "" {
		roles, err := ssoSvc.ListAccountRoles(ctx, &ssosdk.ListAccountRolesInput{
			AccountId:   aws.String(accountId),
			AccessToken: token.AccessToken,
		})
		if err != nil {
			return nil, fmt.Errorf("failed to list roles: %v", err)
		}
		roleNames := make([]string, 0, len(roles.RoleList))
		for _, role := range roles.RoleList {
			roleNames = append(roleNames, *role.RoleName)
		}
		roleName, err = prompt.Select("Select a role", roleNames)
		if err != nil {
			return nil, fmt.Errorf("failed to select role: %v", err)
		}
	}
	fmt.Printf("👤 Role: %s\n", roleName)

	awsCfg, err := discovery.BuildAWSConfig(ctx, ssoRegion, token, accountId, roleName, region)
	if err != nil {
		return nil, fmt.Errorf("failed to create AWS config: %v", err)
	}

	return &awsSession{
		Config:      awsCfg,
		AccountID:   accountId,
		AccountName: accountName,
		RoleName:    roleName,
		Username:    getUsernameFromSTS(awsCfg),
	}, nil
}

// awsSession holds the result of SSO authentication
type awsSession struct {
	Config      aws.Config
	AccountID   string
	AccountName string
	RoleName    string
	Username    string // email from STS GetCallerIdentity (e.g. dan.williams@lottie.org)
}

// isKubernetesAccount returns true if the account name indicates a kubernetes account
func (s *awsSession) isKubernetesAccount() bool {
	return strings.Contains(strings.ToLower(s.AccountName), "kubernetes")
}

// getAWSConfig authenticates via SSO and returns an AWS session.
// It authenticates first, then delegates to getAWSConfigWithToken.
func getAWSConfig(ssoProfileName, region, accountId, roleName string) (*awsSession, error) {
	ctx := context.Background()
	prompt := ui.NewPrompt()

	ssoRegion, _, err := resolveSSOEndpoint(ssoProfileName)
	if err != nil {
		return nil, err
	}

	token, err := newSSOClient(ssoProfileName).Authenticate(ctx)
	if err != nil {
		return nil, fmt.Errorf("authentication failed: %v", err)
	}

	return getAWSConfigWithToken(ssoProfileName, ssoRegion, token, region, accountId, roleName, prompt)
}

func reauthenticate(ctx context.Context, ssoProfileName string) (*ssooidc.CreateTokenOutput, error) {
	return newSSOClient(ssoProfileName).Reauthenticate(ctx)
}

func newSSOClient(ssoProfileName string) *sso.Client {
	ssoRegion, startURL, err := resolveSSOEndpoint(ssoProfileName)
	if err != nil {
		ssoRegion, startURL = defaultSSORegion, defaultSSOStartURL
	}
	return sso.NewClient(ssoRegion, startURL)
}

func resolveSSOEndpoint(ssoProfileName string) (region, startURL string, err error) {
	if ssoProfileName == "" {
		return defaultSSORegion, defaultSSOStartURL, nil
	}
	ssoProfile, err := config.NewManager().GetSSOProfile(ssoProfileName)
	if err != nil {
		return "", "", fmt.Errorf("failed to get SSO profile '%s': %v", ssoProfileName, err)
	}
	return ssoProfile.SSORegion, ssoProfile.StartURL, nil
}

// getUsernameFromSTS extracts the username from the STS caller identity ARN.
// ARN format: arn:aws:sts::ACCOUNT:assumed-role/ROLE/username@domain.com
func getUsernameFromSTS(cfg aws.Config) string {
	stsSvc := sts.NewFromConfig(cfg)
	identity, err := stsSvc.GetCallerIdentity(context.Background(), &sts.GetCallerIdentityInput{})
	if err != nil || identity.Arn == nil {
		return ""
	}
	parts := strings.Split(*identity.Arn, "/")
	if len(parts) >= 3 {
		return parts[len(parts)-1]
	}
	return ""
}

// bastionChoice is a selectable bastion host annotated with its VPC.
type bastionChoice struct {
	DisplayName string // "Name (instance-id)" or just the instance id
	InstanceID  string
	VpcID       string // empty if EC2 details could not be fetched
	Tagged      bool   // carries bifrost:bastion=true
}

// preferTagged returns the bifrost:bastion-tagged choices, or all of them when
// none are tagged.
func preferTagged(choices []bastionChoice) []bastionChoice {
	var tagged []bastionChoice
	for _, c := range choices {
		if c.Tagged {
			tagged = append(tagged, c)
		}
	}
	if len(tagged) == 0 {
		return choices
	}
	return tagged
}

// listSSMManagedInstances returns SSM-managed instances usable as bastion hosts,
// each annotated with its VPC so callers can match a bastion to a target resource.
func listSSMManagedInstances(cfg aws.Config) ([]bastionChoice, error) {
	ssmSvc := ssm.NewFromConfig(cfg)
	ec2Svc := ec2.NewFromConfig(cfg)

	// Get all SSM managed instances
	ssmResult, err := ssmSvc.DescribeInstanceInformation(context.Background(), &ssm.DescribeInstanceInformationInput{})
	if err != nil {
		return nil, fmt.Errorf("failed to list SSM managed instances: %w", err)
	}

	// Get instance IDs that are online or connection lost (still manageable)
	var instanceIds []string
	for _, instance := range ssmResult.InstanceInformationList {
		if instance.InstanceId != nil &&
			(instance.PingStatus == types.PingStatusOnline || instance.PingStatus == types.PingStatusConnectionLost) {
			instanceIds = append(instanceIds, *instance.InstanceId)
		}
	}

	if len(instanceIds) == 0 {
		return []bastionChoice{}, nil
	}

	// Get EC2 instance details to fetch Name tags and VPCs.
	ec2Result, err := ec2Svc.DescribeInstances(context.Background(), &ec2.DescribeInstancesInput{
		InstanceIds: instanceIds,
	})
	if err != nil {
		// If EC2 call fails, return instance IDs without names or VPCs.
		choices := make([]bastionChoice, len(instanceIds))
		for i, id := range instanceIds {
			choices[i] = bastionChoice{DisplayName: id, InstanceID: id}
		}
		return choices, nil
	}

	var choices []bastionChoice
	for _, reservation := range ec2Result.Reservations {
		for _, instance := range reservation.Instances {
			if instance.InstanceId == nil {
				continue
			}

			instanceId := *instance.InstanceId

			// Find Name tag
			var name string
			for _, tag := range instance.Tags {
				if tag.Key != nil && *tag.Key == "Name" && tag.Value != nil {
					name = *tag.Value
					break
				}
			}

			displayName := instanceId
			if name != "" {
				displayName = fmt.Sprintf("%s (%s)", name, instanceId)
			}

			var vpcID string
			if instance.VpcId != nil {
				vpcID = *instance.VpcId
			}

			choices = append(choices, bastionChoice{
				DisplayName: displayName,
				InstanceID:  instanceId,
				VpcID:       vpcID,
				Tagged:      discovery.IsBastionTagged(instance.Tags),
			})
		}
	}

	return choices, nil
}

// List all RDS instances in the region
func listRDSInstances(cfg aws.Config) ([]string, error) {
	svc := rds.NewFromConfig(cfg)

	result, err := svc.DescribeDBInstances(context.Background(), &rds.DescribeDBInstancesInput{})
	if err != nil {
		return nil, fmt.Errorf("failed to list RDS instances: %w", err)
	}

	if len(result.DBInstances) == 0 {
		return []string{}, nil
	}

	instances := make([]string, 0, len(result.DBInstances))
	for _, db := range result.DBInstances {
		if db.DBInstanceIdentifier != nil {
			instances = append(instances, *db.DBInstanceIdentifier)
		}
	}

	return instances, nil
}

// Get the RDS database endpoint and VPC by DB instance name
func getRDSEndpoint(cfg aws.Config, dbInstanceName string) (endpoint string, port int32, vpcID string, err error) {
	if dbInstanceName == "" {
		return "", 0, "", fmt.Errorf("RDS instance name cannot be empty")
	}
	svc := rds.NewFromConfig(cfg)

	// Get specific DB instance by name
	result, err := svc.DescribeDBInstances(context.Background(), &rds.DescribeDBInstancesInput{
		DBInstanceIdentifier: &dbInstanceName,
	})
	if err != nil {
		return "", 0, "", fmt.Errorf("failed to describe DB instance '%s': %w", dbInstanceName, err)
	}

	if len(result.DBInstances) == 0 {
		return "", 0, "", fmt.Errorf("DB instance '%s' not found", dbInstanceName)
	}

	db := result.DBInstances[0]
	if db.Endpoint == nil {
		return "", 0, "", fmt.Errorf("DB instance '%s' does not have an endpoint (may not be available)", dbInstanceName)
	}

	if db.DBSubnetGroup != nil && db.DBSubnetGroup.VpcId != nil {
		vpcID = *db.DBSubnetGroup.VpcId
	}

	fmt.Printf("🎯 Connecting to RDS instance: %s\n", *db.DBInstanceIdentifier)
	return *db.Endpoint.Address, int32(*db.Endpoint.Port), vpcID, nil
}

// List all Redis clusters in the region
func listRedisClusters(cfg aws.Config) ([]string, error) {
	svc := elasticache.NewFromConfig(cfg)

	result, err := svc.DescribeReplicationGroups(context.Background(), &elasticache.DescribeReplicationGroupsInput{})
	if err != nil {
		return nil, fmt.Errorf("failed to list Redis clusters: %w", err)
	}

	if len(result.ReplicationGroups) == 0 {
		return []string{}, nil
	}

	clusters := make([]string, 0, len(result.ReplicationGroups))
	for _, cluster := range result.ReplicationGroups {
		if cluster.ReplicationGroupId != nil {
			clusters = append(clusters, *cluster.ReplicationGroupId)
		}
	}

	return clusters, nil
}

// Get the Redis cluster endpoint and VPC by replication group name
func getRedisEndpoint(cfg aws.Config, clusterName string) (endpoint string, port int32, vpcID string, err error) {
	if clusterName == "" {
		return "", 0, "", fmt.Errorf("redis cluster name cannot be empty")
	}
	svc := elasticache.NewFromConfig(cfg)

	ctx := context.Background()
	result, err := svc.DescribeReplicationGroups(ctx, &elasticache.DescribeReplicationGroupsInput{
		ReplicationGroupId: &clusterName,
	})
	if err != nil {
		return "", 0, "", fmt.Errorf("failed to describe Redis cluster '%s': %w", clusterName, err)
	}

	if len(result.ReplicationGroups) == 0 {
		return "", 0, "", fmt.Errorf("redis cluster '%s' not found", clusterName)
	}

	cluster := result.ReplicationGroups[0]

	// Ensure NodeGroups is non-empty and PrimaryEndpoint is not nil
	if len(cluster.NodeGroups) == 0 {
		return "", 0, "", fmt.Errorf("redis cluster '%s' has no node groups", clusterName)
	}

	if cluster.NodeGroups[0].PrimaryEndpoint == nil {
		return "", 0, "", fmt.Errorf("redis cluster '%s' does not have a primary endpoint (may not be available)", clusterName)
	}

	vpcID = getRedisVPC(cfg, svc, cluster.MemberClusters)

	fmt.Printf("🎯 Connecting to Redis cluster: %s\n", *cluster.ReplicationGroupId)
	return *cluster.NodeGroups[0].PrimaryEndpoint.Address, int32(*cluster.NodeGroups[0].PrimaryEndpoint.Port), vpcID, nil
}

// getRedisVPC resolves the VPC of a replication group via a member cache cluster's
// subnet group. Returns "" if it can't be determined (callers treat that as
// "unknown" and fall back to prompting rather than erroring).
func getRedisVPC(cfg aws.Config, svc *elasticache.Client, memberClusters []string) string {
	if len(memberClusters) == 0 {
		return ""
	}
	ctx := context.Background()
	cc, err := svc.DescribeCacheClusters(ctx, &elasticache.DescribeCacheClustersInput{
		CacheClusterId: &memberClusters[0],
	})
	if err != nil || len(cc.CacheClusters) == 0 || cc.CacheClusters[0].CacheSubnetGroupName == nil {
		return ""
	}
	name := *cc.CacheClusters[0].CacheSubnetGroupName
	sg, err := svc.DescribeCacheSubnetGroups(ctx, &elasticache.DescribeCacheSubnetGroupsInput{
		CacheSubnetGroupName: &name,
	})
	if err != nil || len(sg.CacheSubnetGroups) == 0 || sg.CacheSubnetGroups[0].VpcId == nil {
		return ""
	}
	return *sg.CacheSubnetGroups[0].VpcId
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
