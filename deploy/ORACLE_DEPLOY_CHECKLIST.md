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

## 2) One-command post-deploy verification sequence

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

## 3) Operational checks on Oracle host

```sh
ssh ubuntu@79.72.31.189 'systemctl status igrec --no-pager; sudo nginx -t'
```

- `igrec.service` is active.
- `nginx -t` reports config is successful.

## 4) Email path checks

- Magic-link sender uses `LOGIN_EMAIL_FROM` (`!@igrec.net` path).
- Daily nudge sender uses `DAILY_EMAIL_FROM` (`_@igrec.net` path).
- Inbound reply route forwards to the Cloudflare worker and reaches `/inbound/email`.

## 5) Rollback

If deploy smoke checks fail, re-run the previous known-good commit through the Deploy workflow, then re-run section 2.
