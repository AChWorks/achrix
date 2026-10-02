// SPDX-License-Identifier: MPL-2.0
// Package application owns one authorized behavior shared by direct/HTTP calls.
package application

import (
	"context"
	"crypto/rand"
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
}

func New(app *achrix.Application, r Repository, l *slog.Logger) *Service { return &Service{app, r, l} }

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
		s.logger.ErrorContext(ctx, "note write failed", "component", "notes.application", "operation", Create, "reason", "persistence_failure")
		return domain.Note{}, domain.ErrUnavailable
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
