package app

import (
	"encoding/binary"
	"image"
	"image/color"
	"testing"
)

func TestApplyJPEGOrientationRotatesRight(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 2, 3))
	img.Set(0, 0, color.RGBA{R: 255, A: 255})
	img.Set(1, 0, color.RGBA{G: 255, A: 255})
	img.Set(0, 2, color.RGBA{B: 255, A: 255})

	got := applyJPEGOrientation(img, jpegWithOrientation(6))
	if got.Bounds().Dx() != 3 || got.Bounds().Dy() != 2 {
		t.Fatalf("rotated bounds = %v, want 3x2", got.Bounds())
	}
	if got.At(2, 0) != (color.RGBA{R: 255, A: 255}) {
		t.Fatalf("top-left source pixel did not rotate to top-right")
	}
	if got.At(2, 1) != (color.RGBA{G: 255, A: 255}) {
		t.Fatalf("top-right source pixel did not rotate to bottom-right")
	}
	if got.At(0, 0) != (color.RGBA{B: 255, A: 255}) {
		t.Fatalf("bottom-left source pixel did not rotate to top-left")
	}
}

func jpegWithOrientation(value uint16) []byte {
	tiff := make([]byte, 8+2+12+4)
	copy(tiff[0:2], "II")
	binary.LittleEndian.PutUint16(tiff[2:4], 42)
	binary.LittleEndian.PutUint32(tiff[4:8], 8)
	binary.LittleEndian.PutUint16(tiff[8:10], 1)
	entry := 10
	binary.LittleEndian.PutUint16(tiff[entry:entry+2], 0x0112)
	binary.LittleEndian.PutUint16(tiff[entry+2:entry+4], 3)
	binary.LittleEndian.PutUint32(tiff[entry+4:entry+8], 1)
	binary.LittleEndian.PutUint16(tiff[entry+8:entry+10], value)

	segment := append([]byte("Exif\x00\x00"), tiff...)
	length := len(segment) + 2
	raw := []byte{0xff, 0xd8, 0xff, 0xe1, byte(length >> 8), byte(length)}
	raw = append(raw, segment...)
	raw = append(raw, 0xff, 0xda)
	return raw
}
