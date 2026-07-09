package main

import (
	"compress/gzip"
	"io"
	"os"
	"path/filepath"
	"testing"

	"igrec.net/igrec/internal/store"
)

func TestSQLiteDatabasePath(t *testing.T) {
	tests := map[string]string{
		"":                   "igrec.db",
		"igrec.db":           "igrec.db",
		"sqlite://data/x.db": "data/x.db",
	}
	for input, want := range tests {
		got, err := sqliteDatabasePath(input)
		if err != nil {
			t.Fatalf("sqliteDatabasePath(%q): %v", input, err)
		}
		if got != want {
			t.Fatalf("sqliteDatabasePath(%q) = %q, want %q", input, got, want)
		}
	}
	if _, err := sqliteDatabasePath("postgres://example"); err == nil {
		t.Fatal("expected postgres backup rejection")
	}
}

func TestPruneBackups(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{
		"igrec-20260101T000000Z.sqlite.gz",
		"igrec-20260102T000000Z.sqlite.gz",
		"igrec-20260103T000000Z.sqlite.gz",
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("x"), 0o640); err != nil {
			t.Fatal(err)
		}
	}
	if err := pruneBackups(dir, 2); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "igrec-20260101T000000Z.sqlite.gz")); !os.IsNotExist(err) {
		t.Fatal("expected oldest backup to be pruned")
	}
}

func TestBackupSQLiteCreatesRestorableGzipAndPrunesOldBackups(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "igrec.db")
	backupDir := filepath.Join(dir, "backups")

	db, err := store.Open("sqlite://" + dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Migrate(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })

	if _, err := db.CreateUser("backupuser", "backup@example.com"); err != nil {
		t.Fatal(err)
	}
	if err := writeGzip(filepath.Join(backupDir, "igrec-20260101T000000Z.sqlite.gz"), []byte("old")); err != nil {
		t.Fatal(err)
	}
	if err := writeGzip(filepath.Join(backupDir, "igrec-20260102T000000Z.sqlite.gz"), []byte("older")); err != nil {
		t.Fatal(err)
	}

	backupPath, err := backupSQLite(db, "sqlite://"+dbPath, backupDir, 2)
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Ext(backupPath) != ".gz" {
		t.Fatalf("expected gzip backup path, got %q", backupPath)
	}
	if _, err := os.Stat(filepath.Join(backupDir, "igrec-20260101T000000Z.sqlite.gz")); !os.IsNotExist(err) {
		t.Fatal("expected oldest backup to be pruned")
	}

	restorePath := filepath.Join(dir, "restore.db")
	if err := gunzipFile(backupPath, restorePath); err != nil {
		t.Fatal(err)
	}
	restored, err := store.Open("sqlite://" + restorePath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = restored.Close() })

	user, err := restored.UserByUsername("backupuser")
	if err != nil {
		t.Fatal(err)
	}
	if user.Email != "backup@example.com" {
		t.Fatalf("restored user email = %q, want backup@example.com", user.Email)
	}
}

func writeGzip(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return err
	}
	out, err := os.OpenFile(path, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o640)
	if err != nil {
		return err
	}
	defer out.Close()
	gz := gzip.NewWriter(out)
	if _, err := gz.Write(data); err != nil {
		_ = gz.Close()
		return err
	}
	return gz.Close()
}

func gunzipFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	gz, err := gzip.NewReader(in)
	if err != nil {
		return err
	}
	defer gz.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o640)
	if err != nil {
		return err
	}
	defer out.Close()
	_, err = io.Copy(out, gz)
	return err
}
