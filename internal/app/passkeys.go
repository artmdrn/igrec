package app

import (
	"bytes"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/go-webauthn/webauthn/protocol"
	"github.com/go-webauthn/webauthn/webauthn"
	"igrec.net/igrec/internal/store"
)

const passkeyCookie = "igrec_passkey"

type webAuthnUser struct {
	store.User
	credentials []webauthn.Credential
}

func (u webAuthnUser) WebAuthnID() []byte {
	sum := sha256.Sum256([]byte(fmt.Sprintf("igrec-user:%d", u.ID)))
	id := make([]byte, len(sum))
	copy(id, sum[:])
	return id
}

func (u webAuthnUser) WebAuthnName() string {
	if u.Email != "" {
		return u.Email
	}
	return u.Username
}

func (u webAuthnUser) WebAuthnDisplayName() string {
	return "@" + u.Username
}

func (u webAuthnUser) WebAuthnCredentials() []webauthn.Credential {
	return u.credentials
}

func (a *App) passkeyRegisterOptions(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	user, ok := a.currentUser(r)
	if !ok {
		http.Error(w, "login required", http.StatusUnauthorized)
		return
	}
	web, err := a.webAuthn()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	waUser, err := a.webAuthnUser(user)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	options, session, err := web.BeginRegistration(waUser, webauthn.WithAuthenticatorSelection(protocol.AuthenticatorSelection{
		RequireResidentKey: protocol.ResidentKeyRequired(),
		ResidentKey:        protocol.ResidentKeyRequirementRequired,
		UserVerification:   protocol.VerificationPreferred,
	}))
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if err := a.saveWebAuthnSession(w, "register", sql.NullInt64{Int64: user.ID, Valid: true}, session); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, "application/json; charset=utf-8", options)
}

func (a *App) passkeyRegister(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	user, ok := a.currentUser(r)
	if !ok {
		http.Error(w, "login required", http.StatusUnauthorized)
		return
	}
	web, err := a.webAuthn()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	session, err := a.useWebAuthnSession(r, "register")
	if err != nil {
		http.Error(w, "passkey session expired", http.StatusBadRequest)
		return
	}
	waUser, err := a.webAuthnUser(user)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if !bytes.Equal(session.UserID, waUser.WebAuthnID()) {
		http.Error(w, "passkey session mismatch", http.StatusForbidden)
		return
	}
	credential, err := web.FinishRegistration(waUser, session, r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err := a.db.SavePasskey(user.ID, "passkey", *credential); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, "application/json; charset=utf-8", map[string]any{"ok": true})
}

func (a *App) passkeyLoginOptions(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	web, err := a.webAuthn()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	options, session, err := web.BeginDiscoverableLogin(webauthn.WithUserVerification(protocol.VerificationPreferred))
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if err := a.saveWebAuthnSession(w, "login", sql.NullInt64{}, session); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, "application/json; charset=utf-8", options)
}

func (a *App) passkeyLogin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	web, err := a.webAuthn()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	session, err := a.useWebAuthnSession(r, "login")
	if err != nil {
		http.Error(w, "passkey session expired", http.StatusBadRequest)
		return
	}
	user, credential, err := web.FinishPasskeyLogin(a.discoverableUser, session, r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	waUser, ok := user.(webAuthnUser)
	if !ok {
		http.Error(w, "passkey user mismatch", http.StatusInternalServerError)
		return
	}
	if err := a.db.UpdatePasskeyCredential(*credential); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if err := a.startSession(w, waUser.ID); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, "application/json; charset=utf-8", map[string]any{"ok": true, "next": safeNext(r.URL.Query().Get("next"))})
}

func (a *App) webAuthn() (*webauthn.WebAuthn, error) {
	base, err := url.Parse(strings.TrimRight(a.cfg.BaseURL, "/"))
	if err != nil {
		return nil, err
	}
	rpID := base.Hostname()
	if rpID == "" {
		return nil, errors.New("BASE_URL must include a hostname")
	}
	origin := base.Scheme + "://" + base.Host
	return webauthn.New(&webauthn.Config{
		RPID:          rpID,
		RPDisplayName: "igrec",
		RPOrigins:     []string{origin},
	})
}

func (a *App) webAuthnUser(user store.User) (webAuthnUser, error) {
	credentials, err := a.db.PasskeyCredentialsByUser(user.ID)
	if err != nil {
		return webAuthnUser{}, err
	}
	return webAuthnUser{User: user, credentials: credentials}, nil
}

func (a *App) discoverableUser(rawID, userHandle []byte) (webauthn.User, error) {
	user, err := a.db.UserByPasskeyID(rawID)
	if err != nil {
		return nil, err
	}
	return a.webAuthnUser(user)
}

func (a *App) saveWebAuthnSession(w http.ResponseWriter, kind string, userID sql.NullInt64, session *webauthn.SessionData) error {
	raw, err := json.Marshal(session)
	if err != nil {
		return err
	}
	token, _, err := newToken()
	if err != nil {
		return err
	}
	if err := a.db.CreateWebAuthnSession(token, userID, kind, raw, time.Now().Add(5*time.Minute)); err != nil {
		return err
	}
	http.SetCookie(w, &http.Cookie{
		Name:     passkeyCookie,
		Value:    token,
		Path:     "/",
		MaxAge:   300,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   a.secureCookies(),
	})
	return nil
}

func (a *App) useWebAuthnSession(r *http.Request, kind string) (webauthn.SessionData, error) {
	cookie, err := r.Cookie(passkeyCookie)
	if err != nil {
		return webauthn.SessionData{}, err
	}
	record, err := a.db.UseWebAuthnSession(cookie.Value, kind)
	if err != nil {
		return webauthn.SessionData{}, err
	}
	var session webauthn.SessionData
	if err := json.Unmarshal(record.Data, &session); err != nil {
		return webauthn.SessionData{}, err
	}
	return session, nil
}
