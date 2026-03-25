#!/bin/bash
# enable-gzip.sh — Add gzip compression to the nginx config for deepspaceplace.com
#
# Run on the Linode server:
#   curl -fsSL https://raw.githubusercontent.com/exploded/deepspaceplace/master/scripts/enable-gzip.sh | sudo bash

set -e

NGINX_CONF=/etc/nginx/sites-available/deepspaceplace

if ! [ -f "$NGINX_CONF" ]; then
    echo "ERROR: $NGINX_CONF not found"
    exit 1
fi

if grep -q 'gzip on' "$NGINX_CONF"; then
    echo "gzip already enabled in $NGINX_CONF — nothing to do."
    exit 0
fi

# Insert gzip block after client_max_body_size line
sed -i '/client_max_body_size/a\
\
    gzip on;\
    gzip_vary on;\
    gzip_proxied any;\
    gzip_comp_level 6;\
    gzip_min_length 256;\
    gzip_types text/plain text/css text/javascript application/javascript\
               application/json application/geo+json image/svg+xml;' "$NGINX_CONF"

echo "Testing nginx config..."
nginx -t

echo "Reloading nginx..."
systemctl reload nginx

echo "Done — gzip is now enabled."
