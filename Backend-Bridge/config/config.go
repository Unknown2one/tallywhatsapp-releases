package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// ServerConfig defines HTTP server settings
type ServerConfig struct {
	Port int    `json:"port"`
	Host string `json:"host"`
}

// WhatsAppConfig defines WhatsApp client settings
type WhatsAppConfig struct {
	TimeoutSeconds int  `json:"timeout_seconds"`
	AutoReconnect  bool `json:"auto_reconnect"`
}

// FilesConfig defines file upload settings
type FilesConfig struct {
	MaxSizeMB     int      `json:"max_size_mb"`
	AllowedTypes  []string `json:"allowed_types"`
}

// LoggingConfig defines logging settings
type LoggingConfig struct {
	Level string `json:"level"`
}

// RetryConfig defines retry behavior for operations
type RetryConfig struct {
	MaxRetries     int `json:"max_retries"`
	TimeoutSeconds int `json:"timeout_seconds"`
}

// Config represents the complete application configuration
type Config struct {
	Server   ServerConfig   `json:"server"`
	WhatsApp WhatsAppConfig `json:"whatsapp"`
	Files    FilesConfig    `json:"files"`
	Logging  LoggingConfig  `json:"logging"`
	Retry    RetryConfig    `json:"retry"`
}

// GetDefaults returns a Config with sensible default values
func GetDefaults() *Config {
	return &Config{
		Server: ServerConfig{
			Port: 8080,
			Host: "localhost",
		},
		WhatsApp: WhatsAppConfig{
			TimeoutSeconds: 60,
			AutoReconnect:  true,
		},
		Files: FilesConfig{
			MaxSizeMB:    100,
			AllowedTypes: []string{"pdf", "jpg", "png", "jpeg", "doc", "docx", "txt", "csv", "xlsx"},
		},
		Logging: LoggingConfig{
			Level: "info",
		},
		Retry: RetryConfig{
			MaxRetries:     3,
			TimeoutSeconds: 30,
		},
	}
}

// LoadConfig loads configuration from file and environment variables
// Priority: Environment Variables > Config File > Defaults
func LoadConfig(configPath string) (*Config, error) {
	// Start with defaults
	cfg := GetDefaults()

	// Try to load from config file if it exists
	if configPath == "" {
		configPath = "config.json"
	}

	if _, err := os.Stat(configPath); err == nil {
		if err := loadFromFile(cfg, configPath); err != nil {
			return nil, fmt.Errorf("failed to load config from %s: %w", configPath, err)
		}
	} else if !os.IsNotExist(err) {
		return nil, fmt.Errorf("failed to check config file: %w", err)
	}

	// Override with environment variables
	applyEnvOverrides(cfg)

	// Validate configuration
	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("invalid configuration: %w", err)
	}

	return cfg, nil
}

// loadFromFile reads configuration from a JSON file
func loadFromFile(cfg *Config, path string) error {
	absPath, err := filepath.Abs(path)
	if err != nil {
		return fmt.Errorf("failed to resolve config path: %w", err)
	}

	data, err := os.ReadFile(absPath)
	if err != nil {
		return fmt.Errorf("failed to read config file: %w", err)
	}

	if err := json.Unmarshal(data, cfg); err != nil {
		return fmt.Errorf("failed to parse config JSON: %w", err)
	}

	return nil
}

// applyEnvOverrides applies environment variable overrides to the config
func applyEnvOverrides(cfg *Config) {
	// Server configuration
	if port := os.Getenv("PORT"); port != "" {
		if p, err := strconv.Atoi(port); err == nil {
			cfg.Server.Port = p
		}
	}
	if host := os.Getenv("HOST"); host != "" {
		cfg.Server.Host = host
	}

	// WhatsApp configuration
	if timeout := os.Getenv("WHATSAPP_TIMEOUT"); timeout != "" {
		if t, err := strconv.Atoi(timeout); err == nil {
			cfg.WhatsApp.TimeoutSeconds = t
		}
	}
	if autoReconnect := os.Getenv("WHATSAPP_AUTO_RECONNECT"); autoReconnect != "" {
		cfg.WhatsApp.AutoReconnect = strings.ToLower(autoReconnect) == "true"
	}

	// Files configuration
	if maxSize := os.Getenv("MAX_FILE_SIZE"); maxSize != "" {
		if size, err := strconv.Atoi(maxSize); err == nil {
			cfg.Files.MaxSizeMB = size
		}
	}
	if allowedTypes := os.Getenv("ALLOWED_FILE_TYPES"); allowedTypes != "" {
		cfg.Files.AllowedTypes = strings.Split(allowedTypes, ",")
	}

	// Logging configuration
	if logLevel := os.Getenv("LOG_LEVEL"); logLevel != "" {
		cfg.Logging.Level = logLevel
	}

	// Retry configuration
	if maxRetries := os.Getenv("MAX_RETRIES"); maxRetries != "" {
		if retries, err := strconv.Atoi(maxRetries); err == nil {
			cfg.Retry.MaxRetries = retries
		}
	}
	if retryTimeout := os.Getenv("RETRY_TIMEOUT"); retryTimeout != "" {
		if timeout, err := strconv.Atoi(retryTimeout); err == nil {
			cfg.Retry.TimeoutSeconds = timeout
		}
	}
}

// Validate checks if the configuration values are valid
func (c *Config) Validate() error {
	// Validate server configuration
	if c.Server.Port < 1 || c.Server.Port > 65535 {
		return fmt.Errorf("invalid server port: %d (must be between 1 and 65535)", c.Server.Port)
	}
	if c.Server.Host == "" {
		return fmt.Errorf("server host cannot be empty")
	}

	// Validate WhatsApp configuration
	if c.WhatsApp.TimeoutSeconds < 1 {
		return fmt.Errorf("invalid WhatsApp timeout: %d (must be at least 1 second)", c.WhatsApp.TimeoutSeconds)
	}

	// Validate files configuration
	if c.Files.MaxSizeMB < 1 {
		return fmt.Errorf("invalid max file size: %d (must be at least 1 MB)", c.Files.MaxSizeMB)
	}
	if len(c.Files.AllowedTypes) == 0 {
		return fmt.Errorf("allowed file types cannot be empty")
	}

	// Validate logging configuration
	validLogLevels := map[string]bool{
		"debug": true,
		"info":  true,
		"warn":  true,
		"error": true,
	}
	if !validLogLevels[strings.ToLower(c.Logging.Level)] {
		return fmt.Errorf("invalid log level: %s (must be one of: debug, info, warn, error)", c.Logging.Level)
	}

	// Validate retry configuration
	if c.Retry.MaxRetries < 0 {
		return fmt.Errorf("invalid max retries: %d (must be non-negative)", c.Retry.MaxRetries)
	}
	if c.Retry.TimeoutSeconds < 1 {
		return fmt.Errorf("invalid retry timeout: %d (must be at least 1 second)", c.Retry.TimeoutSeconds)
	}

	return nil
}

// GetMaxFileSizeBytes returns the maximum file size in bytes
func (c *Config) GetMaxFileSizeBytes() int64 {
	return int64(c.Files.MaxSizeMB) * 1024 * 1024
}

// IsFileTypeAllowed checks if a file type is allowed based on extension
func (c *Config) IsFileTypeAllowed(filename string) bool {
	ext := strings.ToLower(strings.TrimPrefix(filepath.Ext(filename), "."))
	for _, allowed := range c.Files.AllowedTypes {
		if strings.ToLower(allowed) == ext {
			return true
		}
	}
	return false
}

// GetServerAddress returns the full server address (host:port)
func (c *Config) GetServerAddress() string {
	return fmt.Sprintf("%s:%d", c.Server.Host, c.Server.Port)
}
