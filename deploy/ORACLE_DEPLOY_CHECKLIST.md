# Oracle Deployment Checklist

This checklist is for deploying `igrec` to Oracle with Cloudflare DNS/email routing, Resend outbound mail, and GitHub Actions deploy.

## 1) Required state before deploy

- Oracle host reachable by SSH as `ubuntu` and app directory exists at `/opt/igrec`.
- Systemd unit installed: `deploy/systemd/igrec.service`.
- Nginx site installed: `deploy/nginx/igrec.net`.
- Cloudflare zone active for `igrec.net` with proxied `A` records for `@` and `www`.
- Cloudflare Email Routing enabled and inbound worker route configured.
- Resend domain `igrec.net` verified (SPF/DKIM green in Resend dashboard).
- GitHub repo secrets set:
  - `ORACLE_HOST`
  - `ORACLE_USER`
  - `ORACLE_SSH_KEY`
  - `CLOUDFLARE_ORIGIN_CERT`
  - `CLOUDFLARE_ORIGIN_KEY`

## 2) Cloudflare origin certificate

Create a Cloudflare Origin CA certificate for `igrec.net` and
`*.igrec.net`, then store the PEM certificate and private key as the
GitHub environment secrets `CLOUDFLARE_ORIGIN_CERT` and
`CLOUDFLARE_ORIGIN_KEY`. The Deploy workflow installs them on Oracle at:

- `/etc/ssl/cloudflare/igrec.net.pem`
- `/etc/ssl/cloudflare/igrec.net.key`

After the first successful deploy, set Cloudflare SSL/TLS encryption mode
to `Full (strict)` for the zone.

For exposed or stale credentials, follow the key rotation runbook:
`deploy/KEY_ROTATION.md`.

## 3) One-command post-deploy verification sequence

Run this after the GitHub Deploy workflow finishes:

```sh
curl -fsS https://igrec.net/healthz \
  && curl -fsSI https://igrec.net/ | head -n 1 \
  && curl -fsSI https://igrec.net/login | head -n 1 \
  && curl -fsSI https://igrec.net/api/@nobody/words | head -n 1
```

Expected results:

- `/healthz` returns body `ok`.
- `/` returns `HTTP/2 200`.
- `/login` returns `HTTP/2 200`.
- `/api/@nobody/words` returns `HTTP/2 404` (route reachable, user missing).

## 4) Operational checks on Oracle host

```sh
ssh ubuntu@79.72.31.189 'systemctl status igrec --no-pager; sudo nginx -t; sudo test -s /etc/ssl/cloudflare/igrec.net.pem; sudo test -s /etc/ssl/cloudflare/igrec.net.key'
```

- `igrec.service` is active.
- `nginx -t` reports config is successful.
- Origin certificate and key files exist.

## 5) Email path checks

- Magic-link sender uses `LOGIN_EMAIL_FROM` (`!@igrec.net` path).
- Daily nudge sender uses `DAILY_EMAIL_FROM` (`_@igrec.net` path).
- Inbound reply route forwards to the Cloudflare worker and reaches `/inbound/email`.

## 6) Rollback

If deploy smoke checks fail, the Deploy workflow restores
`/opt/igrec/bin/igrec.previous` and restarts `igrec.service`
automatically. Confirm service health with sections 3 and 4, then fix the
failed commit before running Deploy again.
