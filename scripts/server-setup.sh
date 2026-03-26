#!/bin/bash
# One-time server setup for deepspaceplace
# Usage: curl -fsSL https://raw.githubusercontent.com/exploded/deepspaceplace/master/scripts/server-setup.sh | sudo bash

set -e

APP=deepspaceplace
APP_DIR=/var/www/$APP
DEPLOY_USER=deploy
DEPLOY_HOME=/home/$DEPLOY_USER
SSH_DIR="$DEPLOY_HOME/.ssh"
KEY_FILE="$SSH_DIR/github_actions"

echo "=== Setting up $APP ==="

# 1. Create deploy user (reuse if exists)
if ! id "$DEPLOY_USER" &>/dev/null; then
    echo "[setup] Creating deploy user..."
    useradd -m -s /bin/bash "$DEPLOY_USER"
else
    echo "[setup] Deploy user already exists."
fi

# 2. SSH key pair (reuse if exists)
mkdir -p "$SSH_DIR"
if [ ! -f "$KEY_FILE" ]; then
    echo "[setup] Generating SSH key pair..."
    ssh-keygen -t ed25519 -f "$KEY_FILE" -N "" -C "github-actions-deploy"
    cat "$KEY_FILE.pub" >> "$SSH_DIR/authorized_keys"
    chmod 600 "$SSH_DIR/authorized_keys"
else
    echo "[setup] SSH key already exists."
fi
chown -R "$DEPLOY_USER:$DEPLOY_USER" "$SSH_DIR"
chmod 700 "$SSH_DIR"

# 3. Application directory
echo "[setup] Creating application directory..."
mkdir -p "$APP_DIR"
chown -R www-data:www-data "$APP_DIR"

# 4. .env template
if [ ! -f "$APP_DIR/.env" ]; then
    echo "[setup] Creating .env template..."
    cat > "$APP_DIR/.env" <<'ENVEOF'
PORT=8686
PROD=True
ADMIN_USER=admin
ADMIN_PASSWORD=CHANGEME
ASTROMETRY_API_KEY=
MONITOR_URL=
MONITOR_API_KEY=
ENVEOF
    chown www-data:www-data "$APP_DIR/.env"
    chmod 600 "$APP_DIR/.env"
    echo "[setup] IMPORTANT: Edit $APP_DIR/.env with real values!"
else
    echo "[setup] .env already exists, skipping."
fi

# 5. Systemd service
echo "[setup] Installing systemd service..."
cat > /etc/systemd/system/$APP.service <<EOF
[Unit]
Description=Deep Space Place astrophotography website
After=network.target

[Service]
Type=simple
User=www-data
Group=www-data
WorkingDirectory=$APP_DIR
EnvironmentFile=$APP_DIR/.env
ExecStart=$APP_DIR/$APP
Restart=on-failure
RestartSec=5

# Security hardening
NoNewPrivileges=true
PrivateTmp=true
ProtectSystem=full
ProtectHome=true

[Install]
WantedBy=multi-user.target
EOF
systemctl daemon-reload

# 6. Deploy script
echo "[setup] Installing deploy script..."
if [ -f "scripts/deploy-$APP" ]; then
    install -m 755 "scripts/deploy-$APP" /usr/local/bin/deploy-$APP
else
    echo "[setup] WARNING: scripts/deploy-$APP not found. Deploy script must be installed manually."
fi

# 7. Sudoers
echo "[setup] Configuring sudoers..."
cat > /tmp/$APP-sudoers <<EOF
deploy ALL=(ALL) NOPASSWD: /usr/local/bin/deploy-$APP
deploy ALL=(ALL) NOPASSWD: /usr/bin/systemctl stop $APP
EOF
if visudo -cf /tmp/$APP-sudoers; then
    mv /tmp/$APP-sudoers /etc/sudoers.d/$APP-deploy
    chmod 440 /etc/sudoers.d/$APP-deploy
    echo "[setup] Sudoers configured."
else
    echo "[setup] ERROR: Sudoers validation failed!"
    rm -f /tmp/$APP-sudoers
    exit 1
fi

# 8. Print GitHub secrets
echo ""
echo "=== Setup complete ==="
echo ""
echo "Add these secrets to GitHub (Settings > Secrets > Actions):"
echo ""
echo "  DEPLOY_HOST     = $(hostname -I | awk '{print $1}')"
echo "  DEPLOY_USER     = $DEPLOY_USER"
echo "  DEPLOY_PORT     = 22"
echo "  DEPLOY_SSH_KEY  = (contents of $KEY_FILE)"
echo ""
echo "To view the private key:"
echo "  cat $KEY_FILE"
echo ""
echo "Don't forget to edit $APP_DIR/.env with real values!"
