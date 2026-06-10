package app

import (
	"crypto/sha256"
	"fmt"
	"html/template"
	"io/fs"
	"net/http"
	"sort"
	"strings"

	"igrec.net/igrec/web"
)

// assetVersions maps a static asset name (e.g. "igrec.css") to a short
// content hash, computed once at startup from the embedded files.
var assetVersions = computeAssetVersions(web.Static)

// assetsVersion fingerprints the whole static set; it keys the service
// worker cache so every deploy invalidates the app shell cleanly.
var assetsVersion = combinedAssetVersion(assetVersions)

func computeAssetVersions(fsys fs.FS) map[string]string {
	versions := make(map[string]string)
	_ = fs.WalkDir(fsys, ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		raw, err := fs.ReadFile(fsys, path)
		if err != nil {
			return err
		}
		sum := sha256.Sum256(raw)
		versions[path] = fmt.Sprintf("%x", sum[:4])
		return nil
	})
	return versions
}

func combinedAssetVersion(versions map[string]string) string {
	names := make([]string, 0, len(versions))
	for name := range versions {
		names = append(names, name)
	}
	sort.Strings(names)
	h := sha256.New()
	for _, name := range names {
		fmt.Fprintf(h, "%s=%s\n", name, versions[name])
	}
	return fmt.Sprintf("%x", h.Sum(nil)[:4])
}

// assetPath returns the content-addressed URL for an embedded static asset.
func assetPath(name string) string {
	if v, ok := assetVersions[name]; ok {
		return "/static/" + name + "?v=" + v
	}
	return "/static/" + name
}

func templateFuncs() template.FuncMap {
	return template.FuncMap{"asset": assetPath}
}

func parseTemplates() *template.Template {
	return template.Must(template.New("").Funcs(templateFuncs()).ParseFS(web.Templates, "templates/*.html"))
}

// staticHandler serves embedded assets. Hashed URLs cache forever;
// unhashed ones revalidate.
func staticHandler() http.Handler {
	files := http.StripPrefix("/static/", http.FileServerFS(web.Static))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name := strings.TrimPrefix(r.URL.Path, "/static/")
		if v := r.URL.Query().Get("v"); v != "" && v == assetVersions[name] {
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		} else {
			w.Header().Set("Cache-Control", "no-cache")
		}
		files.ServeHTTP(w, r)
	})
}
