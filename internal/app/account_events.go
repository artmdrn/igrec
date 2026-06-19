package app

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/mail"

	webpush "github.com/SherClockHolmes/webpush-go"

	"igrec.net/igrec/internal/store"
)

var sendBrowserPushNotification = webpush.SendNotification

type pushMessage struct {
	Title string
	Body  string
	URL   string
	Tag   string
}

func (a *App) notifyFollowedUser(followed, follower store.User) {
	if !a.pushConfigured() {
		return
	}
	if err := a.sendPushToUser(followed.ID, pushMessage{
		Title: "igrec",
		Body:  "@" + follower.Username + " followed you",
		URL:   "/@" + follower.Username,
		Tag:   "follow-" + follower.Username,
	}); err != nil {
		log.Printf("follow push failed followed=@%s follower=@%s err=%v", followed.Username, follower.Username, err)
	}
}

func (a *App) notifyInviterInviteUsed(inviter, joined store.User) {
	if !a.pushConfigured() {
		return
	}
	if err := a.sendPushToUser(inviter.ID, pushMessage{
		Title: "igrec",
		Body:  "@" + joined.Username + " joined with your invite",
		URL:   "/@" + joined.Username,
		Tag:   "invite-used-" + joined.Username,
	}); err != nil {
		log.Printf("invite push failed inviter=@%s joined=@%s err=%v", inviter.Username, joined.Username, err)
	}
}

func (a *App) sendPushToUser(userID int64, msg pushMessage) error {
	subscriptions, err := a.db.PushSubscriptionsByUser(userID)
	if err != nil {
		return err
	}
	if len(subscriptions) == 0 {
		return nil
	}
	payload, err := json.Marshal(map[string]any{
		"title": msg.Title,
		"body":  msg.Body,
		"url":   msg.URL,
		"tag":   msg.Tag,
	})
	if err != nil {
		return err
	}
	options := &webpush.Options{
		Subscriber:      pushSubscriber(a.cfg),
		TTL:             300,
		Topic:           msg.Tag,
		Urgency:         webpush.UrgencyNormal,
		VAPIDPublicKey:  a.cfg.VAPIDPublic,
		VAPIDPrivateKey: a.cfg.VAPIDPrivate,
	}
	for _, subscription := range subscriptions {
		resp, err := sendBrowserPushNotification(payload, &webpush.Subscription{
			Endpoint: subscription.Endpoint,
			Keys: webpush.Keys{
				Auth:   subscription.Auth,
				P256dh: subscription.P256DH,
			},
		}, options)
		if err != nil {
			return fmt.Errorf("send push endpoint %s: %w", subscription.Endpoint, err)
		}
		if resp == nil {
			continue
		}
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
		resp.Body.Close()

		switch resp.StatusCode {
		case http.StatusCreated, http.StatusAccepted:
		case http.StatusGone, http.StatusNotFound:
			if err := a.db.DeletePushSubscription(userID, subscription.Endpoint); err != nil {
				return err
			}
		default:
			return fmt.Errorf("send push endpoint %s: unexpected status %s", subscription.Endpoint, resp.Status)
		}
	}
	return nil
}

func pushSubscriber(cfg Config) string {
	if address, err := mail.ParseAddress(cfg.DailyEmailFrom); err == nil && address.Address != "" {
		return address.Address
	}
	return cfg.BaseURL
}
