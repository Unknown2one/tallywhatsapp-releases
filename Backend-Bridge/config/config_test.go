package config

import (
	"os"
	"testing"
)

func TestGetDefaults(t *testing.T) {
	cfg := GetDefaults()

	if cfg.Server.Port != 8080 {
		t.Errorf("Expected default port 8080, got %d", cfg.Server.Port)
	}
	if cfg.Server.Host != "localhost" {
		t.Errorf("Expected default host 'localhost', got %s", cfg.Server.Host)
	}
	if cfg.WhatsApp.TimeoutSeconds != 60 {
		t.Errorf("Expected default timeout 60, got %d", cfg.WhatsApp.TimeoutSeconds)
	}
	if cfg.Files.MaxSizeMB != 100 {
		t.Errorf("Expected default max size 100, got %d", cfg.Files.MaxSizeMB)
	}
	if cfg.Logging.Level != "info" {
		t.Errorf("Expected default log level 'info', got %s", cfg.Logging.Level)
	}
}

func TestValidateValid(t *testing.T) {
	cfg := GetDefaults()
	if err := cfg.Validate(); err != nil {
		t.Errorf("Default configuration should be valid: %v", err)
	}
}

func TestValidateInvalidPort(t *testing.T) {
	cfg := GetDefaults()
	cfg.Server.Port = 100000
	if err := cfg.Validate(); err == nil {
		t.Error("Expected validation error for invalid port")
	}

	cfg.Server.Port = 0
	if err := cfg.Validate(); err == nil {
		t.Error("Expected validation error for port 0")
	}
}

func TestValidateInvalidLogLevel(t *testing.T) {
	cfg := GetDefaults()
	cfg.Logging.Level = "invalid"
	if err := cfg.Validate(); err == nil {
		t.Error("Expected validation error for invalid log level")
	}
}

func TestValidateInvalidFileSize(t *testing.T) {
	cfg := GetDefaults()
	cfg.Files.MaxSizeMB = 0
	if err := cfg.Validate(); err == nil {
		t.Error("Expected validation error for invalid file size")
	}
}

func TestGetMaxFileSizeBytes(t *testing.T) {
	cfg := GetDefaults()
	cfg.Files.MaxSizeMB = 100
	expected := int64(100 * 1024 * 1024)
	if cfg.GetMaxFileSizeBytes() != expected {
		t.Errorf("Expected %d bytes, got %d", expected, cfg.GetMaxFileSizeBytes())
	}
}

func TestIsFileTypeAllowed(t *testing.T) {
	cfg := GetDefaults()
	cfg.Files.AllowedTypes = []string{"pdf", "jpg", "png"}

	tests := []struct {
		filename string
		allowed  bool
	}{
		{"document.pdf", true},
		{"image.jpg", true},
		{"photo.PNG", true}, // Case insensitive
		{"file.doc", false},
		{"archive.zip", false},
		{"test.PDF", true}, // Case insensitive
	}

	for _, test := range tests {
		result := cfg.IsFileTypeAllowed(test.filename)
		if result != test.allowed {
			t.Errorf("IsFileTypeAllowed(%s) = %v, expected %v", test.filename, result, test.allowed)
		}
	}
}

func TestGetServerAddress(t *testing.T) {
	cfg := GetDefaults()
	cfg.Server.Host = "0.0.0.0"
	cfg.Server.Port = 9090

	expected := "0.0.0.0:9090"
	if cfg.GetServerAddress() != expected {
		t.Errorf("Expected %s, got %s", expected, cfg.GetServerAddress())
	}
}

func TestEnvOverrides(t *testing.T) {
	// Set environment variables
	os.Setenv("PORT", "9090")
	os.Setenv("HOST", "0.0.0.0")
	os.Setenv("LOG_LEVEL", "debug")
	os.Setenv("MAX_FILE_SIZE", "200")
	defer func() {
		os.Unsetenv("PORT")
		os.Unsetenv("HOST")
		os.Unsetenv("LOG_LEVEL")
		os.Unsetenv("MAX_FILE_SIZE")
	}()

	cfg, err := LoadConfig("nonexistent.json")
	if err != nil {
		t.Fatalf("LoadConfig failed: %v", err)
	}

	if cfg.Server.Port != 9090 {
		t.Errorf("Expected port 9090 from env, got %d", cfg.Server.Port)
	}
	if cfg.Server.Host != "0.0.0.0" {
		t.Errorf("Expected host '0.0.0.0' from env, got %s", cfg.Server.Host)
	}
	if cfg.Logging.Level != "debug" {
		t.Errorf("Expected log level 'debug' from env, got %s", cfg.Logging.Level)
	}
	if cfg.Files.MaxSizeMB != 200 {
		t.Errorf("Expected max size 200 from env, got %d", cfg.Files.MaxSizeMB)
	}
}

func TestLoadConfigNonexistentFile(t *testing.T) {
	// Should not error if file doesn't exist, should use defaults
	cfg, err := LoadConfig("nonexistent-file.json")
	if err != nil {
		t.Fatalf("LoadConfig should not fail for nonexistent file: %v", err)
	}

	// Should have default values
	if cfg.Server.Port != 8080 {
		t.Errorf("Expected default port, got %d", cfg.Server.Port)
	}
}
