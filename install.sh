#!/bin/bash
set -e

# Repoxy Installer
# https://github.com/amirmatini/repoxy

VERSION="${REPOXY_VERSION:-latest}"
INSTALL_DIR="${INSTALL_DIR:-/usr/local/bin}"
CONFIG_DIR="${CONFIG_DIR:-/etc}"
SERVICE_DIR="/etc/systemd/system"
CACHE_DIR="/var/cache/repoxy"
REPO="amirmatini/repoxy"

# Detect architecture
ARCH=$(uname -m)
case $ARCH in
    x86_64)  BINARY="repoxy-linux-amd64" ;;
    aarch64) BINARY="repoxy-linux-arm64" ;;
    arm64)   BINARY="repoxy-linux-arm64" ;;
    *)       echo "Unsupported architecture: $ARCH"; exit 1 ;;
esac

echo "Installing Repoxy..."
echo "Architecture: $ARCH"
echo "Binary: $BINARY"

# Download binary
if [ "$VERSION" = "latest" ]; then
    URL="https://github.com/$REPO/releases/latest/download/$BINARY"
else
    URL="https://github.com/$REPO/releases/download/$VERSION/$BINARY"
fi

echo "Downloading from $URL..."
curl -fsSL "$URL" -o /tmp/repoxy
chmod +x /tmp/repoxy

# Install binary
echo "Installing to $INSTALL_DIR/repoxy..."
sudo mv /tmp/repoxy "$INSTALL_DIR/repoxy"

# Download config if not exists
if [ ! -f "$CONFIG_DIR/repoxy.yaml" ]; then
    echo "Downloading default config..."
    curl -fsSL "https://raw.githubusercontent.com/$REPO/main/config.yaml" -o /tmp/repoxy.yaml
    sudo mv /tmp/repoxy.yaml "$CONFIG_DIR/repoxy.yaml"
    echo "Config installed to $CONFIG_DIR/repoxy.yaml"
    echo "⚠️  Edit $CONFIG_DIR/repoxy.yaml before starting the service"
else
    echo "Config already exists at $CONFIG_DIR/repoxy.yaml"
fi

# Download systemd service
echo "Installing systemd service..."
curl -fsSL "https://raw.githubusercontent.com/$REPO/main/repoxy.service" -o /tmp/repoxy.service
sudo mv /tmp/repoxy.service "$SERVICE_DIR/repoxy.service"
sudo systemctl daemon-reload

# Create cache directory
echo "Creating cache directory..."
sudo mkdir -p "$CACHE_DIR"
sudo chown -R nobody:nogroup "$CACHE_DIR" 2>/dev/null || sudo chown -R nobody:nobody "$CACHE_DIR"

echo ""
echo "✅ Repoxy installed successfully!"
echo ""
echo "Next steps:"
echo "  1. Edit config: sudo nano $CONFIG_DIR/repoxy.yaml"
echo "  2. Start service: sudo systemctl enable --now repoxy"
echo "  3. Check status:  sudo systemctl status repoxy"
echo "  4. View logs:     sudo journalctl -u repoxy -f"
echo ""
