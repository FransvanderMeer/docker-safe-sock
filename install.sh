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

echo "Creating docker-safe user and group..."
if ! getent group docker-safe >/dev/null; then
  groupadd --system docker-safe
fi

if ! getent passwd docker-safe >/dev/null; then
  useradd --system --gid docker-safe --no-create-home --home-dir /run/docker-safe --shell /usr/sbin/nologin docker-safe
fi

echo "Adding docker-safe user to docker group..."
usermod -aG docker docker-safe

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
# To listen on localhost TCP (for legacy reasons): DSS_ADDR=127.0.0.1:2375
# Leave empty to disable TCP listening (Recommended for unix-socket only)
DSS_ADDR=

# Path to create safe Unix socket
# This must match a path inside the RuntimeDirectory (e.g. /run/docker-safe/)
DSS_SAFE_SOCKET=/run/docker-safe/docker.sock

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
