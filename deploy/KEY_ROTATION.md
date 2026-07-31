# Key Rotation Runbook

Use this when any production credential has appeared in chat, logs, a local
handover bundle, or another place outside its intended secret store. Do not
commit replacement values to this repository.

## Scope

Rotate every credential that can authorize production access:

- GitHub Actions production secrets:
  - `ORACLE_HOST`
  - `ORACLE_USER`
  - `ORACLE_SSH_KEY`
  - `CLOUDFLARE_ORIGIN_CERT`
  - `CLOUDFLARE_ORIGIN_KEY`
- Oracle host material:
  - SSH deploy key accepted for the deploy user.
  - `/opt/igrec/.env` values, especially `APP_SECRET`, `RESEND_API_KEY`,
    `VAPID_PUBLIC_KEY`, `VAPID_PRIVATE_KEY`, and any Apple Wallet signing
    certificate paths or passwords.
  - `/etc/ssl/cloudflare/igrec.net.pem`
  - `/etc/ssl/cloudflare/igrec.net.key`
- Cloudflare credentials:
  - Scoped API tokens used by local DNS scripts.
  - Origin CA certificate and key for `igrec.net` and `*.igrec.net`.
  - Worker secrets used by inbound email forwarding.
- Resend credentials:
  - Production API key.
- Any local handover or backup bundle containing old secrets.

## Order

1. Generate replacements in the owning provider first. Keep old credentials
   active until the new values have been deployed and verified.
2. Update GitHub production environment secrets for `ORACLE_SSH_KEY`,
   `CLOUDFLARE_ORIGIN_CERT`, and `CLOUDFLARE_ORIGIN_KEY`.
3. Install the new SSH public key on Oracle for the deploy user, then remove
   the old deploy key from `authorized_keys`.
4. Update `/opt/igrec/.env` on Oracle with the new application secrets:
   `APP_SECRET`, `RESEND_API_KEY`, VAPID keys, and Apple Wallet values if used.
5. Update Cloudflare Worker secrets for inbound email forwarding so they match
   the new `APP_SECRET`.
6. Run the GitHub Deploy workflow. It installs the new Cloudflare origin
   certificate and key before restarting the service.
7. Revoke the old provider credentials only after verification passes.
8. Delete local handover bundles or exported secret files that contained the
   old values.

## Verification

Run the deployment checklist health checks after rotation:

```sh
curl -fsS https://igrec.net/healthz \
  && curl -fsSI https://igrec.net/ | head -n 1 \
  && curl -fsSI https://igrec.net/login | head -n 1 \
  && curl -fsSI https://igrec.net/api/@nobody/words | head -n 1
```

Then verify the paths that depend on rotated secrets:

- GitHub Deploy workflow completes from a clean commit.
- `systemctl status igrec --no-pager` is active on Oracle.
- `sudo nginx -t` succeeds on Oracle.
- `sudo test -s /etc/ssl/cloudflare/igrec.net.pem` succeeds on Oracle.
- `sudo test -s /etc/ssl/cloudflare/igrec.net.key` succeeds on Oracle.
- Magic-link email sends through Resend.
- Daily nudge email sends from `_@igrec.net`.
- A reply routed through the Cloudflare Worker reaches `/inbound/email`.
- Existing sessions are expected to become invalid if `APP_SECRET` changes;
  login again with a fresh magic link or passkey.

## Completion

Mark the Phase 7 roadmap item complete only after all old credentials in scope
have been revoked or deleted and the verification checks above pass.
