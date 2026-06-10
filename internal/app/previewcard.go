package app

import (
	"crypto/sha256"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"

	"github.com/golang/freetype"
	"github.com/golang/freetype/truetype"
	xfont "golang.org/x/image/font"
	"golang.org/x/image/font/gofont/gomono"
	"igrec.net/igrec/internal/store"
)

func (a *App) postPreviewCard(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	username, value, ok := previewCardPathParts(r.URL.Path)
	if !ok {
		http.NotFound(w, r)
		return
	}
	post, err := postByPathSegment(a.db, username, value)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	img, err := renderPreviewCard(post)
	if err != nil {
		http.Error(w, "preview unavailable", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("Cache-Control", "public, max-age=14400")
	if r.Method == http.MethodHead {
		return
	}
	_ = png.Encode(w, img)
}

func previewCardPathParts(path string) (string, string, bool) {
	rest := strings.TrimPrefix(path, "/og/@")
	if rest == path || rest == "" {
		return "", "", false
	}
	usernamePart, wordPart, ok := strings.Cut(rest, "/")
	if !ok || usernamePart == "" || !strings.HasSuffix(wordPart, ".png") {
		return "", "", false
	}
	wordPart = strings.TrimSuffix(wordPart, ".png")
	username, err := url.PathUnescape(usernamePart)
	if err != nil {
		return "", "", false
	}
	value, err := url.PathUnescape(wordPart)
	if err != nil {
		return "", "", false
	}
	if username == "" || value == "" {
		return "", "", false
	}
	return username, value, true
}

func canonicalPostSlug(post store.Post) string {
	return strconv.FormatInt(post.ID, 10) + "-" + post.Word
}

func postByPathSegment(db *store.DB, username, segment string) (store.Post, error) {
	if id, value, ok := canonicalPostSegment(segment); ok {
		post, err := db.PostByID(id)
		if err != nil {
			return store.Post{}, err
		}
		if post.Username != username || post.Word != value {
			return store.Post{}, os.ErrNotExist
		}
		return post, nil
	}
	return db.PostByUserWord(username, segment)
}

func canonicalPostSegment(segment string) (int64, string, bool) {
	hyphen := strings.IndexByte(segment, '-')
	if hyphen <= 0 || hyphen == len(segment)-1 {
		return 0, "", false
	}
	id, err := strconv.ParseInt(segment[:hyphen], 10, 64)
	if err != nil || id < 1 {
		return 0, "", false
	}
	return id, segment[hyphen+1:], true
}

func renderPreviewCard(post store.Post) (image.Image, error) {
	const width = 1200
	const height = 630
	colors := previewCardColors(post.Word)
	ttf, err := truetype.Parse(gomono.TTF)
	if err != nil {
		return nil, err
	}
	img := image.NewRGBA(image.Rect(0, 0, width, height))
	fillRect(img, img.Bounds(), colors.paper)
	for y := 0; y < height; y += 8 {
		fillRect(img, image.Rect(0, y, width, y+1), colors.line)
	}
	fillRect(img, image.Rect(38, 38, width-38, height-38), colors.panel)
	fillRect(img, image.Rect(38, 38, width-38, 44), color.RGBA{5, 7, 12, 255})
	fillRect(img, image.Rect(38, height-44, width-38, height-38), color.RGBA{5, 7, 12, 255})
	fillRect(img, image.Rect(38, 38, 44, height-38), color.RGBA{5, 7, 12, 255})
	fillRect(img, image.Rect(width-44, 38, width-38, height-38), color.RGBA{5, 7, 12, 255})
	fillRect(img, image.Rect(58, 58, width-58, height-58), color.RGBA{255, 255, 255, 255})

	drawText(img, ttf, "Y", 34, 88, 102, color.RGBA{5, 7, 12, 255})
	fillRect(img, image.Rect(74, 66, 122, 114), color.RGBA{0, 35, 149, 255})
	fillRect(img, image.Rect(80, 72, 116, 108), color.RGBA{255, 255, 255, 255})
	drawText(img, ttf, "Y", 29, 84, 103, color.RGBA{5, 7, 12, 255})
	drawText(img, ttf, "IGREC", 46, 153, 108, color.RGBA{0, 35, 149, 255})
	drawText(img, ttf, "IGREC", 46, 161, 108, color.RGBA{237, 41, 57, 255})
	drawText(img, ttf, "IGREC", 46, 157, 108, color.RGBA{5, 7, 12, 255})

	wordSize := fittedFontSize(ttf, post.Word, 980, 156, 42)
	wordY := 355
	wordX := centeredTextX(ttf, post.Word, wordSize, width)
	drawText(img, ttf, post.Word, wordSize, wordX-11, wordY, colors.left)
	drawText(img, ttf, post.Word, wordSize, wordX+11, wordY, colors.right)
	drawText(img, ttf, post.Word, wordSize, wordX, wordY, color.RGBA{5, 7, 12, 255})

	byline := "@" + post.Username
	drawText(img, ttf, byline, 34, centeredTextX(ttf, byline, 34, width), 466, color.RGBA{5, 7, 12, 255})
	drawText(img, ttf, post.CreatedAt.Format("2006-01-02 15:04"), 24, 80, 548, color.RGBA{5, 7, 12, 255})
	drawText(img, ttf, "igrec.net", 24, width-220, 548, color.RGBA{5, 7, 12, 255})
	fillRect(img, image.Rect(80, 492, width-80, 497), color.RGBA{5, 7, 12, 255})
	return img, nil
}

type previewColors struct {
	paper color.RGBA
	panel color.RGBA
	line  color.RGBA
	left  color.RGBA
	right color.RGBA
}

func previewCardColors(value string) previewColors {
	palettes := []previewColors{
		{paper: color.RGBA{246, 238, 220, 255}, panel: color.RGBA{255, 252, 242, 255}, line: color.RGBA{230, 220, 190, 120}, left: color.RGBA{0, 35, 149, 255}, right: color.RGBA{237, 41, 57, 255}},
		{paper: color.RGBA{235, 241, 255, 255}, panel: color.RGBA{255, 255, 255, 255}, line: color.RGBA{198, 212, 245, 140}, left: color.RGBA{0, 35, 149, 255}, right: color.RGBA{237, 41, 57, 255}},
		{paper: color.RGBA{255, 236, 239, 255}, panel: color.RGBA{255, 253, 247, 255}, line: color.RGBA{238, 188, 194, 130}, left: color.RGBA{0, 35, 149, 255}, right: color.RGBA{190, 25, 38, 255}},
		{paper: color.RGBA{242, 244, 236, 255}, panel: color.RGBA{255, 255, 250, 255}, line: color.RGBA{207, 215, 196, 140}, left: color.RGBA{0, 35, 149, 255}, right: color.RGBA{237, 41, 57, 255}},
	}
	sum := sha256.Sum256([]byte(strings.ToLower(value)))
	return palettes[int(sum[0])%len(palettes)]
}

func fillRect(img draw.Image, r image.Rectangle, c color.Color) {
	draw.Draw(img, r, &image.Uniform{C: c}, image.Point{}, draw.Src)
}

func drawText(img draw.Image, ttf *truetype.Font, text string, size float64, x, y int, c color.Color) {
	ctx := freetype.NewContext()
	ctx.SetDPI(72)
	ctx.SetFont(ttf)
	ctx.SetFontSize(size)
	ctx.SetClip(img.Bounds())
	ctx.SetDst(img)
	ctx.SetSrc(image.NewUniform(c))
	_, _ = ctx.DrawString(text, freetype.Pt(x, y))
}

func fittedFontSize(ttf *truetype.Font, text string, maxWidth int, start, min float64) float64 {
	for size := start; size >= min; size -= 2 {
		if textWidth(ttf, text, size) <= maxWidth {
			return size
		}
	}
	return min
}

func centeredTextX(ttf *truetype.Font, text string, size float64, canvasWidth int) int {
	return (canvasWidth - textWidth(ttf, text, size)) / 2
}

func textWidth(ttf *truetype.Font, text string, size float64) int {
	face := truetype.NewFace(ttf, &truetype.Options{Size: size, DPI: 72})
	defer face.Close()
	return xfont.MeasureString(face, text).Ceil()
}

func previewCardURL(baseURL string, post store.Post) string {
	return strings.TrimRight(baseURL, "/") + "/og/@" + url.PathEscape(post.Username) + "/" + url.PathEscape(canonicalPostSlug(post)) + ".png"
}
