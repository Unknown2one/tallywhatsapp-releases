# Configuration

This package provides configuration management for the WhatsApp bridge application.

## Usage

```go
import "whatsapp-client/config"

// Load configuration from default location (config.json)
cfg, err := config.LoadConfig("")
if err != nil {
    log.Fatal(err)
}

// Load from specific file
cfg, err := config.LoadConfig("custom-config.json")
if err != nil {
    log.Fatal(err)
}

// Use configuration
fmt.Printf("Server will run on %s\n", cfg.GetServerAddress())
```

## Configuration Priority

The configuration is loaded with the following priority (highest to lowest):

1. **Environment Variables** - Override any config value
2. **Configuration File** - Values from config.json
3. **Default Values** - Built-in sensible defaults

## Configuration File

Create a `config.json` file in the project root. See `config.template.json` for an example.

```json
{
  "server": {
    "port": 8080,
    "host": "localhost"
  },
  "whatsapp": {
    "timeout_seconds": 60,
    "auto_reconnect": true
  },
  "files": {
    "max_size_mb": 100,
    "allowed_types": ["pdf", "jpg", "png", "jpeg", "doc", "docx"]
  },
  "logging": {
    "level": "info"
  },
  "retry": {
    "max_retries": 3,
    "timeout_seconds": 30
  }
}
```

## Environment Variables

You can override any configuration value using environment variables:

| Variable | Description | Example |
|----------|-------------|---------|
| `PORT` | HTTP server port | `8080` |
| `HOST` | HTTP server host | `localhost` |
| `WHATSAPP_TIMEOUT` | WhatsApp connection timeout in seconds | `60` |
| `WHATSAPP_AUTO_RECONNECT` | Auto-reconnect on disconnect | `true` |
| `MAX_FILE_SIZE` | Maximum file size in MB | `100` |
| `ALLOWED_FILE_TYPES` | Comma-separated allowed file extensions | `pdf,jpg,png` |
| `LOG_LEVEL` | Logging level (debug/info/warn/error) | `info` |
| `MAX_RETRIES` | Maximum retry attempts | `3` |
| `RETRY_TIMEOUT` | Retry timeout in seconds | `30` |

### Example

```bash
# Linux/macOS
export PORT=9090
export LOG_LEVEL=debug
./whatsapp-bridge

# Windows PowerShell
$env:PORT=9090
$env:LOG_LEVEL="debug"
.\whatsapp-bridge.exe
```

## Configuration Options

### Server Configuration

- **port**: HTTP server port (1-65535, default: 8080)
- **host**: HTTP server host (default: "localhost")

### WhatsApp Configuration

- **timeout_seconds**: Connection timeout in seconds (minimum: 1, default: 60)
- **auto_reconnect**: Automatically reconnect on disconnect (default: true)

### Files Configuration

- **max_size_mb**: Maximum file upload size in MB (minimum: 1, default: 100)
- **allowed_types**: List of allowed file extensions (default: ["pdf", "jpg", "png", "jpeg", "doc", "docx", "txt", "csv", "xlsx"])

### Logging Configuration

- **level**: Log level - one of: debug, info, warn, error (default: "info")

### Retry Configuration

- **max_retries**: Maximum number of retry attempts (minimum: 0, default: 3)
- **timeout_seconds**: Timeout for retry operations in seconds (minimum: 1, default: 30)

## Helper Methods

The Config struct provides helpful methods:

- `GetMaxFileSizeBytes()`: Returns max file size in bytes
- `IsFileTypeAllowed(filename)`: Checks if a file type is allowed
- `GetServerAddress()`: Returns formatted server address (host:port)
- `Validate()`: Validates all configuration values

## Validation

The configuration is automatically validated when loaded. Invalid values will return an error:

- Port must be between 1 and 65535
- Host cannot be empty
- Timeouts must be at least 1 second
- File size must be at least 1 MB
- At least one file type must be allowed
- Log level must be one of: debug, info, warn, error
- Max retries must be non-negative
