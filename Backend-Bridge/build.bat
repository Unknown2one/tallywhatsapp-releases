@echo off
REM Build and validate the WhatsApp Bridge

echo Building WhatsApp Bridge...
go build -o whatsapp-bridge.exe

if %ERRORLEVEL% neq 0 (
    echo Build failed!
    exit /b 1
)

echo Build successful!
echo.
echo To run with default configuration:
echo   whatsapp-bridge.exe
echo.
echo To run with custom config file:
echo   whatsapp-bridge.exe (place config.json in same directory)
echo.
echo To run with environment variables:
echo   set PORT=9090
echo   set LOG_LEVEL=debug
echo   whatsapp-bridge.exe
