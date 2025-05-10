#!/bin/sh

# Decode and write credentials if provided
if [ -n "$GOOGLE_CREDS_BASE64" ]; then
    echo "$GOOGLE_CREDS_BASE64" | base64 -d > credentials.json
fi

# Run the application
./crypto_trader