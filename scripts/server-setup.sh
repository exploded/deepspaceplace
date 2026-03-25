#!/bin/bash
# server-setup.sh — One-time migration of deepspaceplace.com from PHP to Go
#
# Run as root on the Linode server:
#   curl -fsSL https://raw.githubusercontent.com/exploded/deepspaceplace/master/scripts/server-setup.sh -o /tmp/server-setup.sh && sudo bash /tmp/server-setup.sh
#
# What this does:
#   1. Backs up PHP files to old-site/
#   2. Creates .env with production settings
#   3. Creates systemd service (deepspaceplace)
#   4. Creates nginx config (proxy to :8686)
#   5. Creates deploy script + sudoers for GitHub Actions
#
# After running:
#   1. Edit /var/www/deepspaceplace.com/.env (set ADMIN_PASSWORD)
#   2. FTP images to /var/www/deepspaceplace.com/images/
#   3. Add DEPLOY_* secrets to the GitHub repo (same key as moon)
#   4. Push to master to trigger first deploy
#   5. SSH in and seed the database:
#      sudo -u www-data sqlite3 /var/www/deepspaceplace.com/deepspaceplace.db < /var/www/deepspaceplace.com/db/seed.sql

set -e

SITE_DIR=/var/www/deepspaceplace.com
SERVICE_NAME=deepspaceplace
SERVICE_USER=www-data
SERVICE_GROUP=www-data
DEPLOY_USER=deploy

echo "============================================="
echo " Deep Space Place — PHP to Go Migration"
echo "============================================="
echo ""

# ---------------------------------------------------------------
# 1. Back up PHP site
# ---------------------------------------------------------------
echo "[1/5] Backing up existing PHP site..."

if [ -d "$SITE_DIR/old-site" ]; then
    echo "  old-site/ already exists — skipping backup"
else
    mkdir -p "$SITE_DIR/old-site"

    # Move PHP files
    for f in "$SITE_DIR"/*.php; do
        [ -f "$f" ] && mv "$f" "$SITE_DIR/old-site/"
    done

    # Move all old directories (Go deploy will provide fresh copies)
    for dir in include css js mike meteor data files images; do
        [ -d "$SITE_DIR/$dir" ] && mv "$SITE_DIR/$dir" "$SITE_DIR/old-site/"
    done

    # Remove phpMyAdmin symlink
    [ -L "$SITE_DIR/phpmyadmin" ] && rm "$SITE_DIR/phpmyadmin"

    # Move old favicon/robots too — Go deploy provides its own
    for f in favicon.ico robots.txt; do
        [ -f "$SITE_DIR/$f" ] && mv "$SITE_DIR/$f" "$SITE_DIR/old-site/"
    done

    echo "  Everything moved to $SITE_DIR/old-site/"
    echo "  Site directory is now clean for Go app"
fi

# ---------------------------------------------------------------
# 2. Create .env
# ---------------------------------------------------------------
echo "[2/5] Creating .env..."

if [ -f "$SITE_DIR/.env" ]; then
    echo "  .env already exists — skipping"
else
    cat > "$SITE_DIR/.env" << 'EOF'
PORT=8686
PROD=True
ADMIN_USER=admin
ADMIN_PASSWORD=CHANGE_ME_NOW
EOF
    chmod 600 "$SITE_DIR/.env"
    echo "  Created $SITE_DIR/.env"
    echo "  >>> EDIT THE ADMIN_PASSWORD BEFORE GOING LIVE <<<"
fi

# ---------------------------------------------------------------
# 3. Create systemd service
# ---------------------------------------------------------------
echo "[3/5] Creating systemd service..."

cat > /etc/systemd/system/deepspaceplace.service << EOF
[Unit]
Description=Deep Space Place
After=network.target

[Service]
Type=simple
User=$SERVICE_USER
Group=$SERVICE_GROUP
WorkingDirectory=$SITE_DIR
ExecStart=$SITE_DIR/deepspaceplace
EnvironmentFile=$SITE_DIR/.env
Restart=always
RestartSec=5

[Install]
WantedBy=multi-user.target
EOF

systemctl daemon-reload
systemctl enable deepspaceplace
echo "  Created and enabled deepspaceplace.service"
echo "  (won't start until binary is deployed)"

# ---------------------------------------------------------------
# 4. Create nginx config
# ---------------------------------------------------------------
echo "[4/5] Creating nginx config..."

cat > /etc/nginx/sites-available/deepspaceplace << 'NGINX'
server {
    listen 80;
    listen [::]:80;
    server_name deepspaceplace.com www.deepspaceplace.com;
    return 301 https://$host$request_uri;
}

server {
    listen 443 ssl http2;
    listen [::]:443 ssl http2;
    server_name deepspaceplace.com www.deepspaceplace.com;

    ssl_certificate     /etc/letsencrypt/live/deepspaceplace.com/fullchain.pem;
    ssl_certificate_key /etc/letsencrypt/live/deepspaceplace.com/privkey.pem;

    client_max_body_size 20M;

    location / {
        proxy_pass http://127.0.0.1:8686;
        proxy_set_header Host              $host;
        proxy_set_header X-Real-IP         $remote_addr;
        proxy_set_header X-Forwarded-For   $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;
        proxy_read_timeout 60s;
    }
}
NGINX

ln -sf /etc/nginx/sites-available/deepspaceplace /etc/nginx/sites-enabled/deepspaceplace

echo "  Testing nginx config..."
nginx -t
systemctl reload nginx
echo "  Nginx config active (will 502 until first deploy)"

# ---------------------------------------------------------------
# 5. Deploy script + sudoers
# ---------------------------------------------------------------
echo "[5/5] Setting up deployment..."

cat > /usr/local/bin/deploy-deepspaceplace << 'DEPLOY'
#!/bin/bash
# /usr/local/bin/deploy-deepspaceplace
# Runs as root (via sudo) during GitHub Actions deployments.
#
# To update: include scripts/deploy-deepspaceplace in the SCP bundle —
# it will self-update and re-exec before doing anything else.

set -e

DEPLOY_SRC="${1:-/tmp/deepspaceplace-deploy}"
DEPLOY_DIR=/var/www/deepspaceplace.com

# Self-update: if the bundle contains a newer version, install and re-exec.
BUNDLE_SCRIPT="$DEPLOY_SRC/scripts/deploy-deepspaceplace"
if [ -f "$BUNDLE_SCRIPT" ] && ! diff -q /usr/local/bin/deploy-deepspaceplace "$BUNDLE_SCRIPT" > /dev/null 2>&1; then
    echo "[deploy] Updating deploy script from bundle..."
    install -m 755 "$BUNDLE_SCRIPT" /usr/local/bin/deploy-deepspaceplace
    exec /usr/local/bin/deploy-deepspaceplace "$@"
fi

# Read service owner from systemd — no hardcoded username
SERVICE_USER=$(systemctl show deepspaceplace --property=User --value)
SERVICE_GROUP=$(systemctl show deepspaceplace --property=Group --value)

if [ -z "$SERVICE_USER" ]; then
    echo "[deploy] ERROR: Could not read User from deepspaceplace.service"
    exit 1
fi

echo "[deploy] Stopping service..."
systemctl stop deepspaceplace || true

echo "[deploy] Installing binary to $DEPLOY_DIR/deepspaceplace..."
rm -f "$DEPLOY_DIR/deepspaceplace"
cp "$DEPLOY_SRC/deepspaceplace" "$DEPLOY_DIR/deepspaceplace"
chmod +x "$DEPLOY_DIR/deepspaceplace"

echo "[deploy] Updating web assets..."
cp -r "$DEPLOY_SRC/templates/"  "$DEPLOY_DIR/"
cp -r "$DEPLOY_SRC/static/"    "$DEPLOY_DIR/"
cp -r "$DEPLOY_SRC/data/"      "$DEPLOY_DIR/"
cp -r "$DEPLOY_SRC/files/"     "$DEPLOY_DIR/"
cp -r "$DEPLOY_SRC/db/"        "$DEPLOY_DIR/"
cp -f "$DEPLOY_SRC/favicon.ico" "$DEPLOY_DIR/" 2>/dev/null || true
cp -f "$DEPLOY_SRC/robots.txt"  "$DEPLOY_DIR/" 2>/dev/null || true

chown -R "$SERVICE_USER:$SERVICE_GROUP" "$DEPLOY_DIR"

echo "[deploy] Starting service..."
systemctl start deepspaceplace

echo "[deploy] Verifying service is active..."
sleep 2
if ! systemctl is-active --quiet deepspaceplace; then
    echo "[deploy] ERROR: Service failed to start. Status:"
    systemctl status deepspaceplace --no-pager --lines=30
    exit 1
fi

echo "[deploy] Cleaning up..."
rm -rf "$DEPLOY_SRC"

echo "[deploy] Done — deepspaceplace is running."
DEPLOY

chmod +x /usr/local/bin/deploy-deepspaceplace

# Sudoers
cat > /etc/sudoers.d/deepspaceplace-deploy << 'EOF'
deploy ALL=(ALL) NOPASSWD: /usr/local/bin/deploy-deepspaceplace
deploy ALL=(ALL) NOPASSWD: /usr/bin/systemctl stop deepspaceplace
EOF

chmod 440 /etc/sudoers.d/deepspaceplace-deploy
visudo -c -f /etc/sudoers.d/deepspaceplace-deploy
echo "  Deploy script and sudoers configured"

# ---------------------------------------------------------------
# Set ownership
# ---------------------------------------------------------------
chown -R "$SERVICE_USER:$SERVICE_GROUP" "$SITE_DIR"

echo ""
echo "============================================="
echo " Setup complete!"
echo "============================================="
echo ""
echo " Next steps:"
echo "   1. Edit $SITE_DIR/.env — set ADMIN_PASSWORD"
echo "      sudo nano $SITE_DIR/.env"
echo ""
echo "   2. FTP images to $SITE_DIR/images/"
echo ""
echo "   3. Add GitHub secrets to the deepspaceplace repo"
echo "      (same DEPLOY_HOST, DEPLOY_USER, DEPLOY_SSH_KEY as moon)"
echo ""
echo "   4. Push to master — first deploy will build and start the Go app"
echo ""
echo "   5. After first deploy, seed the database:"
echo "      sudo -u www-data sqlite3 $SITE_DIR/deepspaceplace.db < $SITE_DIR/db/seed.sql"
echo ""
echo " The site will 502 until the first GitHub deploy completes."
echo ""
