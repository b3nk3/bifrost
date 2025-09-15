package sso

import (
	"crypto/sha1"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

type TokenCache struct {
	AccessToken  string    `json:"accessToken"`
	ExpiresAt    time.Time `json:"expiresAt"`
	RefreshToken string    `json:"refreshToken"`
	ClientId     string    `json:"clientId"`
	ClientSecret string    `json:"clientSecret"`
	StartUrl     string    `json:"startUrl"`
	Region       string    `json:"region"`
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

	// Generate hash of start URL for filename
	hash := fmt.Sprintf("%x", sha1.Sum([]byte(startURL)))
	return filepath.Join(cacheDir, hash+".json"), nil
}

func LoadTokenCache(startURL string) (*TokenCache, error) {
	path, err := getTokenCachePath(startURL)
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

	var token TokenCache
	if err := json.Unmarshal(data, &token); err != nil {
		return nil, err
	}

	return &token, nil
}

func SaveTokenCache(token *TokenCache) error {
	path, err := getTokenCachePath(token.StartUrl)
	if err != nil {
		return err
	}

	data, err := json.Marshal(token)
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
