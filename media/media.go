// SPDX-License-Identifier: MPL-2.0
// Package media owns an authorized private file library. Products own
// permissions, content associations and ingress; assets have no public URL.
package media

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"io"
	"regexp"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/AChWorks/achrix"
)

const (
	Create               = "achrix.media.create"
	List                 = "achrix.media.list"
	Read                 = "achrix.media.read"
	Delete               = "achrix.media.delete"
	Reconcile            = "achrix.media.reconcile"
	LibraryTarget        = "library"
	MaxUploadBytes int64 = 10 << 20
	MaxDimension         = 4096
	MaxPixels            = 8 << 20
	maxRevision    int64 = 1<<63 - 1
)

var (
	ErrConfiguration  = errors.New("invalid media configuration")
	ErrInput          = errors.New("invalid media input")
	ErrUnavailable    = errors.New("media unavailable")
	ErrNotFound       = errors.New("media asset not found")
	ErrConflict       = errors.New("media precondition conflict")
	ErrLimited        = errors.New("media capacity exceeded")
	ErrUnknownOutcome = errors.New("media outcome requires reconciliation")
)

// Config identifies an existing product-owned private 0700 directory outside any
// webroot. Now is a trusted concurrent-safe UTC time source, not an attestation.
type Config struct {
	// MaxConns bounds this instance's pool. Zero uses four; negatives are invalid.
	MaxConns int32
	// MaxOperations bounds active owned leases. Zero uses four; negatives are invalid.
	// The separate expensive-validation budget remains two.
	MaxOperations int
	StorageRoot   string
	// AllowedMIMEs selects a finite supported upload profile. Nil preserves
	// PNG/JPEG-only behavior; CommonMIMEs opts into the common profile. An
	// explicit empty selection, duplicates or unsupported entries are invalid.
	// SVG requires explicit image/svg+xml selection, separate from CommonMIMEs.
	// Construction copies the selection; it does not revoke retained reads.
	AllowedMIMEs []string
	Now          func() time.Time
}
type Asset struct {
	ID            string
	Filename      string
	MIME          string
	Size          int64
	Width, Height int
	SHA256        string
	Revision      int64
	State         string
	CreatedAt     time.Time
}
type Page struct {
	Assets     []Asset
	NextCursor string
}
type ReconcileResult struct{ Processed, Busy int }
type Service struct {
	app    *achrix.Application
	module *Module
}

func NewService(app *achrix.Application, module *Module) (*Service, error) {
	if app == nil || module == nil {
		return nil, ErrConfiguration
	}
	return &Service{app: app, module: module}, nil
}

var assetID = regexp.MustCompile(`^[A-Z2-7]{26}$`)

func validID(s string) bool { return len(s) == 26 && assetID.MatchString(s) }
func validFilename(s string) bool {
	if s == "" || len(s) > 256 || !utf8.ValidString(s) || utf8.RuneCountInString(s) > 120 || strings.TrimSpace(s) != s {
		return false
	}
	for _, r := range s {
		if unicode.IsControl(r) || r == 0x061c || r == 0x200e || r == 0x200f || (r >= 0x202a && r <= 0x202e || r >= 0x2066 && r <= 0x2069) || r == '/' || r == '\\' {
			return false
		}
	}
	return s != "." && s != ".."
}
func deadline(parent context.Context, maximum time.Duration) (context.Context, context.CancelFunc) {
	return context.WithTimeout(parent, maximum)
}

// Create returns the generated ID on any failure after intent insertion begins.
// ErrUnknownOutcome means do not retry Create: use Status and explicit Reconcile.
// Input readers are trusted composition objects and MUST return on their caller's
// deadline/cancellation. This synchronous API cannot interrupt arbitrary Read.
func (s *Service) Create(parent context.Context, actor achrix.Principal, filename string, input io.Reader) (Asset, error) {
	if !validFilename(filename) || input == nil {
		return Asset{}, ErrInput
	}
	ctx, cancel := deadline(parent, 15*time.Second)
	defer cancel()
	if err := s.app.Authorize(ctx, actor, Create, LibraryTarget); err != nil {
		return Asset{}, err
	}
	return s.module.create(ctx, filename, input)
}

// List grants collection metadata discovery; it grants neither byte Read nor Delete.
// Pages use immutable generated IDs as keysets and are not a multi-page snapshot.
func (s *Service) List(parent context.Context, actor achrix.Principal, cursor string, limit int) (Page, error) {
	after, err := decodeCursor(cursor)
	if err != nil || limit < 1 || limit > 100 {
		return Page{}, ErrInput
	}
	ctx, cancel := deadline(parent, time.Second)
	defer cancel()
	if err = s.app.Authorize(ctx, actor, List, LibraryTarget); err != nil {
		return Page{}, err
	}
	return s.module.list(ctx, after, limit)
}

// Status separately authorizes Read on the exact ID and returns durable state.
// Pending/deleting outcomes are not byte-readable; deleted is a minimal tombstone.
func (s *Service) Status(parent context.Context, actor achrix.Principal, id string) (Asset, error) {
	if !validID(id) {
		return Asset{}, ErrInput
	}
	ctx, cancel := deadline(parent, time.Second)
	defer cancel()
	if err := s.app.Authorize(ctx, actor, Read, id); err != nil {
		return Asset{}, err
	}
	return s.module.status(ctx, id)
}

// Read authenticates the stored size/hash before copying original bytes. The
// trusted destination must honor cancellation; products serve attachment with
// exact MIME, nosniff and private/no-store, never static paths or public links.
func (s *Service) Read(parent context.Context, actor achrix.Principal, id string, dst io.Writer) (Asset, error) {
	if !validID(id) || dst == nil {
		return Asset{}, ErrInput
	}
	ctx, cancel := deadline(parent, 15*time.Second)
	defer cancel()
	if err := s.app.Authorize(ctx, actor, Read, id); err != nil {
		return Asset{}, err
	}
	return s.module.read(ctx, id, dst)
}
func (s *Service) Delete(parent context.Context, actor achrix.Principal, id string, expectedRevision int64) error {
	if !validID(id) || expectedRevision < 1 {
		return ErrInput
	}
	ctx, cancel := deadline(parent, 5*time.Second)
	defer cancel()
	if err := s.app.Authorize(ctx, actor, Delete, id); err != nil {
		return err
	}
	return s.module.delete(ctx, id, expectedRevision)
}

// Reconcile is an explicit bounded collection-wide cleanup permission. It aborts
// interrupted pending uploads and completes deletion; it never publishes an
// incomplete asset. A currently locked operation is counted Busy and untouched.
func (s *Service) Reconcile(parent context.Context, actor achrix.Principal, limit int) (ReconcileResult, error) {
	if limit < 1 || limit > 40 {
		return ReconcileResult{}, ErrInput
	}
	ctx, cancel := deadline(parent, 10*time.Second)
	defer cancel()
	if err := s.app.Authorize(ctx, actor, Reconcile, LibraryTarget); err != nil {
		return ReconcileResult{}, err
	}
	return s.module.reconcile(ctx, limit)
}
func encodeCursor(id string) string {
	return base64.RawURLEncoding.EncodeToString(append([]byte{1}, []byte(id)...))
}
func decodeCursor(s string) (string, error) {
	if s == "" {
		return "", nil
	}
	if len(s) != 36 {
		return "", ErrInput
	}
	b, e := base64.RawURLEncoding.DecodeString(s)
	if e != nil || len(b) != 27 || b[0] != 1 || !validID(string(b[1:])) {
		return "", ErrInput
	}
	return string(b[1:]), nil
}
func lockKey(id string) int64 {
	// A collision only serializes unrelated assets; it never changes the addressed ID.
	sum := sha256.Sum256([]byte(id))
	return int64(binary.BigEndian.Uint64(sum[:8]))
}
func newID() string { return rand.Text() }
