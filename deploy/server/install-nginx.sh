#!/usr/bin/env bash
# Publishes the kbark console at https://demo.kbark.dev.
# Needs root: it writes nginx configuration and obtains a certificate.
#
#   sudo bash ~/kbark/deploy/install-nginx.sh
#
# Safe to re-run.
set -euo pipefail
[ "$(id -u)" = 0 ] || { echo "run with sudo" >&2; exit 1; }

DOMAIN=demo.kbark.dev
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
SITE=/etc/nginx/sites-available/$DOMAIN

# Two Kind clusters need more inotify instances than the default 128.
cat > /etc/sysctl.d/99-kbark-kind.conf <<'SYSCTL'
fs.inotify.max_user_instances = 512
fs.inotify.max_user_watches = 524288
SYSCTL
sysctl --system >/dev/null

if [ ! -f "$SITE" ] || ! grep -q "ssl_certificate" "$SITE"; then
  install -m 644 "$HERE/nginx.conf" "$SITE"
fi
ln -sfn "$SITE" /etc/nginx/sites-enabled/$DOMAIN
nginx -t
systemctl reload nginx

# certbot edits the site file in place: adds the 443 server and a
# redirect from HTTP. It reuses the account already on this host.
certbot --nginx -d "$DOMAIN" --non-interactive --agree-tos --redirect --keep-until-expiring \
  || certbot --nginx -d "$DOMAIN" --non-interactive --agree-tos --redirect --keep-until-expiring --register-unsafely-without-email
nginx -t
systemctl reload nginx

echo
echo "Published: https://$DOMAIN"
curl -fsS -o /dev/null -w "console answered HTTP %{http_code}\n" "https://$DOMAIN/api/me" || echo "The console did not answer yet; check 'docker logs kbark'."
