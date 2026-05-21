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

## Implemented foundation

- Public firehose at `/`
- User archive at `/@username`
- Write form at `/write`
- Settings shell at `/settings`
- Server-side one-word validation with Unicode support
- SQLite schema for users, invites, posts, follows
- ActivityPub actor, WebFinger, and outbox JSON
- Resend plain-text email helper
- Cloudflare Email Worker skeleton for inbound posting
- Minimal Docker setup

## Still needed before production

- IndieAuth, Mastodon OAuth, and magic-link sessions
- Invite redemption UI and admin invite generation
- Real image upload storage
- Signed ActivityPub delivery to follower inboxes
- VAPID subscription storage and push delivery
- Settings mutations, export, migration, and delete flows
- Cloudflare/Resend credentials, then DNS and Email Routing activation

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

Email Routing still needs to be enabled for the zone in Cloudflare before inbound routes can run. Attach `cloudflare/email-worker.js` to the inbound route and set Worker secrets `IGREC_INBOUND_URL=https://igrec.net/inbound/email` and `APP_SECRET`.

Current Cloudflare status:

- `igrec-inbound-email` Worker is staged in the account.
- Worker secrets are configured for the Oracle callback.
- `igrec.net` must still be added as a Cloudflare zone before DNS and Email Routing rules can be created.

## Oracle deployment

The Oracle host is `79.72.31.189` (`codex-standby-vnic`). It runs igrec from `/opt/igrec` as `igrec.service`, bound to `127.0.0.1:8097` behind nginx. The matching service and nginx files live in `deploy/systemd/igrec.service` and `deploy/nginx/igrec.net`.
