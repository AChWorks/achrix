// SPDX-License-Identifier: MPL-2.0
// Package domain owns the proving consumer's bounded immutable note semantics.
package domain

import (
	"errors"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"
)

var (
	ErrInvalid     = errors.New("invalid note")
	ErrNotFound    = errors.New("note not found")
	ErrUnavailable = errors.New("notes unavailable")
)

type Note struct {
	ID        string    `json:"id"`
	Text      string    `json:"text"`
	CreatedAt time.Time `json:"created_at"`
}

func ValidateText(text string) error {
	if !utf8.ValidString(text) || strings.ContainsRune(text, 0) || strings.TrimSpace(text) == "" || utf8.RuneCountInString(text) > 200 {
		return ErrInvalid
	}
	return nil
}

var opaqueID = regexp.MustCompile(`^[A-Z2-7]{26,64}$`)

func ValidateID(id string) error {
	if len(id) < 26 || len(id) > 64 || !opaqueID.MatchString(id) {
		return ErrInvalid
	}
	return nil
}
