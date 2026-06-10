package app

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"net/mail"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	emailpkg "igrec.net/igrec/internal/email"
	"igrec.net/igrec/internal/store"
)

const sessionCookie = "igrec_session"
const csrfCookie = "igrec_csrf"
const csrfField = "csrf_token"

var usernamePattern = regexp.MustCompile(`^[A-Za-z0-9_]{1,32}$`)

func (a *App) join(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		a.render(w, r, "join.html", a.withCSRF(w, r, map[string]any{"Invite": r.URL.Query().Get("invite")}))
	case http.MethodPost:
		if !a.validCSRF(r) {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		inviteCode := strings.TrimSpace(r.FormValue("invite"))
		username, usernameErr := normalizeSignupUsername(r.FormValue("username"))
		email := strings.ToLower(strings.TrimSpace(r.FormValue("email")))
		if usernameErr != nil {
			a.render(w, r, "join.html", a.withCSRF(w, r, map[string]any{"Error": usernameErr.Error(), "Invite": inviteCode, "Username": r.FormValue("username"), "Email": email}))
			return
		}
		if _, err := mail.ParseAddress(email); err != nil {
			a.render(w, r, "join.html", a.withCSRF(w, r, map[string]any{"Error": "email is not valid", "Invite": inviteCode, "Username": username, "Email": email}))
			return
		}
		invite, err := a.db.InviteByCode(inviteCode)
		if err != nil || invite.UsedAt.Valid {
			a.render(w, r, "join.html", a.withCSRF(w, r, map[string]any{"Error": "invite is not valid", "Invite": inviteCode, "Username": username, "Email": email}))
			return
		}
		user, err := a.db.CreateUser(username, email)
		if err != nil {
			a.render(w, r, "join.html", a.withCSRF(w, r, map[string]any{"Error": "username or email is already taken", "Invite": inviteCode, "Username": username, "Email": email}))
			return
		}
		if err := a.db.UseInvite(invite.Code, user.ID); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		if invite.InviterID.Valid {
			_ = a.db.CreateUserFollow(user.ID, invite.InviterID.Int64)
		}
		if cc, err := a.db.UserByUsername("cc00ffee"); err == nil {
			_ = a.db.CreateUserFollow(user.ID, cc.ID)
		}
		if err := a.startSession(w, user.ID); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		http.Redirect(w, r, "/write", http.StatusSeeOther)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (a *App) login(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		if _, ok := a.currentUser(r); ok {
			http.Redirect(w, r, safeNext(r.URL.Query().Get("next")), http.StatusSeeOther)
			return
		}
		a.render(w, r, "login.html", a.withCSRF(w, r, map[string]any{"Next": r.URL.Query().Get("next")}))
	case http.MethodPost:
		if !a.validCSRF(r) {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		email := strings.ToLower(strings.TrimSpace(r.FormValue("email")))
		if !a.allowRate("login:"+clientKey(r)+":"+email, 5, 10*time.Minute) {
			http.Error(w, "too many login emails", http.StatusTooManyRequests)
			return
		}
		next := safeNext(r.FormValue("next"))
		user, err := a.db.UserByEmail(email)
		if err != nil {
			a.render(w, r, "login.html", a.withCSRF(w, r, map[string]any{"Error": "no account uses that email yet", "Email": email, "Next": next}))
			return
		}
		token, tokenHash, err := newToken()
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		if err := a.db.CreateLoginToken(tokenHash, user.ID, time.Now().Add(20*time.Minute)); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		link := strings.TrimRight(a.cfg.BaseURL, "/") + "/auth/magic?token=" + url.QueryEscape(token) + "&next=" + url.QueryEscape(next)
		body := "sign in to igrec:\n\n" + link + "\n\nthis link expires in 20 minutes.\n"
		err = (emailpkg.Resend{APIKey: a.cfg.ResendAPIKey, From: a.cfg.LoginEmailFrom}).SendPlain(user.Email, "igrec sign in", body)
		if err != nil {
			a.render(w, r, "login.html", a.withCSRF(w, r, map[string]any{"Error": err.Error(), "Email": email, "Next": next}))
			return
		}
		a.render(w, r, "login_sent.html", map[string]any{"Email": email})
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (a *App) magic(w http.ResponseWriter, r *http.Request) {
	token := r.URL.Query().Get("token")
	if token == "" {
		http.NotFound(w, r)
		return
	}
	user, err := a.db.UseLoginToken(hashToken(token))
	if err != nil {
		a.render(w, r, "login.html", map[string]any{"Error": "login link is invalid or expired"})
		return
	}
	if err := a.startSession(w, user.ID); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, safeNext(r.URL.Query().Get("next")), http.StatusSeeOther)
}

func (a *App) confirmEmail(w http.ResponseWriter, r *http.Request) {
	token := r.URL.Query().Get("token")
	if token == "" {
		http.NotFound(w, r)
		return
	}
	user, err := a.db.UseEmailChangeToken(hashToken(token))
	if err != nil {
		a.render(w, r, "settings.html", map[string]any{"User": user, "Error": "email link is invalid or expired"})
		return
	}
	if _, ok := a.currentUser(r); !ok {
		if err := a.startSession(w, user.ID); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
	}
	http.Redirect(w, r, "/settings", http.StatusSeeOther)
}

func (a *App) logout(w http.ResponseWriter, r *http.Request) {
	if cookie, err := r.Cookie(sessionCookie); err == nil {
		_ = a.db.DeleteSession(hashToken(cookie.Value))
	}
	http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: "", Path: "/", MaxAge: -1, HttpOnly: true, SameSite: http.SameSiteLaxMode, Secure: a.secureCookies()})
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (a *App) withCSRF(w http.ResponseWriter, r *http.Request, data map[string]any) map[string]any {
	if data == nil {
		data = map[string]any{}
	}
	token, err := a.ensureCSRFToken(w, r)
	if err == nil {
		data["CSRFToken"] = token
	}
	return data
}

func (a *App) ensureCSRFToken(w http.ResponseWriter, r *http.Request) (string, error) {
	if cookie, err := r.Cookie(csrfCookie); err == nil && cookie.Value != "" {
		return cookie.Value, nil
	}
	token, _, err := newToken()
	if err != nil {
		return "", err
	}
	http.SetCookie(w, &http.Cookie{
		Name:     csrfCookie,
		Value:    token,
		Path:     "/",
		HttpOnly: false,
		SameSite: http.SameSiteLaxMode,
		Secure:   a.secureCookies(),
	})
	return token, nil
}

func (a *App) validCSRF(r *http.Request) bool {
	cookie, err := r.Cookie(csrfCookie)
	if err != nil || cookie.Value == "" {
		return false
	}
	formToken := r.FormValue(csrfField)
	if formToken == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(cookie.Value), []byte(formToken)) == 1
}

func (a *App) currentUser(r *http.Request) (store.User, bool) {
	cookie, err := r.Cookie(sessionCookie)
	if err != nil || cookie.Value == "" {
		return store.User{}, false
	}
	user, err := a.db.UserBySessionHash(hashToken(cookie.Value))
	return user, err == nil
}

func (a *App) requireLogin(w http.ResponseWriter, r *http.Request) {
	http.Redirect(w, r, "/login?next="+url.QueryEscape(r.URL.RequestURI()), http.StatusSeeOther)
}

func (a *App) startSession(w http.ResponseWriter, userID int64) error {
	token, tokenHash, err := newToken()
	if err != nil {
		return err
	}
	expires := time.Now().Add(30 * 24 * time.Hour)
	if err := a.db.CreateSession(tokenHash, userID, expires); err != nil {
		return err
	}
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookie,
		Value:    token,
		Path:     "/",
		Expires:  expires,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   a.secureCookies(),
	})
	return nil
}

func (a *App) secureCookies() bool {
	return strings.HasPrefix(a.cfg.BaseURL, "https://")
}

func newToken() (string, string, error) {
	var raw [32]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", "", err
	}
	token := base64.RawURLEncoding.EncodeToString(raw[:])
	return token, hashToken(token), nil
}

func newInviteCode() (string, error) {
	token, _, err := newToken()
	if err != nil {
		return "", err
	}
	return strings.TrimRight(base64.RawURLEncoding.EncodeToString([]byte(token))[:22], "="), nil
}

func NewShortToken() (string, error) {
	var raw [12]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(raw[:]), nil
}

func hashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return fmt.Sprintf("%x", sum[:])
}

func (a *App) emailToken(user store.User) string {
	return EmailToken(a.cfg, user)
}

func EmailToken(cfg Config, user store.User) string {
	secret := cfg.AppSecret
	if secret == "" {
		secret = cfg.DatabaseURL
	}
	payload := fmt.Sprintf("%d:%s", user.ID, strings.ToLower(user.Email))
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write([]byte(payload))
	return base64.RawURLEncoding.EncodeToString([]byte(payload + ":" + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))))
}

func (a *App) userFromEmailToken(token string) (store.User, error) {
	raw, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil {
		return store.User{}, err
	}
	parts := strings.Split(string(raw), ":")
	if len(parts) != 3 {
		return store.User{}, errors.New("invalid email token")
	}
	id, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil {
		return store.User{}, err
	}
	user, err := a.db.UserByID(id)
	if err != nil {
		return store.User{}, err
	}
	if !strings.EqualFold(parts[1], user.Email) {
		return store.User{}, errors.New("email token does not match user")
	}
	secret := a.cfg.AppSecret
	if secret == "" {
		secret = a.cfg.DatabaseURL
	}
	payload := fmt.Sprintf("%d:%s", user.ID, strings.ToLower(user.Email))
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write([]byte(payload))
	expected := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	if subtle.ConstantTimeCompare([]byte(expected), []byte(parts[2])) != 1 {
		return store.User{}, errors.New("invalid email token")
	}
	return user, nil
}

func safeNext(next string) string {
	if next == "" || !strings.HasPrefix(next, "/") || strings.HasPrefix(next, "//") {
		return "/write"
	}
	return next
}

func normalizeSignupUsername(raw string) (string, error) {
	value := strings.TrimSpace(raw)
	value = strings.TrimPrefix(value, "@")
	if strings.Contains(value, "@") {
		parts := strings.Split(value, "@")
		if len(parts) != 2 || !strings.EqualFold(parts[1], "igrec.net") {
			return "", errors.New("choose a handle at igrec.net")
		}
		value = parts[0]
	}
	if !usernamePattern.MatchString(value) {
		return "", errors.New("handle can use letters, numbers, and underscore")
	}
	return value, nil
}
