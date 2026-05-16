#!/bin/bash
# Build and validate the WhatsApp Bridge

echo "Building WhatsApp Bridge..."
go build -o whatsapp-bridge

if [ $? -ne 0 ]; then
    echo "Build failed!"
    exit 1
fi

echo "Build successful!"
echo ""
echo "To run with default configuration:"
echo "  ./whatsapp-bridge"
echo ""
echo "To run with custom config file:"
echo "  ./whatsapp-bridge (place config.json in same directory)"
echo ""
echo "To run with environment variables:"
echo "  export PORT=9090"
echo "  export LOG_LEVEL=debug"
echo "  ./whatsapp-bridge"
