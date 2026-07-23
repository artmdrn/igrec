package main

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/mail"
	"time"

	webpush "github.com/SherClockHolmes/webpush-go"

	"igrec.net/igrec/internal/app"
	emailpkg "igrec.net/igrec/internal/email"
	"igrec.net/igrec/internal/store"
)

var sendWebPushNotification = webpush.SendNotification

func sendDailyEmails(cfg app.Config, db *store.DB) (int, error) {
	sentOn := time.Now().UTC().Format(time.DateOnly)
	candidates, err := db.DailyEmailCandidates(sentOn, 500)
	if err != nil {
		return 0, err
	}

	sent := 0
	for _, candidate := range candidates {
		unsubscribeToken, err := db.UnsubscribeTokenForUser(candidate.User.ID, app.NewShortToken)
		if err != nil {
			return sent, err
		}
		unsubscribe := fmt.Sprintf("%s/u/%s", cfg.BaseURL, unsubscribeToken)
		onThisDayWord := ""
		onThisDay, err := db.PostsOnThisDay(candidate.User.ID, time.Now())
		if err != nil {
			return sent, err
		}
		if len(onThisDay) > 0 {
			onThisDayWord = onThisDay[0].Word
		}
		body := emailpkg.DailyPrompt("", "", candidate.SentCount == 0, unsubscribe, onThisDayWord)
		if candidate.Post.Valid {
			post := candidate.Post.V
			body = emailpkg.DailyPrompt(post.Username, post.Word, candidate.SentCount == 0, unsubscribe, onThisDayWord)
		}
		err = (emailpkg.Resend{
			APIKey:  cfg.ResendAPIKey,
			From:    cfg.DailyEmailFrom,
			ReplyTo: fmt.Sprintf("Y <_+%s@igrec.net>", candidate.User.Username),
			Headers: map[string]string{
				"List-Unsubscribe":      "<" + unsubscribe + ">",
				"List-Unsubscribe-Post": "List-Unsubscribe=One-Click",
			},
		}).SendPlain(candidate.User.Email, ">", body)
		if err != nil {
			return sent, fmt.Errorf("send daily email to %s: %w", candidate.User.Email, err)
		}
		if err := db.MarkDailyEmailSent(candidate.User.ID, sentOn); err != nil {
			return sent, err
		}
		sent++
	}
	return sent, nil
}

func sendDailyPushes(cfg app.Config, db *store.DB, client webpush.HTTPClient) (int, int, error) {
	if cfg.VAPIDPublic == "" || cfg.VAPIDPrivate == "" {
		return 0, 0, nil
	}

	sentOn := time.Now().UTC().Format(time.DateOnly)
	candidates, err := db.DailyPushCandidates(sentOn, 500)
	if err != nil {
		return 0, 0, err
	}

	options := &webpush.Options{
		HTTPClient:      client,
		Subscriber:      pushSubscriber(cfg),
		TTL:             3600,
		Topic:           "daily-prompt",
		Urgency:         webpush.UrgencyHigh,
		VAPIDPublicKey:  cfg.VAPIDPublic,
		VAPIDPrivateKey: cfg.VAPIDPrivate,
	}

	sentUsers := 0
	sentSubscriptions := 0
	for _, candidate := range candidates {
		subscriptions, err := db.PushSubscriptionsByUser(candidate.User.ID)
		if err != nil {
			return sentUsers, sentSubscriptions, err
		}
		payload, err := dailyPushPayload(candidate)
		if err != nil {
			return sentUsers, sentSubscriptions, err
		}

		delivered := 0
		for _, subscription := range subscriptions {
			resp, err := sendWebPushNotification(payload, &webpush.Subscription{
				Endpoint: subscription.Endpoint,
				Keys: webpush.Keys{
					Auth:   subscription.Auth,
					P256dh: subscription.P256DH,
				},
			}, options)
			if err != nil {
				return sentUsers, sentSubscriptions, fmt.Errorf("send daily push to @%s endpoint %s: %w", candidate.User.Username, subscription.Endpoint, err)
			}
			if resp == nil {
				continue
			}
			_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
			resp.Body.Close()

			switch resp.StatusCode {
			case http.StatusCreated, http.StatusAccepted:
				delivered++
				sentSubscriptions++
			case http.StatusGone, http.StatusNotFound:
				if err := db.DeletePushSubscription(candidate.User.ID, subscription.Endpoint); err != nil {
					return sentUsers, sentSubscriptions, err
				}
			default:
				return sentUsers, sentSubscriptions, fmt.Errorf("send daily push to @%s endpoint %s: unexpected status %s", candidate.User.Username, subscription.Endpoint, resp.Status)
			}
		}
		if delivered == 0 {
			continue
		}
		if err := db.MarkDailyPushSent(candidate.User.ID, sentOn); err != nil {
			return sentUsers, sentSubscriptions, err
		}
		sentUsers++
	}
	return sentUsers, sentSubscriptions, nil
}

func dailyPushPayload(candidate store.DailyPushCandidate) ([]byte, error) {
	body := ">_"
	if candidate.Post.Valid {
		post := candidate.Post.V
		body = fmt.Sprintf("@%s said: %s", post.Username, post.Word)
	}
	if candidate.SentCount == 0 {
		body += "\nreply with one word."
	}
	return json.Marshal(map[string]any{
		"title": "igrec",
		"body":  body,
		"url":   app.NotificationURL,
		"tag":   "daily-prompt",
	})
}

func pushSubscriber(cfg app.Config) string {
	if address, err := mail.ParseAddress(cfg.DailyEmailFrom); err == nil && address.Address != "" {
		return address.Address
	}
	return cfg.BaseURL
}

func printDailyEmailStatus(db *store.DB) error {
	sentOn := time.Now().UTC().Format(time.DateOnly)
	candidates, err := db.DailyEmailCandidates(sentOn, 1000)
	if err != nil {
		return err
	}
	withPost := 0
	first := 0
	for _, candidate := range candidates {
		if candidate.Post.Valid {
			withPost++
		}
		if candidate.SentCount == 0 {
			first++
		}
	}
	log.Printf("daily email pending=%d with_word=%d first_email=%d sent_on=%s", len(candidates), withPost, first, sentOn)
	for _, candidate := range candidates {
		word := ""
		if candidate.Post.Valid {
			word = " @" + candidate.Post.V.Username + " " + candidate.Post.V.Word
		}
		log.Printf("daily email candidate user=@%s email=%s sent_count=%d%s", candidate.User.Username, candidate.User.Email, candidate.SentCount, word)
	}
	return nil
}
