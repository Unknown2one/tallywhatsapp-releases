package validation_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"whatsapp-client/api"
	"whatsapp-client/validation"
)

// MockClient is a mock WhatsApp client for testing
type MockClient struct{}

// MockMessageStore is a mock message store for testing
type MockMessageStore struct{}

// TestFileValidationIntegration demonstrates integration with API handlers
func TestFileValidationIntegration(t *testing.T) {
	// Create temporary directory for test files
	tmpDir := t.TempDir()

	// Create a valid test file
	validFile := filepath.Join(tmpDir, "test.pdf")
	if err := os.WriteFile(validFile, []byte("test content"), 0644); err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}

	// Create an invalid type file
	invalidFile := filepath.Join(tmpDir, "test.exe")
	if err := os.WriteFile(invalidFile, []byte("test"), 0644); err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}

	// Create validator
	validator := validation.NewValidator(10, []string{"pdf", "doc", "jpg", "png"})

	// Create handler (note: in real app, you'd need a real client)
	handler := api.NewHandler(nil, &MockMessageStore{}, validator)

	tests := []struct {
		name           string
		filePath       string
		recipient      string
		expectedStatus int
		expectError    string
	}{
		{
			name:           "Valid file",
			filePath:       validFile,
			recipient:      "1234567890",
			expectedStatus: http.StatusInternalServerError, // Will fail at send but pass validation
		},
		{
			name:           "File not found",
			filePath:       filepath.Join(tmpDir, "nonexistent.pdf"),
			recipient:      "1234567890",
			expectedStatus: http.StatusNotFound,
			expectError:    string(validation.ErrorFileNotFound),
		},
		{
			name:           "Invalid file type",
			filePath:       invalidFile,
			recipient:      "1234567890",
			expectedStatus: http.StatusUnsupportedMediaType,
			expectError:    string(validation.ErrorInvalidFileType),
		},
		{
			name:           "Missing recipient",
			filePath:       validFile,
			recipient:      "",
			expectedStatus: http.StatusBadRequest,
		},
		{
			name:           "Missing file path",
			filePath:       "",
			recipient:      "1234567890",
			expectedStatus: http.StatusBadRequest,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create request
			reqBody := map[string]string{
				"recipient": tt.recipient,
				"file_path": tt.filePath,
				"caption":   "Test caption",
			}
			bodyBytes, _ := json.Marshal(reqBody)

			req := httptest.NewRequest(http.MethodPost, "/api/send-file", bytes.NewReader(bodyBytes))
			req.Header.Set("Content-Type", "application/json")

			// Create response recorder
			rr := httptest.NewRecorder()

			// Call handler
			handler.SendFile(rr, req)

			// Check status code
			if rr.Code != tt.expectedStatus {
				t.Errorf("Expected status %d, got %d", tt.expectedStatus, rr.Code)
			}

			// If we expect an error, check the error response
			if tt.expectError != "" {
				var errResp map[string]interface{}
				if err := json.NewDecoder(rr.Body).Decode(&errResp); err != nil {
					t.Fatalf("Failed to decode error response: %v", err)
				}

				if errResp["error"] != tt.expectError {
					t.Errorf("Expected error '%s', got '%v'", tt.expectError, errResp["error"])
				}

				if errResp["success"] != false {
					t.Errorf("Expected success=false in error response")
				}
			}
		})
	}
}

// TestValidationErrorResponse tests the error response format
func TestValidationErrorResponse(t *testing.T) {
	tests := []struct {
		name           string
		err            *validation.ValidationError
		expectedJSON   map[string]interface{}
		expectedStatus int
	}{
		{
			name: "File not found error",
			err:  validation.NewFileNotFoundError("test.pdf"),
			expectedJSON: map[string]interface{}{
				"success": false,
				"error":   "file_not_found",
			},
			expectedStatus: 404,
		},
		{
			name: "File too large error",
			err:  validation.NewFileTooLargeError("test.pdf", 200000000, 100000000),
			expectedJSON: map[string]interface{}{
				"success": false,
				"error":   "file_too_large",
			},
			expectedStatus: 413,
		},
		{
			name: "Invalid file type error",
			err:  validation.NewInvalidFileTypeError("test.exe", "exe", []string{"pdf", "doc"}),
			expectedJSON: map[string]interface{}{
				"success": false,
				"error":   "invalid_file_type",
			},
			expectedStatus: 415,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			jsonMap := tt.err.ToJSON()

			// Check required fields
			if jsonMap["success"] != tt.expectedJSON["success"] {
				t.Errorf("success mismatch: expected %v, got %v", tt.expectedJSON["success"], jsonMap["success"])
			}

			if jsonMap["error"] != tt.expectedJSON["error"] {
				t.Errorf("error type mismatch: expected %v, got %v", tt.expectedJSON["error"], jsonMap["error"])
			}

			if jsonMap["code"] != tt.expectedStatus {
				t.Errorf("status code mismatch: expected %d, got %v", tt.expectedStatus, jsonMap["code"])
			}

			// Check that message exists and is not empty
			if msg, ok := jsonMap["message"].(string); !ok || msg == "" {
				t.Errorf("message field is missing or empty")
			}
		})
	}
}

// TestPathTraversalPrevention tests that path traversal attacks are prevented
func TestPathTraversalPrevention(t *testing.T) {
	validator := validation.NewValidator(10, []string{"pdf", "txt"})

	dangerousPaths := []string{
		"../../../etc/passwd",
		"..\\..\\..\\windows\\system32\\config\\sam",
		"folder/../../../etc/passwd",
		"./../sensitive/file.txt",
	}

	for _, path := range dangerousPaths {
		t.Run(path, func(t *testing.T) {
			// Sanitize should detect the traversal
			_, err := validation.SanitizePath(path)
			if err == nil {
				t.Errorf("SanitizePath should have detected path traversal in: %s", path)
				return
			}

			// Check it's the right error type
			if validErr, ok := err.(*validation.ValidationError); ok {
				if validErr.Type != validation.ErrorPathTraversal {
					t.Errorf("Expected ErrorPathTraversal, got %v", validErr.Type)
				}
			} else {
				t.Errorf("Expected ValidationError, got %T", err)
			}
		})
	}
}

// BenchmarkFileValidation benchmarks the file validation process
func BenchmarkFileValidation(b *testing.B) {
	// Create a temporary test file
	tmpDir := b.TempDir()
	testFile := filepath.Join(tmpDir, "benchmark.pdf")
	if err := os.WriteFile(testFile, make([]byte, 1024*1024), 0644); err != nil {
		b.Fatalf("Failed to create test file: %v", err)
	}

	validator := validation.NewValidator(10, []string{"pdf", "doc", "jpg"})

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = validator.ValidateComplete(testFile)
	}
}

// BenchmarkMimeTypeDetection benchmarks MIME type detection
func BenchmarkMimeTypeDetection(b *testing.B) {
	tmpDir := b.TempDir()
	testFile := filepath.Join(tmpDir, "test.pdf")
	
	// Create file with PDF header
	content := []byte("%PDF-1.4\n")
	content = append(content, make([]byte, 1024)...)
	if err := os.WriteFile(testFile, content, 0644); err != nil {
		b.Fatalf("Failed to create test file: %v", err)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = validation.GetMimeType(testFile)
	}
}
