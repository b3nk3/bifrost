package sso

import (
	"crypto/sha1"
	"encoding/json"
	"fmt"
	"log"
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

// normalizeStartURL removes trailing "#" and "/" characters from startURL to normalize AWS SSO start URLs.
// Handles edge cases like "/#", "/#/", etc.
func normalizeStartURL(startURL string) string {
	return strings.TrimRight(startURL, "#/")
}

// getTokenCachePath returns the filesystem path for the SSO token cache file corresponding to startURL.
// It ensures the ~/.aws/sso/cache directory exists, normalizes startURL (removes trailing "#" and "/"),
// and uses the SHA-1 hex of the normalized URL with a ".json" extension as the filename.
// An error is returned if the user's home directory cannot be determined or the cache directory cannot be created.
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

// LoadTokenCache loads the SSO token cache for the given start URL.
// It first attempts a direct lookup using the normalized start URL hash, and if that
// fails it scans the SSO cache directory for a token whose stored StartUrl matches
// the provided start URL (after normalization).
//
// The startURL parameter is the SSO start URL used to locate the cached token.
// It returns a pointer to the TokenCache when a matching cache file is found,
// (nil, nil) if no matching token exists, or (nil, error) if an I/O or parsing
// error occurs.
func LoadTokenCache(startURL string) (*TokenCache, error) {
	// First try direct path lookup with normalized URL
	path, err := getTokenCachePath(startURL)
	if err != nil {
		return nil, err
	}

	data, err := os.ReadFile(path)
	if err == nil {
		var token TokenCache
		if err := json.Unmarshal(data, &token); err != nil {
			log.Printf("⚠️ Warning: Failed to parse token cache %s (possible corruption): %v", path, err)
		} else {
			return &token, nil
		}
	}

	// If direct lookup fails, scan cache files for matching startUrl
	// This handles tokens created by other tools with different URL formats
	return findTokenByStartURL(startURL)
}

// findTokenByStartURL scans the user's SSO cache (~/.aws/sso/cache) for a token whose StartUrl matches the provided startURL after normalization.
// It ignores directories, non-`.json` files, and files prefixed with "botocore-client-id-"; unreadable or unparseable files are skipped.
// If a matching token is found it is returned. If no match is found or the cache directory does not exist, (nil, nil) is returned.
// Any error encountered while reading the cache directory is returned.
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

// SaveTokenCache writes a botocore-compatible SSO token cache file for the given start URL and region.
// It serializes a TokenCache containing the normalized StartUrl, Region, AccessToken, ExpiresAt, and the current ReceivedAt timestamp, and writes it to the per-start-URL cache file with file mode 0600.
// Returns an error if the cache path cannot be determined, JSON marshaling fails, or the file cannot be written.
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

// ClearTokenCache removes all SSO token cache JSON files from the user's
// ~/.aws/sso/cache directory except for botocore client-registration files.
// It returns nil if the cache directory does not exist and otherwise
// propagates any filesystem errors encountered while reading or removing files.
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
		// Skip directories and non-JSON files
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		// Preserve client registration files (valid for 90 days)
		if strings.HasPrefix(entry.Name(), "botocore-client-id-") {
			continue
		}
		cachePath := filepath.Join(cacheDir, entry.Name())
		if err := os.Remove(cachePath); err != nil {
			return err
		}
	}

	return nil
}

// getClientRegistrationCachePath returns the filesystem path for the botocore-style
// client registration cache file for the given region. It ensures the ~/.aws/sso/cache
// directory exists. An error is returned if the user home directory cannot be determined
// or the cache directory cannot be created.
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

// LoadClientRegistration reads the botocore-style client registration cache for the given region.
// It returns the cached ClientRegistrationCache if present, `nil, nil` when no cache file exists, or an error if reading or JSON unmarshalling fails.
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

// SaveClientRegistration writes botocore-compatible client registration data for the given region into the SSO cache directory.
// The stored record contains the client ID, client secret, the provided expiration time, and the current receipt timestamp; an error is returned if the cache path cannot be determined or the file cannot be written.
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
