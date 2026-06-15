package app

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"encoding/xml"
	"net/http"
	"net/url"
	"strings"
	"time"
)

func (a *App) shortcutClipboardPack(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	user, ok := a.currentUser(r)
	if !ok {
		a.requireLogin(w, r)
		return
	}
	if !a.validCSRF(r) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}

	token, tokenHash, err := newToken()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	prefix := token
	if len(prefix) > 10 {
		prefix = prefix[:10]
	}
	if err := a.db.CreateAPIToken(user.ID, tokenHash, prefix, "siri shortcut"); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	pack, err := a.shortcutClipboardPackZip(user.Username, token)
	if err != nil {
		http.Error(w, "shortcut pack unavailable", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", `attachment; filename="igrec-shortcuts-`+user.Username+`.zip"`)
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(pack)
}

func (a *App) shortcutClipboardPackZip(username, token string) ([]byte, error) {
	baseURL := strings.TrimRight(a.cfg.BaseURL, "/")
	shortcutName := "Post to igrec (" + username + ")"
	runURL := "shortcuts://run-shortcut?name=" + url.QueryEscape(shortcutName) + "&input=clipboard"
	apiURL := baseURL + "/api/words"

	manifest := map[string]any{
		"name":    shortcutName,
		"mode":    "clipboard-or-share-sheet",
		"run_url": runURL,
		"api": map[string]any{
			"url":    apiURL,
			"method": "POST",
			"headers": map[string]string{
				"Authorization": "Bearer " + token,
				"Content-Type":  "application/x-www-form-urlencoded",
			},
			"body_form": map[string]string{
				"word": "<share sheet input or clipboard>",
			},
		},
		"created_at": time.Now().UTC().Format(time.RFC3339),
		"notes": []string{
			"Use share sheet input when present; otherwise use clipboard.",
			"Do not ask for input. This pack is optimized for one-step posting.",
			"Revoke the dedicated token from /settings if this shortcut leaks.",
		},
	}
	manifestJSON, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return nil, err
	}

	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	files := map[string]string{
		"README.txt":                shortcutClipboardPackReadme(baseURL, username, shortcutName, apiURL, runURL, token),
		"post-to-igrec.json":        string(manifestJSON) + "\n",
		"run-post-to-igrec.webloc":  shortcutWebloc(runURL),
		"run-post-to-igrec.url.txt": runURL + "\n",
	}
	for name, body := range files {
		fw, err := zw.Create(name)
		if err != nil {
			return nil, err
		}
		if _, err := fw.Write([]byte(body)); err != nil {
			return nil, err
		}
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func shortcutClipboardPackReadme(baseURL, username, shortcutName, apiURL, runURL, token string) string {
	return strings.Join([]string{
		"igrec siri / shortcuts pack",
		"",
		"This pack creates a one-step shortcut with no prompt.",
		"It posts the share sheet text when present, otherwise the clipboard.",
		"",
		"Shortcut name:",
		shortcutName,
		"",
		"Recommended shortcut build in Apple Shortcuts:",
		"1. Create a new shortcut named exactly: " + shortcutName,
		"2. Enable Siri and, if you want share-sheet posting, set it to receive Text and URLs.",
		"3. Add an If action:",
		"   If Shortcut Input has any value -> use Shortcut Input",
		"   Otherwise -> Get Clipboard",
		"4. Add Get Contents of URL:",
		"   URL: " + apiURL,
		"   Method: POST",
		"   Headers: Authorization = Bearer " + token,
		"   Request Body: Form",
		"   Field: word = the chosen text from step 3",
		"5. Add Show Notification with the posted word or a simple success message.",
		"",
		"One-step launcher URL:",
		runURL,
		"",
		"Notes:",
		"- This is optimized for clipboard or share sheet posting, not spoken dictation.",
		"- True voice-first posting without a follow-up prompt needs a native iOS app with App Intents.",
		"- Revoke the 'siri shortcut' API token from " + baseURL + "/settings if needed.",
		"",
	}, "\n")
}

type weblocPlist struct {
	XMLName xml.Name   `xml:"plist"`
	Version string     `xml:"version,attr"`
	Dict    weblocDict `xml:"dict"`
}

type weblocDict struct {
	Key   string `xml:"key"`
	Value string `xml:"string"`
}

func shortcutWebloc(rawURL string) string {
	payload, err := xml.MarshalIndent(weblocPlist{
		Version: "1.0",
		Dict: weblocDict{
			Key:   "URL",
			Value: rawURL,
		},
	}, "", "  ")
	if err != nil {
		return ""
	}
	return xml.Header + "<!DOCTYPE plist PUBLIC \"-//Apple//DTD PLIST 1.0//EN\" \"http://www.apple.com/DTDs/PropertyList-1.0.dtd\">\n" + string(payload) + "\n"
}
