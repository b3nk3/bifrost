package sso

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/sso"
	"github.com/aws/aws-sdk-go-v2/service/ssooidc"
	"github.com/pkg/browser"
)

// Client represents an SSO client that handles authentication and token management
type Client struct {
	region   string
	startURL string
}

// NewClient creates a new SSO client
func NewClient(region, startURL string) *Client {
	return &Client{
		region:   region,
		startURL: startURL,
	}
}

// Authenticate handles the SSO authentication flow
func (c *Client) Authenticate(ctx context.Context) (*ssooidc.CreateTokenOutput, error) {
	// Check for cached token
	cachedToken, err := LoadTokenCache(c.startURL)
	if err != nil {
		log.Printf("⚠️ Warning: Failed to load cached token: %v", err)
	}

	if cachedToken != nil && time.Now().Before(cachedToken.ExpiresAt) {
		fmt.Println("🔄 Using cached SSO token...")
		return &ssooidc.CreateTokenOutput{
			AccessToken: aws.String(cachedToken.AccessToken),
		}, nil
	}

	// Step 1: Begin device authorization
	ssoOidc := ssooidc.NewFromConfig(aws.Config{Region: c.region})

	register, err := ssoOidc.RegisterClient(ctx, &ssooidc.RegisterClientInput{
		ClientName: aws.String("bifrost"),
		ClientType: aws.String("public"),
	})
	if err != nil {
		return nil, fmt.Errorf("RegisterClient: %w", err)
	}

	deviceAuth, err := ssoOidc.StartDeviceAuthorization(ctx, &ssooidc.StartDeviceAuthorizationInput{
		ClientId:     register.ClientId,
		ClientSecret: register.ClientSecret,
		StartUrl:     aws.String(c.startURL),
	})
	if err != nil {
		return nil, fmt.Errorf("StartDeviceAuthorization: %w", err)
	}

	verificationURL := *deviceAuth.VerificationUriComplete

	// Open the URL in the default browser
	if err := browser.OpenURL(verificationURL); err != nil {
		fmt.Println("❌ Error opening browser:", err)
	}

	fmt.Println("\n🔐 Please complete the AWS SSO login in your browser")
	fmt.Printf("🔑 Code: %s\n\n", *deviceAuth.UserCode)

	// Step 2: Poll for token
	var token *ssooidc.CreateTokenOutput
	maxRetries := 30 // Maximum number of retries (5 minutes with default 10-second interval)
	retryCount := 0

	for {
		// Check for context cancellation
		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("context cancelled while waiting for token: %w", ctx.Err())
		default:
			// Continue with polling
		}

		// Check if we've exceeded the maximum retry count
		if retryCount >= maxRetries {
			return nil, fmt.Errorf("maximum retry count exceeded while waiting for token")
		}

		time.Sleep(time.Duration(deviceAuth.Interval) * time.Second)
		token, err = ssoOidc.CreateToken(ctx, &ssooidc.CreateTokenInput{
			ClientId:     register.ClientId,
			ClientSecret: register.ClientSecret,
			DeviceCode:   deviceAuth.DeviceCode,
			GrantType:    aws.String("urn:ietf:params:oauth:grant-type:device_code"),
		})
		if err == nil {
			break
		}

		retryCount++
	}

	// Cache the new token
	cacheToken := &TokenCache{
		AccessToken:  *token.AccessToken,
		ExpiresAt:    time.Now().Add(8 * time.Hour), // SSO tokens typically expire in 8 hours
		ClientId:     *register.ClientId,
		ClientSecret: *register.ClientSecret,
		StartUrl:     c.startURL,
		Region:       c.region,
	}
	if err := SaveTokenCache(cacheToken); err != nil {
		log.Printf("⚠️ Warning: Failed to cache token: %v", err)
	}

	return token, nil
}

// ListAccounts returns a list of available AWS accounts
func (c *Client) ListAccounts(ctx context.Context, token *ssooidc.CreateTokenOutput) (*sso.ListAccountsOutput, error) {
	ssoClient := sso.NewFromConfig(aws.Config{Region: c.region})
	return ssoClient.ListAccounts(ctx, &sso.ListAccountsInput{
		AccessToken: token.AccessToken,
	})
}

// ListAccountRoles returns a list of available roles for an account
func (c *Client) ListAccountRoles(ctx context.Context, token *ssooidc.CreateTokenOutput, accountId string) (*sso.ListAccountRolesOutput, error) {
	ssoClient := sso.NewFromConfig(aws.Config{Region: c.region})
	return ssoClient.ListAccountRoles(ctx, &sso.ListAccountRolesInput{
		AccountId:   aws.String(accountId),
		AccessToken: token.AccessToken,
	})
}

// GetRoleCredentials returns credentials for a specific role
func (c *Client) GetRoleCredentials(ctx context.Context, token *ssooidc.CreateTokenOutput, accountId, roleName string) (*sso.GetRoleCredentialsOutput, error) {
	ssoClient := sso.NewFromConfig(aws.Config{Region: c.region})
	return ssoClient.GetRoleCredentials(ctx, &sso.GetRoleCredentialsInput{
		AccessToken: token.AccessToken,
		AccountId:   aws.String(accountId),
		RoleName:    aws.String(roleName),
	})
}
