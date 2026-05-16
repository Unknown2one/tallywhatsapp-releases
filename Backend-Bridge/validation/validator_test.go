package validation

import (
	"os"
	"path/filepath"
	"testing"
)

// TestValidateFile tests file validation
func TestValidateFile(t *testing.T) {
	// Create a temporary test file
	tmpDir := t.TempDir()
	testFile := filepath.Join(tmpDir, "test.txt")
	
	// Create test file with some content
	content := []byte("This is a test file")
	if err := os.WriteFile(testFile, content, 0644); err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}

	tests := []struct {
		name      string
		path      string
		maxSizeMB int
		wantError bool
		errorType ValidationErrorType
	}{
		{
			name:      "Valid file",
			path:      testFile,
			maxSizeMB: 10,
			wantError: false,
		},
		{
			name:      "File too large",
			path:      testFile,
			maxSizeMB: 0, // 0 MB max
			wantError: true,
			errorType: ErrorFileTooLarge,
		},
		{
			name:      "File not found",
			path:      filepath.Join(tmpDir, "nonexistent.txt"),
			maxSizeMB: 10,
			wantError: true,
			errorType: ErrorFileNotFound,
		},
		{
			name:      "Directory instead of file",
			path:      tmpDir,
			maxSizeMB: 10,
			wantError: true,
			errorType: ErrorInvalidPath,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateFile(tt.path, tt.maxSizeMB)
			
			if tt.wantError {
				if err == nil {
					t.Errorf("ValidateFile() expected error but got nil")
					return
				}
				
				if validErr, ok := err.(*ValidationError); ok {
					if validErr.Type != tt.errorType {
						t.Errorf("ValidateFile() error type = %v, want %v", validErr.Type, tt.errorType)
					}
				} else {
					t.Errorf("ValidateFile() error is not ValidationError type")
				}
			} else {
				if err != nil {
					t.Errorf("ValidateFile() unexpected error: %v", err)
				}
			}
		})
	}
}

// TestValidateFileType tests file type validation
func TestValidateFileType(t *testing.T) {
	tmpDir := t.TempDir()
	
	tests := []struct {
		name         string
		fileName     string
		allowedTypes []string
		wantError    bool
	}{
		{
			name:         "PDF allowed",
			fileName:     "document.pdf",
			allowedTypes: []string{"pdf", "doc", "docx"},
			wantError:    false,
		},
		{
			name:         "Image allowed",
			fileName:     "photo.jpg",
			allowedTypes: []string{"jpg", "jpeg", "png"},
			wantError:    false,
		},
		{
			name:         "Case insensitive extension",
			fileName:     "document.PDF",
			allowedTypes: []string{"pdf"},
			wantError:    false,
		},
		{
			name:         "Disallowed type",
			fileName:     "script.exe",
			allowedTypes: []string{"pdf", "doc", "jpg"},
			wantError:    true,
		},
		{
			name:         "No extension",
			fileName:     "noextension",
			allowedTypes: []string{"pdf", "doc"},
			wantError:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create test file
			testFile := filepath.Join(tmpDir, tt.fileName)
			if err := os.WriteFile(testFile, []byte("test"), 0644); err != nil {
				t.Fatalf("Failed to create test file: %v", err)
			}

			err := ValidateFileType(testFile, tt.allowedTypes)
			
			if tt.wantError && err == nil {
				t.Errorf("ValidateFileType() expected error but got nil")
			}
			if !tt.wantError && err != nil {
				t.Errorf("ValidateFileType() unexpected error: %v", err)
			}
		})
	}
}

// TestSanitizePath tests path sanitization
func TestSanitizePath(t *testing.T) {
	tests := []struct {
		name      string
		path      string
		wantError bool
		errorType ValidationErrorType
	}{
		{
			name:      "Valid path",
			path:      "test.txt",
			wantError: false,
		},
		{
			name:      "Valid path with subdirectory",
			path:      filepath.Join("subdir", "test.txt"),
			wantError: false,
		},
		{
			name:      "Path traversal attempt",
			path:      "../../../etc/passwd",
			wantError: true,
			errorType: ErrorPathTraversal,
		},
		{
			name:      "Empty path",
			path:      "",
			wantError: true,
			errorType: ErrorInvalidPath,
		},
		{
			name:      "Path with dots",
			path:      "test..txt",
			wantError: false, // This is valid - it's just a filename with dots
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := SanitizePath(tt.path)
			
			if tt.wantError {
				if err == nil {
					t.Errorf("SanitizePath() expected error but got nil")
					return
				}
				
				if validErr, ok := err.(*ValidationError); ok {
					if validErr.Type != tt.errorType {
						t.Errorf("SanitizePath() error type = %v, want %v", validErr.Type, tt.errorType)
					}
				}
			} else {
				if err != nil {
					t.Errorf("SanitizePath() unexpected error: %v", err)
				}
				if result == "" {
					t.Errorf("SanitizePath() returned empty string for valid path")
				}
			}
		})
	}
}

// TestGetMimeType tests MIME type detection
func TestGetMimeType(t *testing.T) {
	tmpDir := t.TempDir()

	tests := []struct {
		name         string
		fileName     string
		content      []byte
		expectedMime string
	}{
		{
			name:         "Text file",
			fileName:     "test.txt",
			content:      []byte("Hello, World!"),
			expectedMime: "text/plain",
		},
		{
			name:     "PDF file (header)",
			fileName: "test.pdf",
			// PDF magic bytes
			content:      []byte("%PDF-1.4\n"),
			expectedMime: "application/pdf",
		},
		{
			name:     "HTML file",
			fileName: "test.html",
			content:  []byte("<!DOCTYPE html><html><body>Test</body></html>"),
			expectedMime: "text/html",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			testFile := filepath.Join(tmpDir, tt.fileName)
			if err := os.WriteFile(testFile, tt.content, 0644); err != nil {
				t.Fatalf("Failed to create test file: %v", err)
			}

			mimeType, err := GetMimeType(testFile)
			if err != nil {
				t.Errorf("GetMimeType() unexpected error: %v", err)
				return
			}

			// Check if MIME type starts with expected prefix
			// (DetectContentType might add charset, etc.)
			if len(mimeType) < len(tt.expectedMime) || mimeType[:len(tt.expectedMime)] != tt.expectedMime {
				t.Errorf("GetMimeType() = %v, want prefix %v", mimeType, tt.expectedMime)
			}
		})
	}
}

// TestValidator tests the Validator struct methods
func TestValidator(t *testing.T) {
	tmpDir := t.TempDir()
	testFile := filepath.Join(tmpDir, "test.pdf")
	
	// Create test file
	if err := os.WriteFile(testFile, []byte("test content"), 0644); err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}

	validator := NewValidator(10, []string{"pdf", "doc", "jpg"})

	t.Run("ValidateComplete - valid file", func(t *testing.T) {
		err := validator.ValidateComplete(testFile)
		if err != nil {
			t.Errorf("ValidateComplete() unexpected error: %v", err)
		}
	})

	t.Run("ValidateComplete - wrong type", func(t *testing.T) {
		wrongFile := filepath.Join(tmpDir, "test.exe")
		if err := os.WriteFile(wrongFile, []byte("test"), 0644); err != nil {
			t.Fatalf("Failed to create test file: %v", err)
		}

		err := validator.ValidateComplete(wrongFile)
		if err == nil {
			t.Errorf("ValidateComplete() expected error for wrong file type")
		}
	})
}

// TestValidationError tests ValidationError methods
func TestValidationError(t *testing.T) {
	err := NewFileNotFoundError("test.pdf")

	t.Run("Error method", func(t *testing.T) {
		errMsg := err.Error()
		if errMsg == "" {
			t.Errorf("Error() returned empty string")
		}
	})

	t.Run("ToJSON method", func(t *testing.T) {
		jsonMap := err.ToJSON()
		
		if jsonMap["success"] != false {
			t.Errorf("ToJSON() success should be false")
		}
		
		if jsonMap["error"] != string(ErrorFileNotFound) {
			t.Errorf("ToJSON() error type mismatch")
		}
		
		if jsonMap["code"] != 404 {
			t.Errorf("ToJSON() status code = %v, want 404", jsonMap["code"])
		}
	})
}

// TestGetFileSizeReadable tests human-readable file size formatting
func TestGetFileSizeReadable(t *testing.T) {
	tests := []struct {
		name     string
		bytes    int64
		expected string
	}{
		{
			name:     "Bytes",
			bytes:    500,
			expected: "500 bytes",
		},
		{
			name:     "Kilobytes",
			bytes:    2048,
			expected: "2.00 KB",
		},
		{
			name:     "Megabytes",
			bytes:    5 * 1024 * 1024,
			expected: "5.00 MB",
		},
		{
			name:     "Gigabytes",
			bytes:    3 * 1024 * 1024 * 1024,
			expected: "3.00 GB",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := GetFileSizeReadable(tt.bytes)
			if result != tt.expected {
				t.Errorf("GetFileSizeReadable() = %v, want %v", result, tt.expected)
			}
		})
	}
}

// TestValidateAndGetFileInfo tests the combined validation and info retrieval
func TestValidateAndGetFileInfo(t *testing.T) {
	tmpDir := t.TempDir()
	testFile := filepath.Join(tmpDir, "test.pdf")
	content := []byte("test content")
	
	if err := os.WriteFile(testFile, content, 0644); err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}

	fileInfo, err := ValidateAndGetFileInfo(testFile, 10, []string{"pdf", "doc"})
	if err != nil {
		t.Errorf("ValidateAndGetFileInfo() unexpected error: %v", err)
		return
	}

	if fileInfo == nil {
		t.Errorf("ValidateAndGetFileInfo() returned nil fileInfo")
		return
	}

	if fileInfo.Size() != int64(len(content)) {
		t.Errorf("ValidateAndGetFileInfo() size = %v, want %v", fileInfo.Size(), len(content))
	}
}
