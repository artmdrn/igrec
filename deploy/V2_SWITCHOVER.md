# igrec v2: parallel beta and complete switchover

v2 lives on the `v2` branch. It is the same data model as v1 (all
migrations are additive `create table if not exists` / `ensureColumn`),
so switchover is a binary swap, not a data migration.

## Topology while both run

| | production v1 | beta v2 |
|---|---|---|
| URL | https://igrec.net | https://v2.igrec.net |
| service | `igrec.service` | `igrec-v2.service` |
| bind | 127.0.0.1:8097 | 127.0.0.1:8098 |
| app dir | `/opt/igrec` | `/opt/igrec-v2` |
| database | `/opt/igrec/data/igrec.db` | `/opt/igrec-v2/data/igrec.db` (snapshot) |
| daily email timer | `igrec-daily-email.timer` (active) | none on purpose |

The v2 beta runs against a **snapshot** of the production database so it
looks real. Anything posted on v2 stays on the snapshot and is discarded
at switchover. Two cautions while the beta is up:

- Do not enable a daily-email timer for v2; users would get two nudges.
- Posting on v2 delivers ActivityPub Creates to real follower inboxes
  with `v2.igrec.net` object URLs. Fine for operator testing, but don't
  hand the beta URL to every user with remote followers.
- Sessions and passkeys are origin-bound: log into v2 separately with a
  magic link; igrec.net passkeys will not work on v2.igrec.net.

## Refreshing the beta snapshot

```sh
ssh ubuntu@79.72.31.189
sudo systemctl stop igrec-v2
sudo -u igrec sqlite3 /opt/igrec/data/igrec.db ".backup /opt/igrec-v2/data/igrec.db"
sudo rsync -a --delete /opt/igrec/data/uploads/ /opt/igrec-v2/data/uploads/
sudo systemctl start igrec-v2
```

## Complete switchover (v2 becomes igrec.net)

Total downtime is a few seconds: one binary swap and a restart.

1. Merge/fast-forward `main` to the `v2` branch state and push, so the
   normal Deploy workflow and the weekday automation build v2 from now
   on:

   ```sh
   git checkout main && git merge --ff-only v2 && git push
   ```

2. Back up production data:

   ```sh
   ssh ubuntu@79.72.31.189 'sudo -u igrec sqlite3 /opt/igrec/data/igrec.db ".backup /opt/igrec/data/backups/pre-v2-$(date +%Y%m%d-%H%M).db"'
   ```

3. Deploy to production exactly as usual: run the GitHub **Deploy**
   workflow (it tars source, builds on Oracle, replaces
   `/opt/igrec/bin/igrec`, restarts `igrec.service`). The v2 binary
   runs the additive migrations on first boot. Keep `/opt/igrec/.env`
   as is — `BASE_URL=https://igrec.net` stays.

4. Verify:

   ```sh
   curl -fsS https://igrec.net/healthz \
     && curl -fsSI https://igrec.net/ | head -n 1 \
     && curl -fsSI https://igrec.net/about | head -n 1 \
     && curl -fsS https://igrec.net/service-worker.js | head -n 3
   ```

   Expect `ok`, `200`, `200`, and a `igrec-shell-<hash>` cache name.

5. Retire the beta:

   ```sh
   ssh ubuntu@79.72.31.189 'sudo systemctl disable --now igrec-v2 && sudo rm /etc/nginx/sites-enabled/v2.igrec.net && sudo nginx -t && sudo systemctl reload nginx'
   ```

   Optionally delete the `v2` DNS record in Cloudflare and
   `/opt/igrec-v2` once you're confident.

## Rollback

The previous binary still exists until the next deploy overwrites it.
Re-run the Deploy workflow from the last v1 commit, or on the host:

```sh
ssh ubuntu@79.72.31.189
cd /opt/igrec/src && sudo git -C . log --oneline -3   # if needed
# restore DB only if something wrote bad data:
sudo systemctl stop igrec
sudo -u igrec cp /opt/igrec/data/backups/pre-v2-<stamp>.db /opt/igrec/data/igrec.db
sudo systemctl start igrec
```

v2's schema additions are additive, so running the old binary against a
v2-migrated database is safe.
