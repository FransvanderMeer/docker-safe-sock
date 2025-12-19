#!/bin/bash
set -e

if [ "$EUID" -ne 0 ]; then
  echo "Please run as root"
  exit 1
fi

echo "Building docker-safe-sock..."
# Ensure go is in path if running with sudo
export PATH=$PATH:/usr/local/go/bin

if ! command -v go &> /dev/null; then
    echo "Go is not installed or not in PATH. Please install Go to build from source."
    exit 1
fi

go build -o docker-safe-sock .

echo "Installing binary to /usr/local/bin/..."
cp docker-safe-sock /usr/local/bin/
chmod +x /usr/local/bin/docker-safe-sock

echo "Installing systemd service..."
cp docker-safe-sock.service /etc/systemd/system/

echo "Creating default configuration (if not exists)..."
if [ ! -f /etc/default/docker-safe-sock ]; then
    cat <<EOF > /etc/default/docker-safe-sock
# Docker Safe Sock Configuration

# Address to listen on (comma separated)
# To listen on multiple IPs: DSS_ADDR=127.0.0.1:2375,192.168.1.5:2375
# To listen on all Docker bridges: DSS_ADDR=auto:bridge
# To listen on localhost AND bridges: DSS_ADDR=127.0.0.1:2375,auto:bridge
# To listen on localhost AND bridges: DSS_ADDR=127.0.0.1:2375,auto:bridge
DSS_ADDR=127.0.0.1:2375

# Path to create safe Unix socket (optional, for local non-root usage)
# Creates /run/docker-safe-sock/docker-safe.sock
DSS_SAFE_SOCKET=/run/docker-safe-sock/docker-safe.sock

# Path to Docker socket
DSS_SOCKET=/var/run/docker.sock
EOF
fi

systemctl daemon-reload

echo "Enabling and starting service..."
systemctl enable docker-safe-sock
systemctl restart docker-safe-sock

echo "Done! docker-safe-sock is running on port 2375."
systemctl status docker-safe-sock --no-pager
