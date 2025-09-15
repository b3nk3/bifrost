package config

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/viper"
)

// SSOProfile represents SSO authentication configuration
type SSOProfile struct {
	StartURL  string `yaml:"sso_url" mapstructure:"sso_url"`
	SSORegion string `yaml:"sso_region" mapstructure:"sso_region"`
}

// ConnectionProfile represents a connection configuration
type ConnectionProfile struct {
	SSOProfile  string `yaml:"sso_profile" mapstructure:"sso_profile"`
	AccountID   string `yaml:"account_id" mapstructure:"account_id"`
	RoleName    string `yaml:"role_name" mapstructure:"role_name"`
	Region      string `yaml:"region" mapstructure:"region"`
	Environment string `yaml:"environment" mapstructure:"environment"`
	ServiceType string `yaml:"service" mapstructure:"service"`
	Port        string `yaml:"port" mapstructure:"port"`
}

// Config represents the application configuration
type Config struct {
	SSOProfiles        map[string]SSOProfile        `yaml:"sso_profiles" mapstructure:"sso_profiles"`
	ConnectionProfiles map[string]ConnectionProfile `yaml:"connection_profiles" mapstructure:"connection_profiles"`
}

// Manager handles configuration operations
type Manager struct {
	viper *viper.Viper
}

// NewManager creates a new configuration manager
func NewManager() *Manager {
	v := viper.New()
	v.SetConfigType("yaml")
	return &Manager{viper: v}
}

// LocalConfig represents local project configuration (connection profiles only)
type LocalConfig struct {
	ConnectionProfiles map[string]ConnectionProfile `yaml:"connection_profiles" mapstructure:"connection_profiles"`
}

// Load loads the configuration from disk, merging global SSO profiles with local connection profiles
func (m *Manager) Load() (*Config, error) {
	config := &Config{
		SSOProfiles:        make(map[string]SSOProfile),
		ConnectionProfiles: make(map[string]ConnectionProfile),
	}

	// Load global SSO profiles
	if err := m.loadGlobalConfig(config); err != nil {
		return nil, fmt.Errorf("failed to load global config: %w", err)
	}

	// Load local connection profiles (if exists)
	if err := m.loadLocalConfig(config); err != nil {
		return nil, fmt.Errorf("failed to load local config: %w", err)
	}

	return config, nil
}

// loadGlobalConfig loads SSO profiles and global connection profiles from ~/.bifrost/config.yaml
func (m *Manager) loadGlobalConfig(config *Config) error {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return err
	}

	configDir := filepath.Join(homeDir, ".bifrost")
	if err := os.MkdirAll(configDir, 0755); err != nil {
		return err
	}

	configFile := filepath.Join(configDir, "config.yaml")

	// Check if file exists
	if _, err := os.Stat(configFile); os.IsNotExist(err) {
		// Create empty file with basic structure
		initialConfig := `sso_profiles: {}
connection_profiles: {}
`
		if err := os.WriteFile(configFile, []byte(initialConfig), 0644); err != nil {
			return fmt.Errorf("failed to create config file: %w", err)
		}
	}

	globalViper := viper.New()
	globalViper.SetConfigType("yaml")
	globalViper.SetConfigFile(configFile)

	if err := globalViper.ReadInConfig(); err != nil {
		return fmt.Errorf("failed to read global config: %w", err)
	}

	if err := globalViper.Unmarshal(config); err != nil {
		return fmt.Errorf("failed to parse global config: %w", err)
	}

	return nil
}

// loadLocalConfig loads connection profiles from .bifrost.config.yaml in current directory
func (m *Manager) loadLocalConfig(config *Config) error {
	localConfigFile := ".bifrost.config.yaml"
	
	// Check if local config exists
	if _, err := os.Stat(localConfigFile); os.IsNotExist(err) {
		return nil // No local config is fine
	}

	localConfig := &LocalConfig{
		ConnectionProfiles: make(map[string]ConnectionProfile),
	}

	localViper := viper.New()
	localViper.SetConfigType("yaml")
	localViper.SetConfigFile(localConfigFile)

	if err := localViper.ReadInConfig(); err != nil {
		return fmt.Errorf("failed to read local config: %w", err)
	}

	if err := localViper.Unmarshal(localConfig); err != nil {
		return fmt.Errorf("failed to parse local config: %w", err)
	}

	// Merge local connection profiles (local takes priority)
	for name, profile := range localConfig.ConnectionProfiles {
		config.ConnectionProfiles[name] = profile
	}

	return nil
}

// Save saves the global configuration to disk (SSO profiles only go to global config)
func (m *Manager) Save(config *Config) error {
	return m.SaveGlobal(config)
}

// SaveGlobal saves the global configuration to ~/.bifrost/config.yaml
func (m *Manager) SaveGlobal(config *Config) error {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return err
	}

	configDir := filepath.Join(homeDir, ".bifrost")
	configFile := filepath.Join(configDir, "config.yaml")

	globalViper := viper.New()
	globalViper.SetConfigType("yaml")
	globalViper.SetConfigFile(configFile)
	globalViper.Set("sso_profiles", config.SSOProfiles)
	globalViper.Set("connection_profiles", config.ConnectionProfiles)

	return globalViper.WriteConfig()
}

// SaveLocal saves connection profiles to .bifrost.config.yaml in current directory
func (m *Manager) SaveLocal(connectionProfiles map[string]ConnectionProfile) error {
	localConfigFile := ".bifrost.config.yaml"

	localConfig := &LocalConfig{
		ConnectionProfiles: connectionProfiles,
	}

	localViper := viper.New()
	localViper.SetConfigType("yaml")
	localViper.SetConfigFile(localConfigFile)
	localViper.Set("connection_profiles", localConfig.ConnectionProfiles)

	return localViper.WriteConfig()
}

// AddSSOProfile adds or updates an SSO profile
func (m *Manager) AddSSOProfile(name string, profile SSOProfile) error {
	config, err := m.Load()
	if err != nil {
		return err
	}
	
	config.SSOProfiles[name] = profile
	return m.Save(config)
}

// AddConnectionProfile adds or updates a connection profile
func (m *Manager) AddConnectionProfile(name string, profile ConnectionProfile) error {
	config, err := m.Load()
	if err != nil {
		return err
	}
	
	config.ConnectionProfiles[name] = profile
	return m.Save(config)
}

// AddLocalConnectionProfile adds or updates a connection profile in local config
func (m *Manager) AddLocalConnectionProfile(name string, profile ConnectionProfile) error {
	// Load existing local config
	localProfiles := make(map[string]ConnectionProfile)
	
	// Try to load existing local config
	localConfigFile := ".bifrost.config.yaml"
	if _, err := os.Stat(localConfigFile); err == nil {
		localConfig := &LocalConfig{ConnectionProfiles: make(map[string]ConnectionProfile)}
		localViper := viper.New()
		localViper.SetConfigType("yaml")
		localViper.SetConfigFile(localConfigFile)
		
		if err := localViper.ReadInConfig(); err == nil {
			if err := localViper.Unmarshal(localConfig); err != nil {
				// Log error but continue - local config is optional
				fmt.Printf("Warning: failed to unmarshal local config: %v\n", err)
			} else {
				localProfiles = localConfig.ConnectionProfiles
			}
		}
	}
	
	// Add/update the profile
	localProfiles[name] = profile
	
	// Save to local config
	return m.SaveLocal(localProfiles)
}

// GetDefaultSSOProfile returns the SSO profile name if there's only one, empty string otherwise
func (m *Manager) GetDefaultSSOProfile() (string, error) {
	config, err := m.Load()
	if err != nil {
		return "", err
	}
	
	if len(config.SSOProfiles) == 1 {
		for name := range config.SSOProfiles {
			return name, nil
		}
	}
	
	return "", nil
}

// GetSSOProfile retrieves an SSO profile by name
func (m *Manager) GetSSOProfile(name string) (*SSOProfile, error) {
	config, err := m.Load()
	if err != nil {
		return nil, err
	}
	
	profile, exists := config.SSOProfiles[name]
	if !exists {
		return nil, fmt.Errorf("SSO profile '%s' not found", name)
	}
	
	return &profile, nil
}

// GetConnectionProfile retrieves a connection profile by name
func (m *Manager) GetConnectionProfile(name string) (*ConnectionProfile, error) {
	config, err := m.Load()
	if err != nil {
		return nil, err
	}
	
	profile, exists := config.ConnectionProfiles[name]
	if !exists {
		return nil, fmt.Errorf("connection profile '%s' not found", name)
	}
	
	return &profile, nil
}
