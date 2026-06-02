package app

import (
	"os"
	"path/filepath"
	"testing"
)

func TestFormatBytes(t *testing.T) {
	tests := map[int64]string{
		0:               "0 B",
		512:             "512 B",
		1536:            "1.5 KB",
		2 * 1024 * 1024: "2.0 MB",
	}
	for input, want := range tests {
		if got := formatBytes(input); got != want {
			t.Fatalf("formatBytes(%d) = %q, want %q", input, got, want)
		}
	}
}

func TestUploadStorageStats(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.jpg"), []byte("aaa"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "b.jpg"), []byte("bbbbb"), 0o644); err != nil {
		t.Fatal(err)
	}
	a := &App{cfg: Config{UploadDir: dir}}

	stats := a.uploadStorageStats()

	if stats.FileCount != 2 {
		t.Fatalf("expected 2 files, got %d", stats.FileCount)
	}
	if stats.Bytes != 8 || stats.AverageBytes != 4 {
		t.Fatalf("unexpected byte stats: %#v", stats)
	}
	if stats.WatchLevel != "ok" {
		t.Fatalf("expected ok watch level, got %q", stats.WatchLevel)
	}
}
