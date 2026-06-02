# igrec Backups

igrec stores production data in SQLite and uploads under `/opt/igrec/data`.

## Create Backup

The service command creates a consistent SQLite backup with `VACUUM INTO`, gzips it, and prunes old backups:

```sh
sudo systemctl start igrec-backup.service
```

Backups are written to:

```text
/opt/igrec/data/backups/igrec-YYYYMMDDTHHMMSSZ.sqlite.gz
```

The timer runs daily at 03:10 Europe/Paris:

```sh
sudo systemctl enable --now igrec-backup.timer
```

## Restore Drill

```sh
sudo systemctl stop igrec
cd /opt/igrec/data
sudo cp igrec.db igrec.db.before-restore
sudo gzip -dc backups/igrec-YYYYMMDDTHHMMSSZ.sqlite.gz > /tmp/igrec-restore.sqlite
sudo install -o igrec -g igrec -m 0640 /tmp/igrec-restore.sqlite /opt/igrec/data/igrec.db
sudo systemctl start igrec
curl -fsS https://igrec.net/healthz
```

Uploads are not included in SQLite backups. Keep `/opt/igrec/data/uploads` with normal server snapshots until image storage moves to object storage.
