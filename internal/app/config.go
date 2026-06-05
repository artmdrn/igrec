package app

import (
	"crypto/ecdh"
	"encoding/base64"
	"errors"
	"fmt"
	"net/mail"
	"net/url"
	"strings"
)

func (c Config) Validate() error {
	if strings.TrimSpace(c.Addr) == "" {
		return errors.New("ADDR is required")
	}
	if strings.TrimSpace(c.DatabaseURL) == "" {
		return errors.New("DATABASE_URL is required")
	}
	if strings.TrimSpace(c.UploadDir) == "" {
		return errors.New("UPLOAD_DIR is required")
	}

	baseURL := strings.TrimSpace(c.BaseURL)
	if baseURL == "" {
		return errors.New("BASE_URL is required")
	}
	parsed, err := url.Parse(baseURL)
	if err != nil || !parsed.IsAbs() || parsed.Host == "" {
		return errors.New("BASE_URL must be an absolute URL")
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return errors.New("BASE_URL must use http or https")
	}

	if _, err := mail.ParseAddress(c.LoginEmailFrom); err != nil {
		return fmt.Errorf("LOGIN_EMAIL_FROM is invalid: %w", err)
	}
	if _, err := mail.ParseAddress(c.DailyEmailFrom); err != nil {
		return fmt.Errorf("DAILY_EMAIL_FROM is invalid: %w", err)
	}

	if isProductionBaseURL(parsed) {
		if strings.TrimSpace(c.AppSecret) == "" {
			return errors.New("APP_SECRET is required for production")
		}
		if strings.TrimSpace(c.ResendAPIKey) == "" {
			return errors.New("RESEND_API_KEY is required for production")
		}
	}
	if err := validateVAPIDKeys(c.VAPIDPublic, c.VAPIDPrivate); err != nil {
		return err
	}

	return nil
}

func validateVAPIDKeys(publicKey, privateKey string) error {
	publicKey = strings.TrimSpace(publicKey)
	privateKey = strings.TrimSpace(privateKey)
	if publicKey == "" && privateKey == "" {
		return nil
	}
	if publicKey == "" || privateKey == "" {
		return errors.New("VAPID_PUBLIC_KEY and VAPID_PRIVATE_KEY must both be set")
	}
	publicRaw, err := base64.RawURLEncoding.DecodeString(publicKey)
	if err != nil {
		return errors.New("VAPID_PUBLIC_KEY must be base64url-encoded")
	}
	privateRaw, err := base64.RawURLEncoding.DecodeString(privateKey)
	if err != nil {
		return errors.New("VAPID_PRIVATE_KEY must be base64url-encoded")
	}
	curve := ecdh.P256()
	privateECDH, err := curve.NewPrivateKey(privateRaw)
	if err != nil {
		return errors.New("VAPID_PRIVATE_KEY must be a valid P-256 private key")
	}
	publicECDH, err := curve.NewPublicKey(publicRaw)
	if err != nil {
		return errors.New("VAPID_PUBLIC_KEY must be a valid uncompressed P-256 public key")
	}
	if publicECDH.Bytes()[0] != 4 {
		return errors.New("VAPID_PUBLIC_KEY must use uncompressed P-256 format")
	}
	if publicKey != base64.RawURLEncoding.EncodeToString(publicECDH.Bytes()) {
		return errors.New("VAPID_PUBLIC_KEY must use uncompressed P-256 format")
	}
	if publicKey != base64.RawURLEncoding.EncodeToString(privateECDH.PublicKey().Bytes()) {
		return errors.New("VAPID keys do not match")
	}
	return nil
}

func isProductionBaseURL(parsed *url.URL) bool {
	host := strings.ToLower(parsed.Hostname())
	switch host {
	case "localhost", "127.0.0.1", "::1":
		return false
	default:
		return true
	}
}
