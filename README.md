# igrec

The anti-X: federated micro-publishing, one word per post.

## License

AGPL-3.0. igrec is network software; users should be able to inspect, modify, and run the source of the service they use.

## Stack choice

Go keeps the service small and self-hostable: one HTTP server, server-rendered pages, no JavaScript required to read, SQLite by default, and a Docker path for deploys. The storage layer is isolated so Postgres can be added with a driver and placeholder migration changes when the instance outgrows SQLite.

## Run

```sh
cp .env.example .env
go run ./cmd/igrec
```

Open `http://localhost:8080`.

## Users

Registration is invite-only. Operators can create invites at `/operator/invites` after logging in with an email listed in `OPERATOR_EMAILS`. The result is a `/join?invite=...` link.

Users sign in by magic link through `/login`; no passwords are stored. `/write` and `/settings` require a session.

Email is an attachable contact method, not the root account. Logged-in users can add or change it from `/settings` by confirming a link. Transactional email uses `Y` as the sender name with sigil addresses intentionally: magic-link login comes from `!@igrec.net`, and daily nudges come from `_@igrec.net`. Daily replies use tagged addresses like `_+username@igrec.net` so a reply still maps to the right account if the sender address is rewritten.

Send daily nudges with:

```sh
go run ./cmd/igrec send-daily-email
```

Production runs this through `igrec-daily-email.timer` at 08:20 Europe/Paris.
The same job now mirrors the daily prompt to stored browser push subscriptions when VAPID keys are configured.
Local follows also trigger a browser push to the followed account when that user has active subscriptions, linking straight to the follower profile.
During beta, the nudge uses the newest word from someone else; once local follow relationships exist, the same job should narrow that source to followed accounts.

## API and export

Public archives are available as JSON at `/api/@username/words`. The endpoint does not require authentication and returns the user's words, canonical URLs, image URLs when present, timestamps, and timestamp display preference.

Authenticated clients can create words with scoped API tokens from `/settings`:

```sh
curl -X POST -H "Authorization: Bearer $IGREC_API_TOKEN" -d "word=moment" https://igrec.net/api/words
curl -X POST -H "Authorization: Bearer $IGREC_API_TOKEN" -F "word=moment" -F "image_file=@photo.png" https://igrec.net/api/words
```

`POST /api/words` accepts JSON, URL-encoded forms, and multipart forms. Multipart uploads use the same JPEG/PNG sanitizer and 8MB limit as `/write`; optional `focus_x` and `focus_y` values should be between `0` and `1`.

Logged-in users can download a one-click JSON export from `/settings/export`. It includes profile metadata, words, and an ActivityPub-flavored actor/outbox snapshot.

## Wallet cards

Logged-in users see a Wallet section in `/settings`. Apple Wallet support generates a signed `.pkpass` snapshot with the user's latest word, recent friends' words, and a QR link back to `/write?source=wallet`.

Apple Wallet is disabled until pass-signing credentials are configured:

```sh
APPLE_PASS_TYPE_ID=
APPLE_TEAM_ID=
APPLE_PASS_CERT_PATH=
APPLE_PASS_KEY_PATH=
APPLE_PASS_KEY_PASSWORD= # optional
APPLE_WWDR_CERT_PATH=
```

The server uses `openssl smime` to sign `manifest.json`, so `openssl` must be available on the host. Android Wallet needs Google Wallet issuer setup; until then, the installable PWA is the Android pocket card.

## Implemented foundation

- Health check at `/healthz`
- Public archive API at `/api/@username/words`
- Public firehose at `/`
- User archive at `/@username`
- Write form at `/write`
- Settings shell at `/settings`
- Invite-only user registration at `/join`
- Magic-link login at `/login`
- Passkey registration from `/settings`
- Passkey login from `/login`
- One-click JSON export from `/settings/export`
- Server-side one-word validation with Unicode support
- Daily email opt-in in settings
- Daily email nudge command and production timer
- SQLite schema for users, invites, sessions, login tokens, posts, follows
- ActivityPub actor, WebFinger, and outbox JSON
- Resend plain-text email helper
- Cloudflare Email Worker skeleton for inbound posting
- Minimal Docker setup

## Still needed before production

- IndieAuth and Mastodon OAuth
- Configure operator accounts with `OPERATOR_EMAILS`
- Signed ActivityPub delivery to follower inboxes
- Web Push delivery for daily prompt and local follow events
- Settings migration and delete flows

## Image uploads

`/write`, inbound email, and authenticated `POST /api/words` support JPEG/PNG uploads with a conservative policy:

- Max upload size: 8MB
- Accepted types: `image/jpeg`, `image/png`
- Stored as optimized JPEG under `UPLOAD_DIR` and served at `/uploads/...`
- Optional "focus on object" square crop for Instagram-like framing

## Cloudflare setup

Create a scoped Cloudflare API token for `igrec.net` only:

- Zone: DNS: Edit
- Zone: Zone: Read
- Account: Email Routing Addresses: Edit
- Account: Email Routing Rules: Edit

Then configure the DNS records after Resend gives you DKIM values:

```sh
export CLOUDFLARE_API_TOKEN=...
export CLOUDFLARE_ZONE_ID=...
export IGREC_SERVER_IP=...
export RESEND_DKIM_NAME=...
export RESEND_DKIM_VALUE=...
./deploy/cloudflare-records.sh
```

Current Cloudflare status:

- `igrec.net` is an active Cloudflare zone: `c38b6182b674cdc3e15264c04927f81a`.
- `igrec.net` and `www.igrec.net` point to the Oracle host through proxied Cloudflare DNS.
- Email Routing is enabled and ready.
- Catch-all inbound mail routes to the `igrec-inbound-email` Worker.
- Worker secrets are configured for the Oracle callback.
- BIMI discovery points mail clients at `/static/bimi-20260521.svg` for the Y sender mark, and outgoing mail includes a `BIMI-Selector` header.

## Oracle deployment

The Oracle host is `79.72.31.189` (`codex-standby-vnic`). It runs igrec from `/opt/igrec` as `igrec.service`, bound to `127.0.0.1:8097` behind nginx. The matching service and nginx files live in `deploy/systemd/igrec.service` and `deploy/nginx/igrec.net`.

Deployment and verification checklist: `deploy/ORACLE_DEPLOY_CHECKLIST.md`.

## CI/CD

GitHub Actions runs `gofmt`, uncached unit/integration tests with
`go test -count=1 ./...`, and `go build ./cmd/igrec` on every pull
request and push.

Deployments are manual during beta. Run the Deploy workflow from GitHub Actions when you want to update production. Required GitHub repository secrets:

- `ORACLE_HOST`: `79.72.31.189`
- `ORACLE_USER`: `ubuntu`
- `ORACLE_SSH_KEY`: private SSH key with access to the Oracle host
- `CLOUDFLARE_ORIGIN_CERT`: Cloudflare Origin CA certificate PEM for `igrec.net`
- `CLOUDFLARE_ORIGIN_KEY`: matching private key PEM

The deploy workflow uploads the source, builds and tests on Oracle,
installs the Cloudflare origin certificate for nginx HTTPS,
keeps the previous binary as `/opt/igrec/bin/igrec.previous`, restarts
`igrec.service`, smoke-tests nginx locally over HTTP and HTTPS with
`Host: igrec.net`, and
restores the previous binary automatically if restart or smoke checks fail.

For stricter control, configure the `production` GitHub Environment to require your approval before jobs can access deployment secrets.
