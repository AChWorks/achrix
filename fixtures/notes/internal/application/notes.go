// SPDX-License-Identifier: MPL-2.0
// Package application owns one authorized behavior shared by direct/HTTP calls.
package application

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"example.com/achrix-notes/internal/domain"
	"github.com/AChWorks/achrix"
)

const Create = "example.notes.create"
const Read = "example.notes.read"

// Repository is a consumer-owned persistence port with domain types only.
type Repository interface {
	Save(context.Context, domain.Note) error
	Find(context.Context, string) (domain.Note, error)
}

type Service struct {
	app        *achrix.Application
	repository Repository
	logger     *slog.Logger
	diagnostic func(context.Context, string, string, error)
}

func New(app *achrix.Application, r Repository, l *slog.Logger) *Service {
	return NewWithDiagnostics(app, r, l, nil)
}

// NewWithDiagnostics retains unexpected repository causes at an explicitly
// protected product boundary. The callback owns redaction and diagnostic export.
func NewWithDiagnostics(app *achrix.Application, r Repository, l *slog.Logger, diagnostic func(context.Context, string, string, error)) *Service {
	if l == nil {
		l = slog.Default()
	}
	return &Service{app: app, repository: r, logger: l, diagnostic: diagnostic}
}

// Create has no retry: an interrupted database write can have an unknown outcome.
// This fixture has no external effects or automatic replay.
func (s *Service) Create(parent context.Context, p achrix.Principal, text string) (domain.Note, error) {
	ctx, cancel := context.WithTimeout(parent, time.Second)
	defer cancel()
	if err := s.app.Authorize(ctx, p, Create, ""); err != nil {
		return domain.Note{}, err
	}
	if err := domain.ValidateText(text); err != nil {
		return domain.Note{}, err
	}
	n := domain.Note{ID: rand.Text(), Text: text, CreatedAt: time.Now().UTC()}
	if err := s.repository.Save(ctx, n); err != nil {
		// Keep the persistence owner's truthful classification; a generic timeout
		// or unavailable result must never be upgraded to permission to replay.
		switch {
		case errors.Is(err, domain.ErrNotExecuted):
			return domain.Note{}, domain.ErrNotExecuted
		case errors.Is(err, domain.ErrNotCommitted):
			return domain.Note{}, domain.ErrNotCommitted
		case errors.Is(err, domain.ErrUnknownOutcome):
			return domain.Note{ID: n.ID}, domain.ErrUnknownOutcome
		default:
			if s.diagnostic != nil {
				s.diagnostic(ctx, Create, "repository_save", err)
			}
			s.logger.ErrorContext(ctx, "note write outcome unknown", "component", "notes.application", "operation", Create, "phase", "repository_save", "reason", "unclassified_repository_failure", "cause_type", fmt.Sprintf("%T", err), "outcome", "unknown")
			return domain.Note{ID: n.ID}, domain.ErrUnknownOutcome
		}
	}
	return n, nil
}

func (s *Service) Read(parent context.Context, p achrix.Principal, id string) (domain.Note, error) {
	ctx, cancel := context.WithTimeout(parent, time.Second)
	defer cancel()
	// Bound the opaque reference before policy work; syntax reveals no existence
	// and performs no authorization-sensitive read.
	if err := domain.ValidateID(id); err != nil {
		return domain.Note{}, err
	}
	if err := s.app.Authorize(ctx, p, Read, id); err != nil {
		return domain.Note{}, err
	}
	n, err := s.repository.Find(ctx, id)
	return n, err
}

// Policy is fixture-local, not a shared account/role module. Authentication maps
// the product's two runtime tokens to these two principals; no client grants rights.
func Policy(_ context.Context, p achrix.Principal, c, r string) error {
	if (p == "writer" && (c == Create || c == Read)) || (p == "reader" && c == Read) {
		return nil
	}
	return achrix.ErrDenied
}
