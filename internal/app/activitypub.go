package app

import (
	"bytes"
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"igrec.net/igrec/internal/activitypub"
	"igrec.net/igrec/internal/store"
)

const activityPubPublic = "https://www.w3.org/ns/activitystreams#Public"

type activityPubActivity struct {
	Context any    `json:"@context,omitempty"`
	ID      string `json:"id,omitempty"`
	Type    string `json:"type"`
	Actor   string `json:"actor,omitempty"`
	Object  any    `json:"object,omitempty"`
	To      any    `json:"to,omitempty"`
	CC      any    `json:"cc,omitempty"`
}

type remoteActor struct {
	ID            string `json:"id"`
	Type          string `json:"type"`
	Inbox         string `json:"inbox"`
	PreferredName string `json:"preferredUsername"`
	Endpoints     struct {
		SharedInbox string `json:"sharedInbox"`
	} `json:"endpoints"`
}

func (a *App) webfinger(w http.ResponseWriter, r *http.Request) {
	resource := r.URL.Query().Get("resource")
	if !strings.HasPrefix(resource, "acct:") {
		http.NotFound(w, r)
		return
	}
	name := strings.TrimPrefix(strings.Split(strings.TrimPrefix(resource, "acct:"), "@")[0], "@")
	if name == "" {
		http.NotFound(w, r)
		return
	}
	if _, err := a.db.UserByUsername(name); err != nil {
		http.NotFound(w, r)
		return
	}
	writeJSON(w, "application/jrd+json; charset=utf-8", activitypub.WebFinger(a.cfg.BaseURL, name))
}

func (a *App) actor(w http.ResponseWriter, r *http.Request) {
	rest := strings.TrimPrefix(r.URL.Path, "/ap/users/")
	parts := strings.Split(rest, "/")
	username, err := url.PathUnescape(parts[0])
	if err != nil || username == "" {
		http.NotFound(w, r)
		return
	}
	user, err := a.db.UserByUsername(username)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if len(parts) > 1 && parts[1] == "inbox" {
		a.activityPubInbox(w, r, user)
		return
	}
	if len(parts) > 1 && parts[1] == "followers" {
		followers, err := a.db.ActivityPubFollowers(user.ID)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		writeJSON(w, "application/activity+json; charset=utf-8", map[string]any{
			"@context":   "https://www.w3.org/ns/activitystreams",
			"id":         activitypubActorID(a.cfg.BaseURL, user.Username) + "/followers",
			"type":       "OrderedCollection",
			"totalItems": len(followers),
		})
		return
	}
	if len(parts) > 1 && parts[1] == "following" {
		writeJSON(w, "application/activity+json; charset=utf-8", map[string]any{
			"@context":   "https://www.w3.org/ns/activitystreams",
			"id":         activitypubActorID(a.cfg.BaseURL, user.Username) + "/following",
			"type":       "OrderedCollection",
			"totalItems": 0,
		})
		return
	}
	if len(parts) > 1 && parts[1] == "outbox" {
		posts, err := a.db.PostsByUser(user.Username, 20)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		items := make([]any, 0, len(posts))
		for _, post := range posts {
			items = append(items, activitypub.Note(a.cfg.BaseURL, post))
		}
		writeJSON(w, "application/activity+json; charset=utf-8", map[string]any{"@context": "https://www.w3.org/ns/activitystreams", "type": "OrderedCollection", "orderedItems": items})
		return
	}
	publicKey, err := a.activityPubPublicKey(user)
	if err != nil {
		http.Error(w, "activitypub key unavailable", http.StatusInternalServerError)
		return
	}
	writeJSON(w, "application/activity+json; charset=utf-8", activitypub.Actor(a.cfg.BaseURL, user, publicKey))
}

func (a *App) activityPubInbox(w http.ResponseWriter, r *http.Request, user store.User) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !a.allowRate("ap-inbox:"+user.Username+":"+clientKey(r), 120, time.Minute) {
		http.Error(w, "rate limited", http.StatusTooManyRequests)
		return
	}
	var activity activityPubActivity
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&activity); err != nil {
		http.Error(w, "invalid activity", http.StatusBadRequest)
		return
	}
	switch activity.Type {
	case "Follow":
		a.acceptActivityPubFollow(w, r, user, activity)
	case "Undo":
		a.undoActivityPubFollow(w, user, activity)
	default:
		w.WriteHeader(http.StatusAccepted)
	}
}

func (a *App) acceptActivityPubFollow(w http.ResponseWriter, r *http.Request, user store.User, activity activityPubActivity) {
	if activity.Actor == "" || !sameActivityPubObject(activity.Object, activitypubActorID(a.cfg.BaseURL, user.Username)) {
		http.Error(w, "unsupported follow", http.StatusBadRequest)
		return
	}
	remote, err := fetchRemoteActor(r.Context(), activity.Actor)
	if err != nil {
		http.Error(w, "remote actor unavailable", http.StatusBadGateway)
		return
	}
	inbox := remote.Endpoints.SharedInbox
	if inbox == "" {
		inbox = remote.Inbox
	}
	if remote.ID != "" && remote.ID != activity.Actor {
		http.Error(w, "actor mismatch", http.StatusBadRequest)
		return
	}
	if inbox == "" {
		http.Error(w, "remote inbox unavailable", http.StatusBadRequest)
		return
	}
	if err := a.db.UpsertActivityPubFollower(user.ID, activity.Actor, inbox); err != nil {
		http.Error(w, "follow failed", http.StatusInternalServerError)
		return
	}
	accept := activityPubActivity{
		Context: "https://www.w3.org/ns/activitystreams",
		ID:      activitypubActorID(a.cfg.BaseURL, user.Username) + "/accept/" + randomID(),
		Type:    "Accept",
		Actor:   activitypubActorID(a.cfg.BaseURL, user.Username),
		Object:  activity,
		To:      []string{activity.Actor},
	}
	if err := a.deliverActivity(user, inbox, accept); err != nil {
		log.Printf("activitypub accept delivery failed user=%s inbox=%s err=%v", user.Username, inbox, err)
		a.enqueueActivityPubDelivery(user, inbox, accept, err)
	}
	go a.deliverRecentPostsToInbox(user, inbox, 5)
	w.WriteHeader(http.StatusAccepted)
}

func (a *App) undoActivityPubFollow(w http.ResponseWriter, user store.User, activity activityPubActivity) {
	actor := activity.Actor
	if nested, ok := activity.Object.(map[string]any); ok {
		if nestedType, _ := nested["type"].(string); nestedType == "Follow" {
			if nestedActor, _ := nested["actor"].(string); nestedActor != "" {
				actor = nestedActor
			}
		}
	}
	if actor != "" {
		_ = a.db.DeleteActivityPubFollower(user.ID, actor)
	}
	w.WriteHeader(http.StatusAccepted)
}

func (a *App) deliverPost(post store.Post) {
	followers, err := a.db.ActivityPubFollowers(post.UserID)
	if err != nil {
		log.Printf("activitypub followers failed post=%d err=%v", post.ID, err)
		return
	}
	if len(followers) == 0 {
		return
	}
	user := store.User{ID: post.UserID, Username: post.Username}
	create := activitypub.Create(a.cfg.BaseURL, post)
	for _, follower := range followers {
		if err := a.deliverActivity(user, follower.Inbox, create); err != nil {
			log.Printf("activitypub create delivery failed post=%d actor=%s inbox=%s err=%v", post.ID, follower.Actor, follower.Inbox, err)
			a.enqueueActivityPubDelivery(user, follower.Inbox, create, err)
		}
	}
}

func (a *App) deliverRecentPostsToInbox(user store.User, inbox string, limit int) {
	if limit <= 0 {
		return
	}
	posts, err := a.db.PostsByUser(user.Username, limit)
	if err != nil {
		log.Printf("activitypub backfill posts failed user=%s err=%v", user.Username, err)
		return
	}
	// Deliver oldest first so a new follower sees the arrival packet in natural order.
	for i := len(posts) - 1; i >= 0; i-- {
		post := posts[i]
		create := activitypub.Create(a.cfg.BaseURL, post)
		if err := a.deliverActivity(user, inbox, create); err != nil {
			log.Printf("activitypub backfill delivery failed user=%s post=%d inbox=%s err=%v", user.Username, post.ID, inbox, err)
			a.enqueueActivityPubDelivery(user, inbox, create, err)
		}
	}
}

func (a *App) enqueueActivityPubDelivery(user store.User, inbox string, activity any, cause error) {
	raw, err := json.Marshal(activity)
	if err != nil {
		log.Printf("activitypub queue marshal failed user=%s inbox=%s err=%v", user.Username, inbox, err)
		return
	}
	message := ""
	if cause != nil {
		message = cause.Error()
	}
	if err := a.db.EnqueueActivityPubDelivery(user.ID, inbox, raw, time.Now().Add(10*time.Minute), message); err != nil {
		log.Printf("activitypub queue insert failed user=%s inbox=%s err=%v", user.Username, inbox, err)
	}
}

func (a *App) RetryActivityPubDeliveries(limit int) (int, int, error) {
	deliveries, err := a.db.DueActivityPubDeliveries(time.Now(), limit)
	if err != nil {
		return 0, 0, err
	}
	delivered := 0
	failed := 0
	for _, delivery := range deliveries {
		user, err := a.db.UserByID(delivery.UserID)
		if err != nil {
			failed++
			_ = a.db.MarkActivityPubDeliveryFailed(delivery.ID, delivery.Attempts+1, nextActivityPubRetry(delivery.Attempts+1), err.Error())
			continue
		}
		if err := a.deliverActivityBytes(user, delivery.Inbox, delivery.Activity); err != nil {
			failed++
			_ = a.db.MarkActivityPubDeliveryFailed(delivery.ID, delivery.Attempts+1, nextActivityPubRetry(delivery.Attempts+1), err.Error())
			continue
		}
		if err := a.db.MarkActivityPubDeliveryDelivered(delivery.ID); err != nil {
			return delivered, failed, err
		}
		delivered++
	}
	if _, err := a.db.PruneDeliveredActivityPubDeliveries(time.Now().Add(-7 * 24 * time.Hour)); err != nil {
		return delivered, failed, err
	}
	return delivered, failed, nil
}

func nextActivityPubRetry(attempts int) time.Time {
	delays := []time.Duration{
		10 * time.Minute,
		30 * time.Minute,
		2 * time.Hour,
		8 * time.Hour,
		24 * time.Hour,
	}
	if attempts <= 0 {
		attempts = 1
	}
	idx := attempts - 1
	if idx >= len(delays) {
		idx = len(delays) - 1
	}
	return time.Now().Add(delays[idx])
}

func (a *App) deliverActivity(user store.User, inbox string, activity any) error {
	if !safeRemoteURL(inbox) {
		return fmt.Errorf("unsafe inbox url")
	}
	raw, err := json.Marshal(activity)
	if err != nil {
		return err
	}
	return a.deliverActivityBytes(user, inbox, raw)
}

func (a *App) deliverActivityBytes(user store.User, inbox string, raw []byte) error {
	if !safeRemoteURL(inbox) {
		return fmt.Errorf("unsafe inbox url")
	}
	key, err := a.activityPubPrivateKey(user)
	if err != nil {
		return err
	}
	req, err := http.NewRequest(http.MethodPost, inbox, bytes.NewReader(raw))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/activity+json")
	req.Header.Set("Accept", "application/activity+json")
	req.Header.Set("Date", time.Now().UTC().Format(http.TimeFormat))
	sum := sha256.Sum256(raw)
	req.Header.Set("Digest", "SHA-256="+base64.StdEncoding.EncodeToString(sum[:]))
	if err := signActivityPubRequest(req, key, activitypubActorID(a.cfg.BaseURL, user.Username)+"#main-key"); err != nil {
		return err
	}
	client := &http.Client{Timeout: 12 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return fmt.Errorf("remote status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	return nil
}

func (a *App) activityPubPublicKey(user store.User) (string, error) {
	key, err := a.ensureActivityPubKey(user.ID)
	if err != nil {
		return "", err
	}
	return key.PublicKeyPEM, nil
}

func (a *App) activityPubPrivateKey(user store.User) (*rsa.PrivateKey, error) {
	key, err := a.ensureActivityPubKey(user.ID)
	if err != nil {
		return nil, err
	}
	block, _ := pem.Decode([]byte(key.PrivateKeyPEM))
	if block == nil {
		return nil, errors.New("invalid activitypub private key")
	}
	return x509.ParsePKCS1PrivateKey(block.Bytes)
}

func (a *App) ensureActivityPubKey(userID int64) (store.ActivityPubKey, error) {
	key, err := a.db.ActivityPubKey(userID)
	if err == nil {
		return key, nil
	}
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return store.ActivityPubKey{}, err
	}
	privatePEM := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(privateKey)})
	publicDER, err := x509.MarshalPKIXPublicKey(&privateKey.PublicKey)
	if err != nil {
		return store.ActivityPubKey{}, err
	}
	publicPEM := pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: publicDER})
	if err := a.db.CreateActivityPubKey(userID, string(privatePEM), string(publicPEM)); err != nil {
		return store.ActivityPubKey{}, err
	}
	return store.ActivityPubKey{UserID: userID, PrivateKeyPEM: string(privatePEM), PublicKeyPEM: string(publicPEM)}, nil
}

func signActivityPubRequest(req *http.Request, key *rsa.PrivateKey, keyID string) error {
	host := req.URL.Host
	target := strings.ToLower(req.Method) + " " + req.URL.RequestURI()
	signed := "(request-target): " + target + "\n" +
		"host: " + host + "\n" +
		"date: " + req.Header.Get("Date") + "\n" +
		"digest: " + req.Header.Get("Digest")
	digest := sha256.Sum256([]byte(signed))
	signature, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, digest[:])
	if err != nil {
		return err
	}
	req.Host = host
	req.Header.Set("Signature", fmt.Sprintf(`keyId="%s",algorithm="rsa-sha256",headers="(request-target) host date digest",signature="%s"`, keyID, base64.StdEncoding.EncodeToString(signature)))
	return nil
}

func fetchRemoteActor(ctx context.Context, actorURL string) (remoteActor, error) {
	if !safeRemoteURL(actorURL) {
		return remoteActor{}, errors.New("unsafe actor url")
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, actorURL, nil)
	if err != nil {
		return remoteActor{}, err
	}
	req.Header.Set("Accept", `application/activity+json, application/ld+json; profile="https://www.w3.org/ns/activitystreams"`)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return remoteActor{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return remoteActor{}, fmt.Errorf("remote actor status %d", resp.StatusCode)
	}
	var actor remoteActor
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&actor); err != nil {
		return remoteActor{}, err
	}
	return actor, nil
}

func safeRemoteURL(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Hostname() == "" {
		return false
	}
	host := strings.ToLower(u.Hostname())
	if host == "localhost" || strings.HasSuffix(host, ".localhost") {
		return false
	}
	if ip := net.ParseIP(host); ip != nil {
		return !ip.IsLoopback() && !ip.IsPrivate() && !ip.IsUnspecified()
	}
	return true
}

func sameActivityPubObject(value any, expected string) bool {
	switch object := value.(type) {
	case string:
		return object == expected
	case map[string]any:
		id, _ := object["id"].(string)
		return id == expected
	default:
		return false
	}
}

func activitypubActorID(baseURL, username string) string {
	return strings.TrimRight(baseURL, "/") + "/ap/users/" + url.PathEscape(username)
}

func randomID() string {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return strconv.FormatInt(time.Now().UnixNano(), 36)
	}
	return base64.RawURLEncoding.EncodeToString(raw[:])
}

func activitypubOutbox(baseURL string, posts []store.Post) map[string]any {
	items := make([]any, 0, len(posts))
	for _, post := range posts {
		items = append(items, activitypub.Note(baseURL, post))
	}
	return map[string]any{
		"@context":     "https://www.w3.org/ns/activitystreams",
		"type":         "OrderedCollection",
		"totalItems":   len(items),
		"orderedItems": items,
	}
}
