#!/bin/bash
# gather-info.sh - Run on Linode server to collect info for migration
# Usage: bash gather-info.sh
#
# Copy the entire output and paste it back to Claude.

set -e

echo "=========================================="
echo " Deep Space Place - Server Info Gathering"
echo "=========================================="
echo ""

echo "--- OS ---"
cat /etc/os-release 2>/dev/null | head -5
echo ""

echo "--- Hostname & IP ---"
hostname
hostname -I 2>/dev/null
echo ""

echo "--- Disk space ---"
df -h / /var/www 2>/dev/null | head -5
echo ""

echo "--- Nginx: sites-enabled for deepspaceplace ---"
ls -la /etc/nginx/sites-enabled/ 2>/dev/null
echo ""
for f in /etc/nginx/sites-enabled/*deepspace* /etc/nginx/sites-available/*deepspace*; do
    if [ -f "$f" ]; then
        echo "--- Contents of $f ---"
        cat "$f"
        echo ""
    fi
done

echo "--- Nginx: all site configs (in case it uses a different name) ---"
for f in /etc/nginx/sites-enabled/*; do
    if [ -f "$f" ]; then
        echo "  $f -> $(readlink -f "$f" 2>/dev/null || echo 'not a symlink')"
    fi
done
echo ""

echo "--- PHP status ---"
php -v 2>/dev/null || echo "PHP not found"
systemctl list-units --type=service | grep -i php 2>/dev/null || echo "No PHP services found"
echo ""

echo "--- Current /var/www/deepspaceplace.com contents (top level) ---"
ls -la /var/www/deepspaceplace.com/ 2>/dev/null || echo "Directory does not exist"
echo ""

echo "--- Current /var/www/deepspaceplace.com disk usage ---"
du -sh /var/www/deepspaceplace.com/ 2>/dev/null || echo "N/A"
du -sh /var/www/deepspaceplace.com/images/ 2>/dev/null || echo "No images dir"
echo ""

echo "--- Deploy user ---"
id deploy 2>/dev/null || echo "deploy user does not exist"
echo ""

echo "--- Existing sudoers for deploy ---"
ls -la /etc/sudoers.d/ 2>/dev/null
echo ""
cat /etc/sudoers.d/moon-deploy 2>/dev/null || echo "No moon-deploy sudoers"
echo ""

echo "--- Existing systemd services (moon, deepspace) ---"
systemctl status moon --no-pager 2>/dev/null | head -5 || echo "No moon service"
echo ""
systemctl status deepspaceplace --no-pager 2>/dev/null | head -5 || echo "No deepspaceplace service"
echo ""

echo "--- Moon service file (for reference) ---"
cat /etc/systemd/system/moon.service 2>/dev/null || echo "No moon.service file"
echo ""

echo "--- Firewall (ufw) ---"
ufw status 2>/dev/null || echo "ufw not available"
echo ""

echo "--- SSL certs ---"
ls -la /etc/letsencrypt/live/ 2>/dev/null || echo "No letsencrypt certs"
echo ""

echo "=========================================="
echo " Done. Copy everything above and paste it"
echo " back to Claude."
echo "=========================================="
