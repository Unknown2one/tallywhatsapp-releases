# WhatsApp Bridge Configuration Guide

This guide explains how to configure the WhatsApp Bridge application.

## Quick Start

The application works out of the box with sensible defaults. No configuration file is required to get started.

```bash
# Just run the application
./whatsapp-bridge
```

## Configuration Methods

The application supports three configuration methods (in order of priority):

1. **Environment Variables** (highest priority)
2. **Configuration File** (config.json)
3. **Default Values** (built-in, lowest priority)

### Method 1: Environment Variables

Set environment variables to override any configuration value:

**Windows (PowerShell):**
```powershell
$env:PORT=9090
$env:LOG_LEVEL="debug"
$env:MAX_FILE_SIZE=200
.\whatsapp-bridge.exe
```

**Windows (Command Prompt):**
```cmd
set PORT=9090
set LOG_LEVEL=debug
set MAX_FILE_SIZE=200
whatsapp-bridge.exe
```

**Linux/macOS:**
```bash
export PORT=9090
export LOG_LEVEL=debug
export MAX_FILE_SIZE=200
./whatsapp-bridge
```

**Available Environment Variables:**

| Variable | Type | Default | Description |
|----------|------|---------|-------------|
| `PORT` | int | 8080 | HTTP server port |
| `HOST` | string | localhost | HTTP server host |
| `WHATSAPP_TIMEOUT` | int | 60 | WhatsApp connection timeout (seconds) |
| `WHATSAPP_AUTO_RECONNECT` | bool | true | Auto-reconnect on disconnect |
| `MAX_FILE_SIZE` | int | 100 | Maximum file size (MB) |
| `ALLOWED_FILE_TYPES` | string | pdf,jpg,... | Comma-separated file extensions |
| `LOG_LEVEL` | string | info | Log level (debug/info/warn/error) |
| `MAX_RETRIES` | int | 3 | Maximum retry attempts |
| `RETRY_TIMEOUT` | int | 30 | Retry timeout (seconds) |

### Method 2: Configuration File

Create a `config.json` file in the same directory as the executable:

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

**To get started:**
```bash
# Copy the template
cp config.template.json config.json

# Edit config.json with your preferred settings
# Then run the application
./whatsapp-bridge
```

### Method 3: Default Values

If no configuration file or environment variables are set, the application uses these defaults:

- **Server Port:** 8080
- **Server Host:** localhost
- **WhatsApp Timeout:** 60 seconds
- **Auto Reconnect:** true
- **Max File Size:** 100 MB
- **Allowed File Types:** pdf, jpg, png, jpeg, doc, docx, txt, csv, xlsx
- **Log Level:** info
- **Max Retries:** 3
- **Retry Timeout:** 30 seconds

## Configuration Options Details

### Server Configuration

**port** (1-65535, default: 8080)
- The port on which the HTTP server listens
- Example: `8080`, `9000`, `3000`

**host** (default: "localhost")
- The host address the server binds to
- Use `"0.0.0.0"` to accept connections from any network interface
- Use `"localhost"` or `"127.0.0.1"` for local-only access

### WhatsApp Configuration

**timeout_seconds** (minimum: 1, default: 60)
- Timeout for WhatsApp connection and QR code scanning
- If QR code is not scanned within this time, connection fails
- Recommended: 60-180 seconds

**auto_reconnect** (default: true)
- Automatically attempt to reconnect if connection is lost
- Set to `false` if you want manual control over reconnection

### Files Configuration

**max_size_mb** (minimum: 1, default: 100)
- Maximum allowed file size for uploads in megabytes
- Helps prevent memory issues and abuse
- WhatsApp has its own limits (typically 100MB for documents)

**allowed_types** (default: ["pdf", "jpg", "png", "jpeg", "doc", "docx", "txt", "csv", "xlsx"])
- List of allowed file extensions (without dots)
- Files with extensions not in this list will be rejected
- Add or remove extensions based on your security requirements

### Logging Configuration

**level** (debug/info/warn/error, default: "info")
- Controls verbosity of logging
- **debug**: Very verbose, shows all operations
- **info**: Normal operations and important events
- **warn**: Warning messages and above
- **error**: Only error messages

### Retry Configuration

**max_retries** (minimum: 0, default: 3)
- Number of times to retry failed operations
- Set to `0` to disable retries
- Higher values increase resilience but may slow down failure detection

**timeout_seconds** (minimum: 1, default: 30)
- Timeout for each retry attempt in seconds
- Total timeout could be: `timeout_seconds * (max_retries + 1)`

## Configuration Validation

The application validates all configuration values on startup. Invalid values will cause the application to exit with an error message.

**Example validation errors:**
```
invalid server port: 100000 (must be between 1 and 65535)
invalid log level: debug2 (must be one of: debug, info, warn, error)
invalid max file size: 0 (must be at least 1 MB)
```

## Examples

### Production Configuration

```json
{
  "server": {
    "port": 8080,
    "host": "0.0.0.0"
  },
  "whatsapp": {
    "timeout_seconds": 120,
    "auto_reconnect": true
  },
  "files": {
    "max_size_mb": 50,
    "allowed_types": ["pdf", "jpg", "png"]
  },
  "logging": {
    "level": "warn"
  },
  "retry": {
    "max_retries": 5,
    "timeout_seconds": 60
  }
}
```

### Development Configuration

```json
{
  "server": {
    "port": 3000,
    "host": "localhost"
  },
  "whatsapp": {
    "timeout_seconds": 60,
    "auto_reconnect": true
  },
  "files": {
    "max_size_mb": 200,
    "allowed_types": ["pdf", "jpg", "png", "jpeg", "doc", "docx", "txt", "csv", "xlsx", "zip"]
  },
  "logging": {
    "level": "debug"
  },
  "retry": {
    "max_retries": 1,
    "timeout_seconds": 10
  }
}
```

### Minimal Configuration

```json
{
  "server": {
    "port": 9090
  },
  "logging": {
    "level": "debug"
  }
}
```
*Other values will use defaults*

## Using with Docker

If running in Docker, you can pass environment variables:

```bash
docker run -d \
  -e PORT=8080 \
  -e LOG_LEVEL=info \
  -e MAX_FILE_SIZE=100 \
  -p 8080:8080 \
  whatsapp-bridge
```

Or mount a config file:

```bash
docker run -d \
  -v $(pwd)/config.json:/app/config.json \
  -p 8080:8080 \
  whatsapp-bridge
```

## Troubleshooting

### Configuration not loading

**Problem:** Changes to config.json are not taking effect

**Solution:**
- Ensure the config.json file is in the same directory as the executable
- Check for JSON syntax errors using a JSON validator
- Restart the application after making changes

### Environment variables not working

**Problem:** Environment variables are not overriding config values

**Solution:**
- Verify the variable names match exactly (case-sensitive on Linux/macOS)
- Check that variables are set in the same shell session where you run the app
- Use `echo $PORT` (Linux/macOS) or `echo %PORT%` (Windows) to verify

### Validation errors

**Problem:** Application exits with validation error

**Solution:**
- Read the error message carefully - it tells you exactly what's wrong
- Check the value is within the allowed range
- For log_level, use lowercase: "debug", "info", "warn", or "error"
- For boolean values in JSON, use `true` or `false` (not quoted)

## Building

To build the application:

**Windows:**
```cmd
build.bat
```

**Linux/macOS:**
```bash
chmod +x build.sh
./build.sh
```

**Manual build:**
```bash
go build -o whatsapp-bridge
```

## Configuration Best Practices

1. **Don't commit secrets**: Add `config.json` to `.gitignore`
2. **Use environment variables in production**: Easier to manage in containerized environments
3. **Start with defaults**: Only override what you need to change
4. **Validate early**: Test configuration changes in development first
5. **Document your changes**: Keep a commented version of your config.json
6. **Use appropriate log levels**:
   - Production: `warn` or `error`
   - Staging: `info`
   - Development: `debug`

## See Also

- [config/README.md](config/README.md) - Detailed API documentation
- [config.template.json](config.template.json) - Example configuration
- [.env.example](.env.example) - Example environment variables
