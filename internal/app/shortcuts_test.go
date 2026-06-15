package app

import (
	"archive/zip"
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestShortcutClipboardPackRequiresPOST(t *testing.T) {
	a := testApp(t)
	req := httptest.NewRequest(http.MethodGet, "/settings/shortcuts/clipboard-pack", nil)
	w := httptest.NewRecorder()

	a.shortcutClipboardPack(w, req)

	if w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected status %d, got %d", http.StatusMethodNotAllowed, w.Code)
	}
}

func TestShortcutClipboardPackDownloadsPersonalizedZip(t *testing.T) {
	a := testApp(t)
	user, err := a.db.CreateUser("shortcutuser", "shortcut@example.com")
	if err != nil {
		t.Fatal(err)
	}
	sessionToken, sessionHash, err := newToken()
	if err != nil {
		t.Fatal(err)
	}
	if err := a.db.CreateSession(sessionHash, user.ID, farFuture()); err != nil {
		t.Fatal(err)
	}

	wGet := httptest.NewRecorder()
	reqGet := httptest.NewRequest(http.MethodGet, "/settings", nil)
	reqGet.AddCookie(&http.Cookie{Name: sessionCookie, Value: sessionToken})
	a.settings(wGet, reqGet)
	csrf := cookieByName(wGet.Result(), csrfCookie)
	if csrf == nil || csrf.Value == "" {
		t.Fatal("expected csrf cookie from GET /settings")
	}

	reqPost := httptest.NewRequest(http.MethodPost, "/settings/shortcuts/clipboard-pack", strings.NewReader(csrfField+"="+csrf.Value))
	reqPost.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	reqPost.AddCookie(&http.Cookie{Name: sessionCookie, Value: sessionToken})
	reqPost.AddCookie(csrf)
	wPost := httptest.NewRecorder()
	a.shortcutClipboardPack(wPost, reqPost)

	if wPost.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d: %s", http.StatusOK, wPost.Code, wPost.Body.String())
	}
	if contentType := wPost.Header().Get("Content-Type"); contentType != "application/zip" {
		t.Fatalf("expected zip content type, got %q", contentType)
	}
	if disposition := wPost.Header().Get("Content-Disposition"); !strings.Contains(disposition, `igrec-shortcuts-shortcutuser.zip`) {
		t.Fatalf("expected attachment filename, got %q", disposition)
	}

	tokens, err := a.db.APITokensByUser(user.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(tokens) != 1 || tokens[0].Name != "siri shortcut" {
		t.Fatalf("expected dedicated siri shortcut token, got %#v", tokens)
	}

	files := unzipFiles(t, wPost.Body.Bytes())
	readme := files["README.txt"]
	if !strings.Contains(readme, "Post to igrec (shortcutuser)") {
		t.Fatalf("expected personalized shortcut name, got %s", readme)
	}
	if !strings.Contains(readme, "Bearer ") {
		t.Fatalf("expected embedded bearer token, got %s", readme)
	}
	if !strings.Contains(files["post-to-igrec.json"], `"mode": "clipboard-or-share-sheet"`) {
		t.Fatalf("expected shortcut manifest, got %s", files["post-to-igrec.json"])
	}
	if !strings.Contains(files["run-post-to-igrec.url.txt"], "shortcuts://run-shortcut?name=Post+to+igrec+%28shortcutuser%29&input=clipboard") {
		t.Fatalf("expected launcher url, got %s", files["run-post-to-igrec.url.txt"])
	}
}

func unzipFiles(t *testing.T, raw []byte) map[string]string {
	t.Helper()
	reader, err := zip.NewReader(bytes.NewReader(raw), int64(len(raw)))
	if err != nil {
		t.Fatal(err)
	}
	files := make(map[string]string, len(reader.File))
	for _, file := range reader.File {
		rc, err := file.Open()
		if err != nil {
			t.Fatal(err)
		}
		body, err := io.ReadAll(rc)
		_ = rc.Close()
		if err != nil {
			t.Fatal(err)
		}
		files[file.Name] = string(body)
	}
	return files
}
