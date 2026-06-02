package main

import (
	"os"
	"path/filepath"
	"testing"
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
