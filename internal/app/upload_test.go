package app

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"mime/multipart"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSaveUploadedImageRejectsUnsupportedType(t *testing.T) {
	a := &App{cfg: Config{UploadDir: t.TempDir()}}
	data := []byte("GIF89a")
	_, err := a.saveUploadedImage(bytes.NewReader(data), &multipart.FileHeader{Size: int64(len(data))}, false, 0.5, 0.5)
	if err == nil || !strings.Contains(err.Error(), "only JPEG and PNG") {
		t.Fatalf("expected JPEG/PNG validation error, got %v", err)
	}
}

func TestSaveUploadedImageStoresJpeg(t *testing.T) {
	a := &App{cfg: Config{UploadDir: t.TempDir()}}
	src := image.NewRGBA(image.Rect(0, 0, 120, 80))
	for y := 0; y < 80; y++ {
		for x := 0; x < 120; x++ {
			src.Set(x, y, color.RGBA{R: uint8(x), G: uint8(y), B: 120, A: 255})
		}
	}
	var raw bytes.Buffer
	if err := png.Encode(&raw, src); err != nil {
		t.Fatal(err)
	}
	url, err := a.saveUploadedImage(bytes.NewReader(raw.Bytes()), &multipart.FileHeader{Size: int64(raw.Len())}, true, 0.8, 0.5)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(url, "/uploads/") || !strings.HasSuffix(url, ".jpg") {
		t.Fatalf("unexpected image URL %q", url)
	}
	path := filepath.Join(a.cfg.UploadDir, strings.TrimPrefix(url, "/uploads/"))
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("expected stored image at %s: %v", path, err)
	}
}
