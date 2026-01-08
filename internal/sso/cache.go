package sso

import (
	"crypto/sha1"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// TokenCache is botocore-compatible format used by aws-sso-util and assume/granted
type TokenCache struct {
	StartUrl    string    `json:"startUrl"`
	Region      string    `json:"region"`
	AccessToken string    `json:"accessToken"`
	ExpiresAt   time.Time `json:"expiresAt"`
	ReceivedAt  time.Time `json:"receivedAt"`
}

// ClientRegistrationCache stores client registration data in botocore-compatible format
type ClientRegistrationCache struct {
	ClientId     string    `json:"clientId"`
	ClientSecret string    `json:"clientSecret"`
	ExpiresAt    time.Time `json:"expiresAt"`
	ReceivedAt   time.Time `json:"receivedAt"`
}

// normalizeStartURL removes trailing /# or / to match aws-sso-util format
func normalizeStartURL(startURL string) string {
	startURL = strings.TrimSuffix(startURL, "#")
	startURL = strings.TrimSuffix(startURL, "/")
	return startURL
}

func getTokenCachePath(startURL string) (string, error) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}

	// Use the same cache directory as AWS CLI
	cacheDir := filepath.Join(homeDir, ".aws", "sso", "cache")
	if err := os.MkdirAll(cacheDir, 0700); err != nil {
		return "", err
	}

	// Normalize URL to match aws-sso-util format
	normalizedURL := normalizeStartURL(startURL)
	hash := fmt.Sprintf("%x", sha1.Sum([]byte(normalizedURL)))
	return filepath.Join(cacheDir, hash+".json"), nil
}

func LoadTokenCache(startURL string) (*TokenCache, error) {
	// First try direct path lookup with normalized URL
	path, err := getTokenCachePath(startURL)
	if err != nil {
		return nil, err
	}

	data, err := os.ReadFile(path)
	if err == nil {
		var token TokenCache
		if err := json.Unmarshal(data, &token); err == nil {
			return &token, nil
		}
	}

	// If direct lookup fails, scan cache files for matching startUrl
	// This handles tokens created by other tools with different URL formats
	return findTokenByStartURL(startURL)
}

// findTokenByStartURL scans all cache files to find a token matching the startURL
func findTokenByStartURL(startURL string) (*TokenCache, error) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}

	cacheDir := filepath.Join(homeDir, ".aws", "sso", "cache")
	entries, err := os.ReadDir(cacheDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	normalizedTarget := normalizeStartURL(startURL)

	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		// Skip botocore client registration files
		if strings.HasPrefix(entry.Name(), "botocore-client-id-") {
			continue
		}

		filePath := filepath.Join(cacheDir, entry.Name())
		data, err := os.ReadFile(filePath)
		if err != nil {
			continue
		}

		var token TokenCache
		if err := json.Unmarshal(data, &token); err != nil {
			continue
		}

		// Check if this token's startUrl matches (after normalization)
		if token.StartUrl != "" && normalizeStartURL(token.StartUrl) == normalizedTarget {
			return &token, nil
		}
	}

	return nil, nil
}

func SaveTokenCache(accessToken, startUrl, region string, expiresAt time.Time) error {
	path, err := getTokenCachePath(startUrl)
	if err != nil {
		return err
	}

	cache := TokenCache{
		StartUrl:    normalizeStartURL(startUrl),
		Region:      region,
		AccessToken: accessToken,
		ExpiresAt:   expiresAt,
		ReceivedAt:  time.Now(),
	}

	data, err := json.Marshal(cache)
	if err != nil {
		return err
	}

	return os.WriteFile(path, data, 0600)
}

func ClearTokenCache() error {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return err
	}

	cacheDir := filepath.Join(homeDir, ".aws", "sso", "cache")
	
	// Check if cache directory exists
	if _, err := os.Stat(cacheDir); os.IsNotExist(err) {
		return nil // Nothing to clear
	}

	// Remove all cache files
	entries, err := os.ReadDir(cacheDir)
	if err != nil {
		return err
	}

	for _, entry := range entries {
		if !entry.IsDir() && filepath.Ext(entry.Name()) == ".json" {
			cachePath := filepath.Join(cacheDir, entry.Name())
			if err := os.Remove(cachePath); err != nil {
				return err
			}
		}
	}

	return nil
}

func getClientRegistrationCachePath(region string) (string, error) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}

	cacheDir := filepath.Join(homeDir, ".aws", "sso", "cache")
	if err := os.MkdirAll(cacheDir, 0700); err != nil {
		return "", err
	}

	// Use botocore-compatible filename
	filename := fmt.Sprintf("botocore-client-id-%s.json", region)
	return filepath.Join(cacheDir, filename), nil
}

func LoadClientRegistration(region string) (*ClientRegistrationCache, error) {
	path, err := getClientRegistrationCachePath(region)
	if err != nil {
		return nil, err
	}

	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	var cache ClientRegistrationCache
	if err := json.Unmarshal(data, &cache); err != nil {
		return nil, err
	}

	return &cache, nil
}

func SaveClientRegistration(region, clientId, clientSecret string, expiresAt time.Time) error {
	path, err := getClientRegistrationCachePath(region)
	if err != nil {
		return err
	}

	cache := ClientRegistrationCache{
		ClientId:     clientId,
		ClientSecret: clientSecret,
		ExpiresAt:    expiresAt,
		ReceivedAt:   time.Now(),
	}

	data, err := json.Marshal(cache)
	if err != nil {
		return err
	}

	return os.WriteFile(path, data, 0600)
}
