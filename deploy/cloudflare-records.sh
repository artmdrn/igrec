#!/usr/bin/env bash
set -euo pipefail

: "${CLOUDFLARE_API_TOKEN:?missing CLOUDFLARE_API_TOKEN}"
: "${CLOUDFLARE_ZONE_ID:?missing CLOUDFLARE_ZONE_ID}"
: "${IGREC_SERVER_IP:?missing IGREC_SERVER_IP}"
: "${RESEND_DKIM_NAME:?missing RESEND_DKIM_NAME from Resend domain setup}"
: "${RESEND_DKIM_VALUE:?missing RESEND_DKIM_VALUE from Resend domain setup}"

api() {
  curl -fsS "https://api.cloudflare.com/client/v4/$1" \
    -H "authorization: Bearer $CLOUDFLARE_API_TOKEN" \
    -H "content-type: application/json" \
    "${@:2}"
}

create_dns() {
  local type=$1 name=$2 content=$3 priority=${4:-}
  local data
  if [[ -n "$priority" ]]; then
    data=$(printf '{"type":"%s","name":"%s","content":"%s","ttl":1,"proxied":false,"priority":%s}' "$type" "$name" "$content" "$priority")
  else
    data=$(printf '{"type":"%s","name":"%s","content":"%s","ttl":1,"proxied":false}' "$type" "$name" "$content")
  fi
  api "zones/$CLOUDFLARE_ZONE_ID/dns_records" -X POST --data "$data"
}

create_dns A igrec.net "$IGREC_SERVER_IP"
create_dns CNAME www igrec.net
create_dns TXT igrec.net "v=spf1 include:amazonses.com ~all"
create_dns TXT _dmarc "v=DMARC1; p=quarantine; rua=mailto:postmaster@igrec.net"
create_dns CNAME "$RESEND_DKIM_NAME" "$RESEND_DKIM_VALUE"

echo "DNS records requested. Enable Email Routing in the Cloudflare dashboard, then attach cloudflare/email-worker.js to the inbound route."
