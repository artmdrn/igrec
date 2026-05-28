package main

import (
	"fmt"
	"time"

	"igrec.net/igrec/internal/app"
	emailpkg "igrec.net/igrec/internal/email"
	"igrec.net/igrec/internal/store"
)

func sendDailyEmails(cfg app.Config, db *store.DB) (int, error) {
	sentOn := time.Now().UTC().Format(time.DateOnly)
	candidates, err := db.DailyEmailCandidates(sentOn, 500)
	if err != nil {
		return 0, err
	}

	sent := 0
	for _, candidate := range candidates {
		unsubscribe := fmt.Sprintf("%s/email/unsubscribe?token=%s", cfg.BaseURL, app.EmailToken(cfg, candidate.User))
		body := emailpkg.DailyPrompt("", "", candidate.SentCount == 0, unsubscribe)
		if candidate.Post.Valid {
			post := candidate.Post.V
			body = emailpkg.DailyPrompt(post.Username, post.Word, candidate.SentCount == 0, unsubscribe)
		}
		err := (emailpkg.Resend{
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
