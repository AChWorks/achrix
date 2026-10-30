// SPDX-License-Identifier: MPL-2.0
// Package domain owns the proving consumer's bounded immutable note semantics.
package domain

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"
)

var (
	ErrInvalid     = errors.New("invalid note")
	ErrNotFound    = errors.New("note not found")
	ErrUnavailable = errors.New("notes unavailable")
	// These canonical errors contain no provider cause. A write error alone does
	// not prove non-execution; an interrupted dispatched write may have committed.
	ErrNotExecuted    = fmt.Errorf("%w: write not executed", ErrUnavailable)
	ErrNotCommitted   = fmt.Errorf("%w: write not committed", ErrUnavailable)
	ErrUnknownOutcome = fmt.Errorf("%w: write outcome unknown", ErrUnavailable)
)

type InputError struct{ Field, Problem string }

func (e *InputError) Error() string { return "invalid note: " + e.Field + " " + e.Problem }
func (e *InputError) Unwrap() error { return ErrInvalid }

type Note struct {
	ID        string    `json:"id"`
	Text      string    `json:"text"`
	CreatedAt time.Time `json:"created_at"`
}

func ValidateText(text string) error {
	if !utf8.ValidString(text) || strings.ContainsRune(text, 0) || strings.TrimSpace(text) == "" || utf8.RuneCountInString(text) > 200 {
		return &InputError{Field: "text", Problem: "must contain 1..200 Unicode characters, non-blank UTF-8, without NUL"}
	}
	return nil
}

var opaqueID = regexp.MustCompile(`^[A-Z2-7]{26,64}$`)

func ValidateID(id string) error {
	if len(id) < 26 || len(id) > 64 || !opaqueID.MatchString(id) {
		return &InputError{Field: "id", Problem: "must be the 26..64 character opaque identifier returned by this service"}
	}
	return nil
}
