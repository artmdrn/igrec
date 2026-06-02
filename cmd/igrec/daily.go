package main

import (
	"fmt"
	"log"
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
		unsubscribeToken, err := db.UnsubscribeTokenForUser(candidate.User.ID, app.NewShortToken)
		if err != nil {
			return sent, err
		}
		unsubscribe := fmt.Sprintf("%s/u/%s", cfg.BaseURL, unsubscribeToken)
		body := emailpkg.DailyPrompt("", "", candidate.SentCount == 0, unsubscribe)
		if candidate.Post.Valid {
			post := candidate.Post.V
			body = emailpkg.DailyPrompt(post.Username, post.Word, candidate.SentCount == 0, unsubscribe)
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
