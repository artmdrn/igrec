package app

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"html/template"
	"image"
	"image/jpeg"
	"io"
	"math"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"igrec.net/igrec/internal/store"
)

const maxUploadBytes = 8 << 20

func (a *App) saveUploadedImage(file io.Reader, header *multipart.FileHeader) (string, error) {
	if header.Size > maxUploadBytes {
		return "", errors.New("image must be 8MB or smaller")
	}
	raw, err := io.ReadAll(io.LimitReader(file, maxUploadBytes+1))
	if err != nil {
		return "", errors.New("could not read image")
	}
	if int64(len(raw)) > maxUploadBytes {
		return "", errors.New("image must be 8MB or smaller")
	}
	return a.storeImageBytes(raw)
}

func (a *App) storeImageBytes(raw []byte) (string, error) {
	contentType := http.DetectContentType(raw)
	if contentType != "image/jpeg" && contentType != "image/png" {
		return "", errors.New("only JPEG and PNG are supported")
	}
	img, _, err := image.Decode(bytes.NewReader(raw))
	if err != nil {
		return "", errors.New("invalid image file")
	}
	if contentType == "image/jpeg" {
		img = applyJPEGOrientation(img, raw)
	}
	bounds := img.Bounds()
	if bounds.Dx() < 24 || bounds.Dy() < 24 {
		return "", errors.New("image is too small")
	}
	img = downscaleWithin(img, 1440)
	token, _, err := newToken()
	if err != nil {
		return "", errors.New("could not store image")
	}
	if err := os.MkdirAll(a.cfg.UploadDir, 0o755); err != nil {
		return "", errors.New("could not store image")
	}
	filename := token[:20] + ".jpg"
	target := filepath.Join(a.cfg.UploadDir, filename)
	out, err := os.Create(target)
	if err != nil {
		return "", errors.New("could not store image")
	}
	defer out.Close()
	if err := jpeg.Encode(out, img, &jpeg.Options{Quality: 86}); err != nil {
		return "", errors.New("could not store image")
	}
	url := "/uploads/" + filename
	return url, nil
}

func cropSquareAroundFocus(img image.Image, focusX, focusY float64) image.Image {
	b := img.Bounds()
	w := b.Dx()
	h := b.Dy()
	size := w
	if h < size {
		size = h
	}
	cx := int(focusX * float64(w))
	cy := int(focusY * float64(h))
	left := cx - size/2
	top := cy - size/2
	if left < 0 {
		left = 0
	}
	if top < 0 {
		top = 0
	}
	if left+size > w {
		left = w - size
	}
	if top+size > h {
		top = h - size
	}
	rect := image.Rect(b.Min.X+left, b.Min.Y+top, b.Min.X+left+size, b.Min.Y+top+size)
	dst := image.NewRGBA(image.Rect(0, 0, size, size))
	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			dst.Set(x, y, img.At(rect.Min.X+x, rect.Min.Y+y))
		}
	}
	return dst
}

func downscaleWithin(img image.Image, maxEdge int) image.Image {
	b := img.Bounds()
	w := b.Dx()
	h := b.Dy()
	if w <= maxEdge && h <= maxEdge {
		return img
	}
	scale := float64(maxEdge) / float64(w)
	if h > w {
		scale = float64(maxEdge) / float64(h)
	}
	nw := int(float64(w) * scale)
	nh := int(float64(h) * scale)
	if nw < 1 {
		nw = 1
	}
	if nh < 1 {
		nh = 1
	}
	dst := image.NewRGBA(image.Rect(0, 0, nw, nh))
	for y := 0; y < nh; y++ {
		sy := b.Min.Y + int(float64(y)/scale)
		if sy >= b.Max.Y {
			sy = b.Max.Y - 1
		}
		for x := 0; x < nw; x++ {
			sx := b.Min.X + int(float64(x)/scale)
			if sx >= b.Max.X {
				sx = b.Max.X - 1
			}
			dst.Set(x, y, img.At(sx, sy))
		}
	}
	return dst
}

func applyJPEGOrientation(img image.Image, raw []byte) image.Image {
	orientation := jpegOrientation(raw)
	if orientation <= 1 || orientation > 8 {
		return img
	}
	b := img.Bounds()
	w := b.Dx()
	h := b.Dy()
	dw := w
	dh := h
	if orientation >= 5 {
		dw = h
		dh = w
	}
	dst := image.NewRGBA(image.Rect(0, 0, dw, dh))
	for y := 0; y < dh; y++ {
		for x := 0; x < dw; x++ {
			var sx, sy int
			switch orientation {
			case 2:
				sx, sy = w-1-x, y
			case 3:
				sx, sy = w-1-x, h-1-y
			case 4:
				sx, sy = x, h-1-y
			case 5:
				sx, sy = y, x
			case 6:
				sx, sy = y, h-1-x
			case 7:
				sx, sy = w-1-y, h-1-x
			case 8:
				sx, sy = w-1-y, x
			default:
				sx, sy = x, y
			}
			dst.Set(x, y, img.At(b.Min.X+sx, b.Min.Y+sy))
		}
	}
	return dst
}

func jpegOrientation(raw []byte) int {
	if len(raw) < 4 || raw[0] != 0xff || raw[1] != 0xd8 {
		return 1
	}
	for i := 2; i+4 <= len(raw); {
		if raw[i] != 0xff {
			return 1
		}
		for i < len(raw) && raw[i] == 0xff {
			i++
		}
		if i >= len(raw) {
			return 1
		}
		marker := raw[i]
		i++
		if marker == 0xd9 || marker == 0xda {
			return 1
		}
		if i+2 > len(raw) {
			return 1
		}
		size := int(binary.BigEndian.Uint16(raw[i : i+2]))
		i += 2
		if size < 2 || i+size-2 > len(raw) {
			return 1
		}
		segment := raw[i : i+size-2]
		i += size - 2
		if marker != 0xe1 || len(segment) < 14 || string(segment[:6]) != "Exif\x00\x00" {
			continue
		}
		if value := exifOrientation(segment[6:]); value >= 1 && value <= 8 {
			return value
		}
	}
	return 1
}

func exifOrientation(tiff []byte) int {
	if len(tiff) < 8 {
		return 1
	}
	var order binary.ByteOrder
	switch string(tiff[:2]) {
	case "II":
		order = binary.LittleEndian
	case "MM":
		order = binary.BigEndian
	default:
		return 1
	}
	if order.Uint16(tiff[2:4]) != 42 {
		return 1
	}
	offset := int(order.Uint32(tiff[4:8]))
	if offset < 8 || offset+2 > len(tiff) {
		return 1
	}
	count := int(order.Uint16(tiff[offset : offset+2]))
	entries := offset + 2
	for n := 0; n < count; n++ {
		entry := entries + n*12
		if entry+12 > len(tiff) {
			return 1
		}
		tag := order.Uint16(tiff[entry : entry+2])
		if tag != 0x0112 {
			continue
		}
		typ := order.Uint16(tiff[entry+2 : entry+4])
		if typ == 3 {
			return int(order.Uint16(tiff[entry+8 : entry+10]))
		}
		if typ == 4 {
			return int(order.Uint32(tiff[entry+8 : entry+12]))
		}
		return 1
	}
	return 1
}

func imageFocusCSS(post store.Post) template.CSS {
	x := strconv.FormatFloat(unitPercent(post.FocusX), 'f', 1, 64)
	y := strconv.FormatFloat(unitPercent(post.FocusY), 'f', 1, 64)
	return template.CSS("object-position: " + x + "% " + y + "%;")
}

func unitPercent(value float64) float64 {
	if math.IsNaN(value) || math.IsInf(value, 0) {
		return 50
	}
	if value < 0 {
		value = 0
	}
	if value > 1 {
		value = 1
	}
	return value * 100
}

func (a *App) captionStyleForPost(post store.Post) template.CSS {
	accent := "#ffd460"
	text := "#ffffff"
	if value, ok := a.imagePalette(post.ImageURL.String); ok {
		accent = value.accent
		text = value.text
	}
	return template.CSS("color: " + text + "; text-shadow: 0 1px 1px #000, 0 0 24px " + accent + ";")
}

type palette struct {
	accent string
	text   string
}

func (a *App) imagePalette(imageURL string) (palette, bool) {
	if !strings.HasPrefix(imageURL, "/uploads/") {
		return palette{}, false
	}
	filename := filepath.Base(imageURL)
	if filename == "." || filename == "/" || filename == "" {
		return palette{}, false
	}
	path := filepath.Join(a.cfg.UploadDir, filename)
	f, err := os.Open(path)
	if err != nil {
		return palette{}, false
	}
	defer f.Close()
	img, _, err := image.Decode(f)
	if err != nil {
		return palette{}, false
	}
	accent, luminance := sampledAccent(img)
	text := "#ffffff"
	if luminance > 0.62 {
		text = "#080a0f"
	}
	return palette{accent: accent, text: text}, true
}

func (a *App) imageDimensions(imageURL string) (int, int, bool) {
	if !strings.HasPrefix(imageURL, "/uploads/") {
		return 0, 0, false
	}
	filename := filepath.Base(imageURL)
	if filename == "." || filename == "/" || filename == "" {
		return 0, 0, false
	}
	path := filepath.Join(a.cfg.UploadDir, filename)
	f, err := os.Open(path)
	if err != nil {
		return 0, 0, false
	}
	defer f.Close()
	cfg, _, err := image.DecodeConfig(f)
	if err != nil {
		return 0, 0, false
	}
	return cfg.Width, cfg.Height, cfg.Width > 0 && cfg.Height > 0
}

func sampledAccent(img image.Image) (string, float64) {
	b := img.Bounds()
	stepX := maxInt(1, b.Dx()/24)
	stepY := maxInt(1, b.Dy()/24)
	var rSum, gSum, bSum float64
	var samples float64
	for y := b.Min.Y; y < b.Max.Y; y += stepY {
		for x := b.Min.X; x < b.Max.X; x += stepX {
			cr, cg, cb, _ := img.At(x, y).RGBA()
			r := float64(cr>>8) / 255.0
			g := float64(cg>>8) / 255.0
			bl := float64(cb>>8) / 255.0
			// Prefer pixels with meaningful saturation for a stronger accent.
			_, sat, _ := rgbToHSV(r, g, bl)
			weight := 0.35 + sat*0.65
			rSum += r * weight
			gSum += g * weight
			bSum += bl * weight
			samples += weight
		}
	}
	if samples == 0 {
		return "#ffd460", 0
	}
	r := clamp01(rSum / samples)
	g := clamp01(gSum / samples)
	bl := clamp01(bSum / samples)
	_, sat, val := rgbToHSV(r, g, bl)
	// Nudge toward a stylish accent: keep hue, boost sat/value slightly.
	sat = clamp01(sat*1.18 + 0.08)
	val = clamp01(val*1.08 + 0.06)
	ar, ag, ab := hsvToRGB(hueOf(r, g, bl), sat, val)
	luminance := relativeLuminance(ar, ag, ab)
	return rgbHex(ar, ag, ab), luminance
}

func rgbToHSV(r, g, b float64) (float64, float64, float64) {
	max := math.Max(r, math.Max(g, b))
	min := math.Min(r, math.Min(g, b))
	delta := max - min
	h := 0.0
	switch {
	case delta == 0:
		h = 0
	case max == r:
		h = math.Mod((g-b)/delta, 6.0)
	case max == g:
		h = (b-r)/delta + 2.0
	default:
		h = (r-g)/delta + 4.0
	}
	h *= 60
	if h < 0 {
		h += 360
	}
	s := 0.0
	if max > 0 {
		s = delta / max
	}
	return h, s, max
}

func hsvToRGB(h, s, v float64) (float64, float64, float64) {
	c := v * s
	x := c * (1 - math.Abs(math.Mod(h/60.0, 2)-1))
	m := v - c
	r, g, b := 0.0, 0.0, 0.0
	switch {
	case h < 60:
		r, g, b = c, x, 0
	case h < 120:
		r, g, b = x, c, 0
	case h < 180:
		r, g, b = 0, c, x
	case h < 240:
		r, g, b = 0, x, c
	case h < 300:
		r, g, b = x, 0, c
	default:
		r, g, b = c, 0, x
	}
	return r + m, g + m, b + m
}

func hueOf(r, g, b float64) float64 {
	h, _, _ := rgbToHSV(r, g, b)
	return h
}

func rgbHex(r, g, b float64) string {
	cr := uint8(clamp01(r) * 255)
	cg := uint8(clamp01(g) * 255)
	cb := uint8(clamp01(b) * 255)
	return fmt.Sprintf("#%02x%02x%02x", cr, cg, cb)
}

func relativeLuminance(r, g, b float64) float64 {
	conv := func(c float64) float64 {
		if c <= 0.03928 {
			return c / 12.92
		}
		return math.Pow((c+0.055)/1.055, 2.4)
	}
	return 0.2126*conv(r) + 0.7152*conv(g) + 0.0722*conv(b)
}

func clamp01(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}
