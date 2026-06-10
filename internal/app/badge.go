package app

import (
	"fmt"
	"html/template"
	"strconv"
	"strings"

	"igrec.net/igrec/internal/store"
)

func renderBadgeSVG(user store.User, post store.Post) string {
	wordText := template.HTMLEscapeString(post.Word)
	userText := template.HTMLEscapeString("@" + user.Username)
	dateText := template.HTMLEscapeString(post.CreatedAt.Format("2006-01-02"))
	width := 260 + len([]rune(post.Word))*30
	if width < 520 {
		width = 520
	}
	if width > 980 {
		width = 980
	}
	wordSize := 74
	if len([]rune(post.Word)) > 10 {
		wordSize = 58
	}
	if len([]rune(post.Word)) > 18 {
		wordSize = 44
	}
	stripes := badgeStripePath(width)
	return fmt.Sprintf(`<svg xmlns="http://www.w3.org/2000/svg" width="%d" height="180" viewBox="0 0 %d 180" role="img" aria-label="%s said %s">
<rect width="100%%" height="100%%" fill="#f6eedc"/>
<path d="%s" stroke="#e4d8b8" stroke-width="1"/>
<rect x="10" y="10" width="%d" height="160" fill="#fffef7" stroke="#111" stroke-width="4"/>
<rect x="24" y="24" width="30" height="30" fill="#fff" stroke="#0055a4" stroke-width="3"/>
<text x="32" y="47" font-family="ui-monospace, SFMono-Regular, Menlo, monospace" font-size="24" font-weight="900" fill="#111">Y</text>
<text x="66" y="47" font-family="ui-monospace, SFMono-Regular, Menlo, monospace" font-size="22" font-weight="900" fill="#0055a4">IGREC</text>
<text x="70" y="47" font-family="ui-monospace, SFMono-Regular, Menlo, monospace" font-size="22" font-weight="900" fill="#ef4135">IGREC</text>
<text x="68" y="47" font-family="ui-monospace, SFMono-Regular, Menlo, monospace" font-size="22" font-weight="900" fill="#111">IGREC</text>
<text x="32" y="118" font-family="ui-monospace, SFMono-Regular, Menlo, monospace" font-size="%d" font-weight="900" fill="#0055a4">%s</text>
<text x="38" y="118" font-family="ui-monospace, SFMono-Regular, Menlo, monospace" font-size="%d" font-weight="900" fill="#ef4135">%s</text>
<text x="35" y="118" font-family="ui-monospace, SFMono-Regular, Menlo, monospace" font-size="%d" font-weight="900" fill="#111">%s</text>
<path d="M24 136H%d" stroke="#111" stroke-width="3"/>
<text x="24" y="158" font-family="ui-monospace, SFMono-Regular, Menlo, monospace" font-size="18" fill="#111">%s</text>
<text x="%d" y="158" text-anchor="end" font-family="ui-monospace, SFMono-Regular, Menlo, monospace" font-size="18" fill="#111">igrec.net · %s</text>
</svg>`, width, width, userText, wordText, stripes, width-20, wordSize, wordText, wordSize, wordText, wordSize, wordText, width-24, userText, width-24, dateText)
}

func badgeStripePath(width int) string {
	var b strings.Builder
	for y := 12; y <= 172; y += 8 {
		if b.Len() > 0 {
			b.WriteByte(' ')
		}
		b.WriteString("M0 ")
		b.WriteString(strconv.Itoa(y))
		b.WriteString("H")
		b.WriteString(strconv.Itoa(width))
	}
	return b.String()
}
