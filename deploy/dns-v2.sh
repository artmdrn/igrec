#!/usr/bin/env bash
# Create the proxied A record for v2.igrec.net in Cloudflare.
# Reads CLOUDFLARE_API_TOKEN and CLOUDFLARE_ZONE_ID from the
# environment or from a .env file passed as $1.
set -euo pipefail

if [ "${1:-}" != "" ]; then
  # shellcheck disable=SC1090
  set -a; source "$1"; set +a
fi
: "${CLOUDFLARE_API_TOKEN:?set CLOUDFLARE_API_TOKEN}"
: "${CLOUDFLARE_ZONE_ID:?set CLOUDFLARE_ZONE_ID}"

existing=$(curl -fsS \
  -H "Authorization: Bearer $CLOUDFLARE_API_TOKEN" \
  "https://api.cloudflare.com/client/v4/zones/$CLOUDFLARE_ZONE_ID/dns_records?type=A&name=v2.igrec.net")
if printf '%s' "$existing" | grep -q '"name":"v2.igrec.net"'; then
  echo "v2.igrec.net record already exists; nothing to do."
  exit 0
fi

curl -fsS -X POST \
  -H "Authorization: Bearer $CLOUDFLARE_API_TOKEN" \
  -H "Content-Type: application/json" \
  --data '{"type":"A","name":"v2","content":"79.72.31.189","proxied":true,"ttl":1,"comment":"igrec v2 parallel beta"}' \
  "https://api.cloudflare.com/client/v4/zones/$CLOUDFLARE_ZONE_ID/dns_records" >/dev/null

echo "Created proxied A record v2.igrec.net -> 79.72.31.189"
