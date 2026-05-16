package validation

import "fmt"

// ValidationErrorType represents the type of validation error
type ValidationErrorType string

const (
	ErrorFileNotFound        ValidationErrorType = "file_not_found"
	ErrorFileNotReadable     ValidationErrorType = "file_not_readable"
	ErrorFileTooLarge        ValidationErrorType = "file_too_large"
	ErrorInvalidFileType     ValidationErrorType = "invalid_file_type"
	ErrorInvalidPath         ValidationErrorType = "invalid_path"
	ErrorPathTraversal       ValidationErrorType = "path_traversal_detected"
	ErrorMimeTypeDetection   ValidationErrorType = "mime_type_detection_failed"
	ErrorInvalidMimeType     ValidationErrorType = "invalid_mime_type"
)

// ValidationError represents a file validation error with HTTP status code
type ValidationError struct {
	Type       ValidationErrorType
	Message    string
	Path       string
	StatusCode int
}

// Error implements the error interface
func (e *ValidationError) Error() string {
	return e.Message
}

// ToJSON converts the error to a JSON-friendly map
func (e *ValidationError) ToJSON() map[string]interface{} {
	return map[string]interface{}{
		"success": false,
		"error":   string(e.Type),
		"message": e.Message,
		"code":    e.StatusCode,
	}
}

// NewValidationError creates a new ValidationError
func NewValidationError(errType ValidationErrorType, message, path string, statusCode int) *ValidationError {
	return &ValidationError{
		Type:       errType,
		Message:    message,
		Path:       path,
		StatusCode: statusCode,
	}
}

// NewFileNotFoundError creates a file not found error
func NewFileNotFoundError(path string) *ValidationError {
	return &ValidationError{
		Type:       ErrorFileNotFound,
		Message:    fmt.Sprintf("The specified file does not exist: %s", path),
		Path:       path,
		StatusCode: 404,
	}
}

// NewFileNotReadableError creates a file not readable error
func NewFileNotReadableError(path string) *ValidationError {
	return &ValidationError{
		Type:       ErrorFileNotReadable,
		Message:    fmt.Sprintf("The file cannot be read (check permissions): %s", path),
		Path:       path,
		StatusCode: 403,
	}
}

// NewFileTooLargeError creates a file too large error
func NewFileTooLargeError(path string, size, maxSize int64) *ValidationError {
	return &ValidationError{
		Type:       ErrorFileTooLarge,
		Message:    fmt.Sprintf("File size (%d bytes) exceeds maximum allowed size (%d bytes): %s", size, maxSize, path),
		Path:       path,
		StatusCode: 413,
	}
}

// NewInvalidFileTypeError creates an invalid file type error
func NewInvalidFileTypeError(path, fileType string, allowedTypes []string) *ValidationError {
	return &ValidationError{
		Type:       ErrorInvalidFileType,
		Message:    fmt.Sprintf("File type '%s' is not allowed. Allowed types: %v. File: %s", fileType, allowedTypes, path),
		Path:       path,
		StatusCode: 415,
	}
}

// NewInvalidPathError creates an invalid path error
func NewInvalidPathError(path string) *ValidationError {
	return &ValidationError{
		Type:       ErrorInvalidPath,
		Message:    fmt.Sprintf("The provided file path is invalid: %s", path),
		Path:       path,
		StatusCode: 400,
	}
}

// NewPathTraversalError creates a path traversal error
func NewPathTraversalError(path string) *ValidationError {
	return &ValidationError{
		Type:       ErrorPathTraversal,
		Message:    fmt.Sprintf("Path traversal attempt detected in file path: %s", path),
		Path:       path,
		StatusCode: 403,
	}
}

// NewMimeTypeDetectionError creates a MIME type detection error
func NewMimeTypeDetectionError(path string, err error) *ValidationError {
	return &ValidationError{
		Type:       ErrorMimeTypeDetection,
		Message:    fmt.Sprintf("Failed to detect MIME type for file %s: %v", path, err),
		Path:       path,
		StatusCode: 500,
	}
}

// NewInvalidMimeTypeError creates an invalid MIME type error
func NewInvalidMimeTypeError(path, mimeType string, allowedMimeTypes []string) *ValidationError {
	return &ValidationError{
		Type:       ErrorInvalidMimeType,
		Message:    fmt.Sprintf("MIME type '%s' is not allowed. Allowed types: %v. File: %s", mimeType, allowedMimeTypes, path),
		Path:       path,
		StatusCode: 415,
	}
}
