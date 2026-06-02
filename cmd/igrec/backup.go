package main

import (
	"compress/gzip"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"igrec.net/igrec/internal/store"
)

func backupSQLite(db *store.DB, databaseURL, backupDir string, keep int) (string, error) {
	if _, err := sqliteDatabasePath(databaseURL); err != nil {
		return "", err
	}
	if backupDir == "" {
		backupDir = "data/backups"
	}
	if keep <= 0 {
		keep = 14
	}
	if err := os.MkdirAll(backupDir, 0o750); err != nil {
		return "", err
	}
	stamp := time.Now().UTC().Format("20060102T150405Z")
	rawPath := filepath.Join(backupDir, "igrec-"+stamp+".sqlite")
	gzPath := rawPath + ".gz"
	if _, err := db.Exec("vacuum into " + sqliteQuote(rawPath)); err != nil {
		return "", err
	}
	if err := gzipFile(rawPath, gzPath); err != nil {
		_ = os.Remove(rawPath)
		return "", err
	}
	_ = os.Remove(rawPath)
	if err := pruneBackups(backupDir, keep); err != nil {
		return "", err
	}
	return gzPath, nil
}

func sqliteDatabasePath(databaseURL string) (string, error) {
	if strings.HasPrefix(databaseURL, "sqlite://") {
		return strings.TrimPrefix(databaseURL, "sqlite://"), nil
	}
	if databaseURL == "" {
		return "igrec.db", nil
	}
	if strings.HasPrefix(databaseURL, "postgres://") || strings.HasPrefix(databaseURL, "postgresql://") {
		return "", fmt.Errorf("backup-sqlite only supports sqlite DATABASE_URL")
	}
	return databaseURL, nil
}

func sqliteQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "''") + "'"
}

func gzipFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o640)
	if err != nil {
		return err
	}
	defer out.Close()
	gz := gzip.NewWriter(out)
	if _, err := io.Copy(gz, in); err != nil {
		_ = gz.Close()
		return err
	}
	return gz.Close()
}

func pruneBackups(backupDir string, keep int) error {
	entries, err := filepath.Glob(filepath.Join(backupDir, "igrec-*.sqlite.gz"))
	if err != nil {
		return err
	}
	sort.Strings(entries)
	for len(entries) > keep {
		if err := os.Remove(entries[0]); err != nil {
			return err
		}
		entries = entries[1:]
	}
	return nil
}
