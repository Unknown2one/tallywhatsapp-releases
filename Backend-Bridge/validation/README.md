# File Validation Package

This package provides comprehensive file validation and error handling for the WhatsApp bridge service.

## Features

- **File existence validation**: Checks if files exist before processing
- **File type validation**: Validates files by extension and MIME type
- **File size validation**: Enforces maximum file size limits from configuration
- **Path sanitization**: Prevents directory traversal attacks
- **Read permissions check**: Ensures files are readable
- **Custom error types**: Detailed, user-friendly error messages with HTTP status codes

## Usage

### Basic Validation

```go
import "whatsapp-client/validation"

// Create a validator with config
validator := validation.NewValidator(100, []string{"pdf", "doc", "docx", "jpg", "png"})

// Validate a file (checks existence, size, type)
err := validator.ValidateComplete("path/to/file.pdf")
if err != nil {
    // Handle validation error
    if validErr, ok := err.(*validation.ValidationError); ok {
        // Access error details
        fmt.Printf("Error: %s (code: %d)\n", validErr.Message, validErr.StatusCode)
    }
}
```

### Individual Validation Functions

```go
// Validate file existence and size
err := validation.ValidateFile("path/to/file.pdf", 100) // 100 MB max

// Validate file type by extension
err := validation.ValidateFileType("path/to/file.pdf", []string{"pdf", "doc"})

// Get MIME type
mimeType, err := validation.GetMimeType("path/to/file.pdf")

// Sanitize and validate path (prevents directory traversal)
safePath, err := validation.SanitizePath("path/to/file.pdf")
```

### Validation with MIME Type Check

```go
// Validate with both extension and MIME type
err := validator.ValidateCompleteWithMime("path/to/file.pdf")
```

### Get File Information

```go
// Validate and get file info in one call
fileInfo, err := validation.ValidateAndGetFileInfo(
    "path/to/file.pdf",
    100, // max size in MB
    []string{"pdf", "doc"},
)
if err == nil {
    fmt.Printf("File size: %d bytes\n", fileInfo.Size())
}
```

## Error Types

The package defines the following error types:

- `ErrorFileNotFound` (404): File does not exist
- `ErrorFileNotReadable` (403): File cannot be read (permission issue)
- `ErrorFileTooLarge` (413): File exceeds maximum size
- `ErrorInvalidFileType` (415): File extension not allowed
- `ErrorInvalidPath` (400): Invalid file path
- `ErrorPathTraversal` (403): Path traversal attempt detected
- `ErrorMimeTypeDetection` (500): Failed to detect MIME type
- `ErrorInvalidMimeType` (415): MIME type not allowed

## Error Response Format

When a validation error occurs, it can be converted to JSON format:

```json
{
  "success": false,
  "error": "file_not_found",
  "message": "The specified file does not exist: C:\\path\\file.pdf",
  "code": 404
}
```

## Integration with API Handlers

```go
import (
    "whatsapp-client/validation"
    "encoding/json"
    "net/http"
)

func (h *Handler) SendFile(w http.ResponseWriter, r *http.Request) {
    // ... parse request ...
    
    // Validate file
    if err := h.Validator.ValidateComplete(req.FilePath); err != nil {
        if validErr, ok := err.(*validation.ValidationError); ok {
            // Respond with validation error details
            w.Header().Set("Content-Type", "application/json")
            w.WriteHeader(validErr.StatusCode)
            json.NewEncoder(w).Encode(validErr.ToJSON())
            return
        }
    }
    
    // Proceed with file processing...
}
```

## Supported File Types

The validator supports the following file types (configurable):

**Documents:**
- PDF (`.pdf`)
- Microsoft Word (`.doc`, `.docx`)
- Microsoft Excel (`.xls`, `.xlsx`)
- Microsoft PowerPoint (`.ppt`, `.pptx`)
- Text files (`.txt`, `.csv`)

**Images:**
- JPEG (`.jpg`, `.jpeg`)
- PNG (`.png`)
- GIF (`.gif`)
- BMP (`.bmp`)
- SVG (`.svg`)
- WebP (`.webp`)

**Media:**
- MP3 (`.mp3`)
- WAV (`.wav`)
- MP4 (`.mp4`)
- AVI (`.avi`)
- MOV (`.mov`)

**Archives:**
- ZIP (`.zip`)
- RAR (`.rar`)
- 7-Zip (`.7z`)

## Configuration

File validation settings are defined in the application configuration:

```json
{
  "files": {
    "max_size_mb": 100,
    "allowed_types": ["pdf", "doc", "docx", "jpg", "jpeg", "png"]
  }
}
```

Or via environment variables:
- `MAX_FILE_SIZE`: Maximum file size in MB
- `ALLOWED_FILE_TYPES`: Comma-separated list of allowed extensions

## Testing

Run the validation tests:

```bash
go test -v ./validation/...
```

Run tests with coverage:

```bash
go test -cover ./validation/...
```

## Security Features

1. **Path Traversal Prevention**: All paths are sanitized to prevent `../` attacks
2. **MIME Type Verification**: Files are validated by both extension and content
3. **Size Limits**: Prevents DoS attacks via large file uploads
4. **Permission Checks**: Ensures files are readable before processing
5. **Detailed Error Messages**: Clear errors without exposing system internals
