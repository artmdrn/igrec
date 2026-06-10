#!/usr/bin/env bash
# Deploy the current checkout as the parallel v2 beta on Oracle.
# Safe to re-run: the beta env and database snapshot are only created
# if missing, and production igrec.service is never touched (except a
# ~1s stop/start only if the sqlite3 CLI is unavailable for the first
# snapshot).
#
# Usage: ./deploy/deploy-v2.sh [user@host]
set -euo pipefail

HOST="${1:-ubuntu@79.72.31.189}"
cd "$(dirname "$0")/.."

tar --exclude='.git' --exclude='data' --exclude='*.db' \
    --exclude='handover' --exclude='.gocache' \
    -czf /tmp/igrec-v2-deploy.tgz .
scp /tmp/igrec-v2-deploy.tgz "$HOST:/tmp/igrec-v2-deploy.tgz"

ssh "$HOST" <<'REMOTE'
set -euo pipefail

sudo mkdir -p /opt/igrec-v2/src /opt/igrec-v2/bin /opt/igrec-v2/data/uploads
sudo find /opt/igrec-v2/src -mindepth 1 -maxdepth 1 -exec rm -rf {} +
sudo tar -xzf /tmp/igrec-v2-deploy.tgz -C /opt/igrec-v2/src
sudo chown -R "$USER:$USER" /opt/igrec-v2/src
cd /opt/igrec-v2/src
go test ./...
CGO_ENABLED=1 go build -o /tmp/igrec-v2-build ./cmd/igrec
sudo mv /tmp/igrec-v2-build /opt/igrec-v2/bin/igrec
sudo chmod 755 /opt/igrec-v2/bin/igrec

# Beta env: production env with beta address, base URL, and database.
if ! sudo test -f /opt/igrec-v2/.env; then
  sudo cp /opt/igrec/.env /opt/igrec-v2/.env
  sudo sed -i 's#^BASE_URL=.*#BASE_URL=https://v2.igrec.net#' /opt/igrec-v2/.env
  sudo sed -i 's#^ADDR=.*#ADDR=:8098#' /opt/igrec-v2/.env
  sudo sed -i 's#^DATABASE_URL=.*#DATABASE_URL=sqlite:///opt/igrec-v2/data/igrec.db#' /opt/igrec-v2/.env
fi

# First run: snapshot production data so the beta looks real.
if ! sudo test -f /opt/igrec-v2/data/igrec.db; then
  if command -v sqlite3 >/dev/null; then
    sudo sqlite3 /opt/igrec/data/igrec.db ".backup /opt/igrec-v2/data/igrec.db"
  else
    sudo systemctl stop igrec
    sudo cp /opt/igrec/data/igrec.db /opt/igrec-v2/data/igrec.db
    sudo systemctl start igrec
  fi
  sudo rsync -a /opt/igrec/data/uploads/ /opt/igrec-v2/data/uploads/ 2>/dev/null \
    || sudo cp -a /opt/igrec/data/uploads/. /opt/igrec-v2/data/uploads/
fi
sudo chown -R igrec:igrec /opt/igrec-v2/bin /opt/igrec-v2/data /opt/igrec-v2/.env

sudo cp deploy/systemd/igrec-v2.service /etc/systemd/system/igrec-v2.service
sudo systemctl daemon-reload
sudo systemctl enable igrec-v2
sudo systemctl restart igrec-v2

sudo cp deploy/nginx/v2.igrec.net /etc/nginx/sites-available/v2.igrec.net
sudo ln -sf /etc/nginx/sites-available/v2.igrec.net /etc/nginx/sites-enabled/v2.igrec.net
sudo nginx -t
sudo systemctl reload nginx

sleep 1
echo "--- beta health:" && curl -fsS http://127.0.0.1:8098/healthz
echo "--- production untouched:" && systemctl is-active igrec && curl -fsS http://127.0.0.1:8097/healthz
REMOTE

echo
echo "Done. If the v2 DNS record does not exist yet, run ./deploy/dns-v2.sh"
echo "then verify: curl -fsS https://v2.igrec.net/healthz"
