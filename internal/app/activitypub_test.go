package app

import (
	"crypto"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (fn roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return fn(req)
}

func TestActivityPubActorIncludesPublicKeyAndMedia(t *testing.T) {
	a := testApp(t)
	if _, err := a.db.CreateUser("cc00ffee", "cc@example.com"); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodGet, "/ap/users/cc00ffee", nil)
	w := httptest.NewRecorder()
	a.actor(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d", http.StatusOK, w.Code)
	}
	var actor map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &actor); err != nil {
		t.Fatal(err)
	}
	publicKey, ok := actor["publicKey"].(map[string]any)
	if !ok {
		t.Fatalf("expected publicKey in actor: %#v", actor)
	}
	if got, _ := publicKey["id"].(string); got != "http://localhost:8080/ap/users/cc00ffee#main-key" {
		t.Fatalf("unexpected public key id %q", got)
	}
	if pem, _ := publicKey["publicKeyPem"].(string); !strings.Contains(pem, "BEGIN PUBLIC KEY") {
		t.Fatalf("expected public key pem, got %q", pem)
	}
	if _, ok := actor["icon"].(map[string]any); !ok {
		t.Fatalf("expected icon in actor: %#v", actor)
	}
	if _, ok := actor["image"].(map[string]any); !ok {
		t.Fatalf("expected image in actor: %#v", actor)
	}
	if got, _ := actor["name"].(string); got != "cc00ffee · igrec" {
		t.Fatalf("unexpected actor name %q", got)
	}
	if got, _ := actor["summary"].(string); got == "" {
		t.Fatalf("expected actor summary")
	}
	if got, _ := actor["followers"].(string); got != "http://localhost:8080/ap/users/cc00ffee/followers" {
		t.Fatalf("unexpected followers URL %q", got)
	}
	if got, _ := actor["following"].(string); got != "http://localhost:8080/ap/users/cc00ffee/following" {
		t.Fatalf("unexpected following URL %q", got)
	}
}

func TestSignActivityPubRequestUsesActorKey(t *testing.T) {
	a := testApp(t)
	user, err := a.db.CreateUser("cc00ffee", "cc@example.com")
	if err != nil {
		t.Fatal(err)
	}
	privateKey, err := a.activityPubPrivateKey(user)
	if err != nil {
		t.Fatal(err)
	}
	publicPEM, err := a.activityPubPublicKey(user)
	if err != nil {
		t.Fatal(err)
	}
	publicKey := parseTestRSAPublicKey(t, publicPEM)
	if publicKey.N.Cmp(privateKey.N) != 0 || publicKey.E != privateKey.E {
		t.Fatal("expected actor public key to match signing private key")
	}

	body := []byte(`{"type":"Accept"}`)
	req := httptest.NewRequest(http.MethodPost, "https://remote.example/inbox?x=1", strings.NewReader(string(body)))
	req.Header.Set("Date", "Mon, 29 Jun 2026 12:00:00 GMT")
	sum := sha256.Sum256(body)
	req.Header.Set("Digest", "SHA-256="+base64.StdEncoding.EncodeToString(sum[:]))
	keyID := activitypubActorID(a.cfg.BaseURL, user.Username) + "#main-key"

	if err := signActivityPubRequest(req, privateKey, keyID); err != nil {
		t.Fatal(err)
	}
	signature := req.Header.Get("Signature")
	if !strings.Contains(signature, `keyId="`+keyID+`"`) || !strings.Contains(signature, `headers="(request-target) host date digest"`) {
		t.Fatalf("unexpected signature header %q", signature)
	}
	sig := signatureParam(t, signature, "signature")
	signed := "(request-target): post /inbox?x=1\n" +
		"host: remote.example\n" +
		"date: Mon, 29 Jun 2026 12:00:00 GMT\n" +
		"digest: " + req.Header.Get("Digest")
	digest := sha256.Sum256([]byte(signed))
	rawSig, err := base64.StdEncoding.DecodeString(sig)
	if err != nil {
		t.Fatal(err)
	}
	if err := rsa.VerifyPKCS1v15(publicKey, crypto.SHA256, digest[:], rawSig); err != nil {
		t.Fatalf("signature does not verify with actor public key: %v", err)
	}
	if req.Host != "remote.example" {
		t.Fatalf("expected Host to be signed host, got %q", req.Host)
	}
}

func TestActivityPubFollowersCollection(t *testing.T) {
	a := testApp(t)
	user, err := a.db.CreateUser("cc00ffee", "cc@example.com")
	if err != nil {
		t.Fatal(err)
	}
	if err := a.db.UpsertActivityPubFollower(user.ID, "https://example.social/users/a", "https://example.social/inbox"); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodGet, "/ap/users/cc00ffee/followers", nil)
	w := httptest.NewRecorder()
	a.actor(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d", http.StatusOK, w.Code)
	}
	if !strings.Contains(w.Body.String(), `"totalItems":1`) {
		t.Fatalf("expected one follower, got %s", w.Body.String())
	}
}

func TestActivityPubFollowingCollection(t *testing.T) {
	a := testApp(t)
	if _, err := a.db.CreateUser("cc00ffee", "cc@example.com"); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodGet, "/ap/users/cc00ffee/following", nil)
	w := httptest.NewRecorder()
	a.actor(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d", http.StatusOK, w.Code)
	}
	if !strings.Contains(w.Body.String(), `"totalItems":0`) {
		t.Fatalf("expected zero following, got %s", w.Body.String())
	}
}

func TestStartAccountMigrationQueuesMoveAndRedirectsActor(t *testing.T) {
	a := testApp(t)
	user, err := a.db.CreateUser("member", "member@example.com")
	if err != nil {
		t.Fatal(err)
	}
	target := "https://remote.example/users/member"
	if err := a.db.UpdateSettings(user.ID, "smart", false, "", target); err != nil {
		t.Fatal(err)
	}
	user, err = a.db.UserByID(user.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := a.db.UpsertActivityPubFollower(user.ID, "https://follower.example/users/a", "https://follower.example/inbox"); err != nil {
		t.Fatal(err)
	}

	oldClient := http.DefaultClient
	http.DefaultClient = &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		body := `{"id":"` + target + `","type":"Person","alsoKnownAs":["http://localhost:8080/ap/users/member"]}`
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body)), Request: req}, nil
	})}
	t.Cleanup(func() { http.DefaultClient = oldClient })

	if err := a.startAccountMigration(t.Context(), user); err != nil {
		t.Fatal(err)
	}
	migration, err := a.db.AccountMigrationByUser(user.ID)
	if err != nil {
		t.Fatal(err)
	}
	if migration.TargetActor != target {
		t.Fatalf("expected migration target %q, got %q", target, migration.TargetActor)
	}
	deliveries, err := a.db.DueActivityPubDeliveries(farFuture(), 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(deliveries) != 1 || deliveries[0].Inbox != "https://follower.example/inbox" {
		t.Fatalf("expected one queued follower delivery, got %#v", deliveries)
	}
	var move map[string]any
	if err := json.Unmarshal(deliveries[0].Activity, &move); err != nil {
		t.Fatal(err)
	}
	if move["type"] != "Move" || move["object"] != "http://localhost:8080/ap/users/member" || move["target"] != target {
		t.Fatalf("unexpected Move activity: %#v", move)
	}

	req := httptest.NewRequest(http.MethodGet, "/ap/users/member", nil)
	w := httptest.NewRecorder()
	a.actor(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d", http.StatusOK, w.Code)
	}
	var actor map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &actor); err != nil {
		t.Fatal(err)
	}
	if actor["movedTo"] != target || actor["discoverable"] != false {
		t.Fatalf("expected migrated actor, got %#v", actor)
	}
}

func TestStartAccountMigrationRequiresAlias(t *testing.T) {
	a := testApp(t)
	user, err := a.db.CreateUser("member", "member@example.com")
	if err != nil {
		t.Fatal(err)
	}
	target := "https://remote.example/users/member"
	if err := a.db.UpdateSettings(user.ID, "smart", false, "", target); err != nil {
		t.Fatal(err)
	}
	user, err = a.db.UserByID(user.ID)
	if err != nil {
		t.Fatal(err)
	}

	oldClient := http.DefaultClient
	http.DefaultClient = &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		body := `{"id":"` + target + `","type":"Person"}`
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body)), Request: req}, nil
	})}
	t.Cleanup(func() { http.DefaultClient = oldClient })

	err = a.startAccountMigration(t.Context(), user)
	if err == nil || !strings.Contains(err.Error(), "must first list this igrec account as an alias") {
		t.Fatalf("expected alias verification error, got %v", err)
	}
	if _, err := a.db.AccountMigrationByUser(user.ID); err == nil {
		t.Fatal("expected migration not to start")
	}
}

func parseTestRSAPublicKey(t *testing.T, publicPEM string) *rsa.PublicKey {
	t.Helper()
	block, _ := pem.Decode([]byte(publicPEM))
	if block == nil {
		t.Fatal("public key PEM did not decode")
	}
	raw, err := x509.ParsePKIXPublicKey(block.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	publicKey, ok := raw.(*rsa.PublicKey)
	if !ok {
		t.Fatalf("expected RSA public key, got %T", raw)
	}
	return publicKey
}

func signatureParam(t *testing.T, header, key string) string {
	t.Helper()
	for _, part := range strings.Split(header, ",") {
		name, value, ok := strings.Cut(strings.TrimSpace(part), "=")
		if !ok || name != key {
			continue
		}
		return strings.Trim(value, `"`)
	}
	t.Fatalf("signature header missing %s: %q", key, header)
	return ""
}
