package app

import (
	"archive/zip"
	"bytes"
	"crypto/sha1"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"igrec.net/igrec/internal/store"
)

type walletPassData struct {
	User        store.User
	SelfPost    sql.Null[store.Post]
	FriendPosts []store.Post
	WriteURL    string
}

func (a *App) appleWalletPass(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	user, ok := a.currentUser(r)
	if !ok {
		a.requireLogin(w, r)
		return
	}
	if !a.appleWalletConfigured() {
		http.Error(w, "apple wallet is not configured yet", http.StatusServiceUnavailable)
		return
	}
	pkg, err := a.buildAppleWalletPass(user)
	if err != nil {
		http.Error(w, "wallet pass unavailable", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/vnd.apple.pkpass")
	w.Header().Set("Content-Disposition", `attachment; filename="igrec-`+user.Username+`.pkpass"`)
	w.Header().Set("Cache-Control", "no-store")
	if r.Method == http.MethodHead {
		return
	}
	_, _ = w.Write(pkg)
}

func (a *App) appleWalletConfigured() bool {
	cfg := a.cfg.ApplePass
	return strings.TrimSpace(cfg.PassTypeID) != "" &&
		strings.TrimSpace(cfg.TeamID) != "" &&
		strings.TrimSpace(cfg.CertPath) != "" &&
		strings.TrimSpace(cfg.KeyPath) != "" &&
		strings.TrimSpace(cfg.WWDRPath) != ""
}

func (a *App) buildAppleWalletPass(user store.User) ([]byte, error) {
	data := walletPassData{
		User:     user,
		WriteURL: strings.TrimRight(a.cfg.BaseURL, "/") + "/write?source=wallet",
	}
	if post, err := a.db.LatestPostByUser(user.Username); err == nil {
		data.SelfPost = sql.Null[store.Post]{V: post, Valid: true}
	}
	if posts, err := a.db.FriendPosts(user.ID, 4); err == nil {
		data.FriendPosts = posts
	}
	files, err := a.applePassFiles(data)
	if err != nil {
		return nil, err
	}
	return a.signAndZipPass(files)
}

func (a *App) applePassFiles(data walletPassData) (map[string][]byte, error) {
	pass, err := json.MarshalIndent(a.applePassJSON(data), "", "  ")
	if err != nil {
		return nil, err
	}
	files := map[string][]byte{"pass.json": pass}
	if icon, err := os.ReadFile("web/static/favicon-32.png"); err == nil {
		files["icon.png"] = icon
		files["icon@2x.png"] = icon
	}
	if logo, err := os.ReadFile("web/static/apple-touch-icon.png"); err == nil {
		files["logo.png"] = logo
		files["logo@2x.png"] = logo
	}
	if _, ok := files["icon.png"]; !ok {
		return nil, errors.New("wallet icon asset missing")
	}
	return files, nil
}

func (a *App) applePassJSON(data walletPassData) map[string]any {
	wordValue := ">_"
	changedAt := time.Now()
	if data.SelfPost.Valid {
		wordValue = data.SelfPost.V.Word
		changedAt = data.SelfPost.V.CreatedAt
	}
	friendValue := friendWordsValue(data.FriendPosts)
	if friendValue == "" {
		friendValue = "no friends yet"
	}
	return map[string]any{
		"formatVersion":              1,
		"passTypeIdentifier":         a.cfg.ApplePass.PassTypeID,
		"serialNumber":               "igrec-" + strconv.FormatInt(data.User.ID, 10),
		"teamIdentifier":             a.cfg.ApplePass.TeamID,
		"organizationName":           "igrec",
		"description":                "igrec pocket word",
		"logoText":                   "igrec",
		"foregroundColor":            "rgb(17, 17, 17)",
		"backgroundColor":            "rgb(246, 238, 220)",
		"labelColor":                 "rgb(0, 85, 164)",
		"sharingProhibited":          false,
		"relevantDate":               changedAt.UTC().Format(time.RFC3339),
		"associatedStoreIdentifiers": []any{},
		"generic": map[string]any{
			"primaryFields": []any{
				map[string]string{"key": "word", "label": "last word", "value": wordValue},
			},
			"secondaryFields": []any{
				map[string]string{"key": "handle", "label": "handle", "value": "@" + data.User.Username},
			},
			"auxiliaryFields": []any{
				map[string]string{"key": "friends", "label": "friends", "value": friendValue},
			},
			"backFields": []any{
				map[string]string{"key": "write", "label": "write", "value": data.WriteURL},
				map[string]string{"key": "profile", "label": "profile", "value": strings.TrimRight(a.cfg.BaseURL, "/") + "/@" + url.PathEscape(data.User.Username)},
			},
		},
		"barcodes": []any{
			map[string]string{"format": "PKBarcodeFormatQR", "message": data.WriteURL, "messageEncoding": "iso-8859-1"},
		},
	}
}

func friendWordsValue(posts []store.Post) string {
	values := make([]string, 0, len(posts))
	for _, post := range posts {
		values = append(values, "@"+post.Username+": "+post.Word)
	}
	return strings.Join(values, "  ")
}

func (a *App) signAndZipPass(files map[string][]byte) ([]byte, error) {
	dir, err := os.MkdirTemp("", "igrec-pass-*")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(dir)

	for name, body := range files {
		if err := os.WriteFile(filepath.Join(dir, name), body, 0o600); err != nil {
			return nil, err
		}
	}
	manifest := make(map[string]string, len(files))
	for name, body := range files {
		sum := sha1.Sum(body)
		manifest[name] = hex.EncodeToString(sum[:])
	}
	manifestBody, err := json.Marshal(manifest)
	if err != nil {
		return nil, err
	}
	if err := os.WriteFile(filepath.Join(dir, "manifest.json"), manifestBody, 0o600); err != nil {
		return nil, err
	}
	if err := a.signPassManifest(dir); err != nil {
		return nil, err
	}
	return zipPassDirectory(dir)
}

func (a *App) signPassManifest(dir string) error {
	cfg := a.cfg.ApplePass
	args := []string{
		"smime", "-binary", "-sign",
		"-certfile", cfg.WWDRPath,
		"-signer", cfg.CertPath,
		"-inkey", cfg.KeyPath,
		"-outform", "DER",
		"-in", filepath.Join(dir, "manifest.json"),
		"-out", filepath.Join(dir, "signature"),
	}
	cmd := exec.Command("openssl", args...)
	if strings.TrimSpace(cfg.KeyPassword) != "" {
		args = append(args, "-passin", "env:APPLE_PASS_KEY_PASSWORD")
		cmd = exec.Command("openssl", args...)
		cmd.Env = append(os.Environ(), "APPLE_PASS_KEY_PASSWORD="+cfg.KeyPassword)
	}
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("sign pass manifest: %w: %s", err, strings.TrimSpace(string(output)))
	}
	return nil
}

func zipPassDirectory(dir string) ([]byte, error) {
	var out bytes.Buffer
	zw := zip.NewWriter(&out)
	for _, name := range []string{"pass.json", "icon.png", "icon@2x.png", "logo.png", "logo@2x.png", "manifest.json", "signature"} {
		body, err := os.ReadFile(filepath.Join(dir, name))
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, err
		}
		w, err := zw.Create(name)
		if err != nil {
			return nil, err
		}
		if _, err := io.Copy(w, bytes.NewReader(body)); err != nil {
			return nil, err
		}
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}
