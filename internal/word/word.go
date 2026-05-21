package word

import (
	"errors"
	"strings"
	"unicode"
	"unicode/utf8"
)

var (
	ErrEmpty       = errors.New("word is required")
	ErrTooLong     = errors.New("word must be 50 characters or fewer")
	ErrWhitespace  = errors.New("word cannot contain spaces")
	ErrPunctuation = errors.New("word cannot contain punctuation")
)

func Normalize(input string) (string, error) {
	value := strings.TrimSpace(input)
	if value == "" {
		return "", ErrEmpty
	}
	if utf8.RuneCountInString(value) > 50 {
		return "", ErrTooLong
	}
	for _, r := range value {
		if unicode.IsSpace(r) {
			return "", ErrWhitespace
		}
		if unicode.IsPunct(r) || unicode.IsSymbol(r) {
			return "", ErrPunctuation
		}
	}
	return value, nil
}
