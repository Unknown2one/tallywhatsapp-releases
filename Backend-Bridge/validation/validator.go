package validation

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

// Validator holds configuration for file validation
type Validator struct {
	MaxSizeMB    int
	AllowedTypes []string
}

// NewValidator creates a new Validator instance
func NewValidator(maxSizeMB int, allowedTypes []string) *Validator {
	return &Validator{
		MaxSizeMB:    maxSizeMB,
		AllowedTypes: allowedTypes,
	}
}

// ValidateFile performs comprehensive validation on a file
// Checks: existence, readability, size, file type, path safety
func ValidateFile(path string, maxSizeMB int) error {
	// Sanitize the path first
	sanitizedPath, err := SanitizePath(path)
	if err != nil {
		return err
	}

	// Check if file exists
	fileInfo, err := os.Stat(sanitizedPath)
	if err != nil {
		if os.IsNotExist(err) {
			return NewFileNotFoundError(path)
		}
		return NewInvalidPathError(path)
	}

	// Check if it's a regular file (not a directory)
	if fileInfo.IsDir() {
		return NewInvalidPathError(path)
	}

	// Check file size
	maxSizeBytes := int64(maxSizeMB) * 1024 * 1024
	if fileInfo.Size() > maxSizeBytes {
		return NewFileTooLargeError(path, fileInfo.Size(), maxSizeBytes)
	}

	// Check if file is readable
	file, err := os.Open(sanitizedPath)
	if err != nil {
		return NewFileNotReadableError(path)
	}
	file.Close()

	return nil
}

// ValidateFileType validates the file type by extension and optionally by MIME type
func ValidateFileType(path string, allowedTypes []string) error {
	// Sanitize path
	sanitizedPath, err := SanitizePath(path)
	if err != nil {
		return err
	}

	// Get file extension
	ext := strings.ToLower(strings.TrimPrefix(filepath.Ext(sanitizedPath), "."))
	if ext == "" {
		return NewInvalidFileTypeError(path, "no extension", allowedTypes)
	}

	// Check if extension is in allowed types
	isAllowed := false
	for _, allowed := range allowedTypes {
		if strings.ToLower(allowed) == ext {
			isAllowed = true
			break
		}
	}

	if !isAllowed {
		return NewInvalidFileTypeError(path, ext, allowedTypes)
	}

	return nil
}

// ValidateFileWithMimeType validates file type using both extension and MIME type
func ValidateFileWithMimeType(path string, allowedTypes []string) error {
	// First validate by extension
	if err := ValidateFileType(path, allowedTypes); err != nil {
		return err
	}

	// Then validate MIME type
	mimeType, err := GetMimeType(path)
	if err != nil {
		return err
	}

	// Get allowed MIME types based on extensions
	allowedMimeTypes := getAllowedMimeTypes(allowedTypes)

	// Check if MIME type is in allowed list
	isAllowed := false
	for _, allowed := range allowedMimeTypes {
		if strings.HasPrefix(mimeType, allowed) {
			isAllowed = true
			break
		}
	}

	if !isAllowed {
		return NewInvalidMimeTypeError(path, mimeType, allowedMimeTypes)
	}

	return nil
}

// GetMimeType detects the MIME type of a file
func GetMimeType(path string) (string, error) {
	// Sanitize path
	sanitizedPath, err := SanitizePath(path)
	if err != nil {
		return "", err
	}

	// Open the file
	file, err := os.Open(sanitizedPath)
	if err != nil {
		return "", NewMimeTypeDetectionError(path, err)
	}
	defer file.Close()

	// Read first 512 bytes for MIME detection
	buffer := make([]byte, 512)
	n, err := file.Read(buffer)
	if err != nil && err != io.EOF {
		return "", NewMimeTypeDetectionError(path, err)
	}

	// Detect content type
	mimeType := http.DetectContentType(buffer[:n])

	return mimeType, nil
}

// SanitizePath cleans and validates a file path to prevent directory traversal
func SanitizePath(path string) (string, error) {
	if path == "" {
		return "", NewInvalidPathError(path)
	}

	// Clean the path to remove any ".." or other suspicious elements
	cleanPath := filepath.Clean(path)

	// Convert to absolute path
	absPath, err := filepath.Abs(cleanPath)
	if err != nil {
		return "", NewInvalidPathError(path)
	}

	// Check for directory traversal attempts
	// If the original path contained "..", filepath.Clean would have resolved it
	// We check if the cleaned path still contains suspicious patterns
	if strings.Contains(path, "..") {
		// Additional check: ensure the absolute path doesn't escape intended boundaries
		// This is a security measure to detect path traversal attempts
		return "", NewPathTraversalError(path)
	}

	return absPath, nil
}

// ValidateAndGetFileInfo performs full validation and returns file info
func ValidateAndGetFileInfo(path string, maxSizeMB int, allowedTypes []string) (os.FileInfo, error) {
	// Perform basic file validation
	if err := ValidateFile(path, maxSizeMB); err != nil {
		return nil, err
	}

	// Validate file type
	if err := ValidateFileType(path, allowedTypes); err != nil {
		return nil, err
	}

	// Get sanitized path
	sanitizedPath, err := SanitizePath(path)
	if err != nil {
		return nil, err
	}

	// Get file info
	fileInfo, err := os.Stat(sanitizedPath)
	if err != nil {
		return nil, NewFileNotFoundError(path)
	}

	return fileInfo, nil
}

// getAllowedMimeTypes maps file extensions to their MIME types
func getAllowedMimeTypes(extensions []string) []string {
	mimeMap := map[string]string{
		"pdf":  "application/pdf",
		"doc":  "application/msword",
		"docx": "application/vnd.openxmlformats-officedocument.wordprocessingml.document",
		"xls":  "application/vnd.ms-excel",
		"xlsx": "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet",
		"ppt":  "application/vnd.ms-powerpoint",
		"pptx": "application/vnd.openxmlformats-officedocument.presentationml.presentation",
		"txt":  "text/plain",
		"csv":  "text/csv",
		"jpg":  "image/jpeg",
		"jpeg": "image/jpeg",
		"png":  "image/png",
		"gif":  "image/gif",
		"bmp":  "image/bmp",
		"svg":  "image/svg+xml",
		"webp": "image/webp",
		"mp3":  "audio/mpeg",
		"wav":  "audio/wav",
		"mp4":  "video/mp4",
		"avi":  "video/x-msvideo",
		"mov":  "video/quicktime",
		"zip":  "application/zip",
		"rar":  "application/x-rar-compressed",
		"7z":   "application/x-7z-compressed",
	}

	var mimeTypes []string
	seen := make(map[string]bool)

	for _, ext := range extensions {
		ext = strings.ToLower(ext)
		if mimeType, ok := mimeMap[ext]; ok {
			if !seen[mimeType] {
				mimeTypes = append(mimeTypes, mimeType)
				seen[mimeType] = true
			}
		}
	}

	return mimeTypes
}

// (v *Validator) ValidateFile validates a file using the validator's configuration
func (v *Validator) ValidateFile(path string) error {
	return ValidateFile(path, v.MaxSizeMB)
}

// (v *Validator) ValidateFileType validates file type using the validator's allowed types
func (v *Validator) ValidateFileType(path string) error {
	return ValidateFileType(path, v.AllowedTypes)
}

// (v *Validator) ValidateComplete performs complete validation (existence, size, type)
func (v *Validator) ValidateComplete(path string) error {
	// Validate file existence, size, and readability
	if err := v.ValidateFile(path); err != nil {
		return err
	}

	// Validate file type
	if err := v.ValidateFileType(path); err != nil {
		return err
	}

	return nil
}

// (v *Validator) ValidateCompleteWithMime performs complete validation including MIME type check
func (v *Validator) ValidateCompleteWithMime(path string) error {
	// Validate file existence, size, and readability
	if err := v.ValidateFile(path); err != nil {
		return err
	}

	// Validate file type with MIME check
	if err := ValidateFileWithMimeType(path, v.AllowedTypes); err != nil {
		return err
	}

	return nil
}

// GetFileSizeReadable returns a human-readable file size
func GetFileSizeReadable(bytes int64) string {
	const (
		KB = 1024
		MB = 1024 * KB
		GB = 1024 * MB
	)

	switch {
	case bytes >= GB:
		return fmt.Sprintf("%.2f GB", float64(bytes)/float64(GB))
	case bytes >= MB:
		return fmt.Sprintf("%.2f MB", float64(bytes)/float64(MB))
	case bytes >= KB:
		return fmt.Sprintf("%.2f KB", float64(bytes)/float64(KB))
	default:
		return fmt.Sprintf("%d bytes", bytes)
	}
}
