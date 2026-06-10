// Package web holds the embedded templates and static assets so the
// compiled binary is self-contained and does not depend on its working
// directory.
package web

import (
	"embed"
	"io/fs"
)

//go:embed templates/*.html static
var files embed.FS

// Templates is the embedded template tree rooted at templates/.
var Templates fs.FS = files

// Static is the embedded static asset tree, rooted so that a lookup of
// "igrec.css" resolves to web/static/igrec.css.
var Static fs.FS = mustSub(files, "static")

func mustSub(fsys fs.FS, dir string) fs.FS {
	sub, err := fs.Sub(fsys, dir)
	if err != nil {
		panic(err)
	}
	return sub
}
