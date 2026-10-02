// SPDX-License-Identifier: MPL-2.0
package media

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"image"
	"image/jpeg"
	"image/png"
	"io"
	"os"
	"syscall"
)

type localStorage struct {
	root      *os.Root
	directory *os.File
}

func openStorage(path string) (*localStorage, error) {
	info, err := os.Lstat(path)
	if err != nil || !info.IsDir() || info.Mode().Perm() != 0700 || info.Mode()&os.ModeSymlink != 0 {
		return nil, ErrConfiguration
	}
	root, err := os.OpenRoot(path)
	if err != nil {
		return nil, ErrConfiguration
	}
	directory, err := root.Open(".")
	if err != nil {
		_ = root.Close()
		return nil, ErrConfiguration
	}
	st := &localStorage{root, directory}
	if err = st.check(); err != nil {
		_ = st.close()
		return nil, err
	}
	return st, nil
}
func (s *localStorage) check() error {
	info, err := s.directory.Stat()
	if err != nil || !info.IsDir() || info.Mode().Perm() != 0700 {
		return ErrConfiguration
	}
	return s.directory.Sync()
}
func (s *localStorage) close() error { return errors.Join(s.directory.Close(), s.root.Close()) }
func (s *localStorage) sync() error  { return s.directory.Sync() }

// Independent open descriptions make flock protect this storage target across
// instances even if a PostgreSQL session is lost during synchronous filesystem
// work. Upload/read share; delete/reconciliation fail Busy while any transfer is
// active. This small Linux adapter deliberately trades delete concurrency for a
// clear cross-store safety boundary. No lock files or global Go registry exist.
func (s *localStorage) lock(shared bool) (func(), error) {
	f, err := s.root.Open(".")
	if err != nil {
		return nil, err
	}
	mode := syscall.LOCK_EX | syscall.LOCK_NB
	if shared {
		mode = syscall.LOCK_SH | syscall.LOCK_NB
	}
	if err = syscall.Flock(int(f.Fd()), mode); err != nil {
		_ = f.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return nil, ErrConflict
		}
		return nil, err
	}
	return func() { _ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN); _ = f.Close() }, nil
}

// Reusing an orphaned name would make reconciliation ambiguous. Check both
// names under the acknowledged PostgreSQL asset lock before creating intent.
// Storage is exclusively product-owned; external writers are unsupported.
func (s *localStorage) unused(id string) error {
	if !validID(id) {
		return ErrInput
	}
	for _, name := range []string{id, id + ".upload"} {
		if _, err := s.root.Lstat(name); !errors.Is(err, os.ErrNotExist) {
			if err == nil {
				return ErrUnavailable
			}
			return err
		}
	}
	return nil
}

func (s *localStorage) open(id string) (*os.File, error) {
	if !validID(id) {
		return nil, ErrInput
	}
	info, err := s.root.Lstat(id)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Mode().Perm() != 0600 {
		return nil, ErrUnavailable
	}
	f, err := s.root.OpenFile(id, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return nil, err
	}
	got, err := f.Stat()
	if err != nil || !os.SameFile(info, got) || !got.Mode().IsRegular() || got.Mode().Perm() != 0600 {
		_ = f.Close()
		return nil, ErrUnavailable
	}
	stat, ok := got.Sys().(*syscall.Stat_t)
	if !ok || stat.Nlink != 1 {
		_ = f.Close()
		return nil, ErrUnavailable
	}
	return f, nil
}

type contextReader struct {
	ctx context.Context
	r   io.Reader
}

func (r contextReader) Read(b []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.r.Read(b)
}

type contextWriter struct {
	ctx context.Context
	w   io.Writer
}

func (w contextWriter) Write(b []byte) (int, error) {
	if err := w.ctx.Err(); err != nil {
		return 0, err
	}
	return w.w.Write(b)
}

// Copies always use a fixed buffer and do not invoke vendor ReaderFrom/WriterTo
// fast paths which could bypass context checks or retain arbitrary buffers.
func copyBounded(ctx context.Context, w io.Writer, r io.Reader, max int64) (int64, error) {
	buffer := make([]byte, 32<<10)
	return io.CopyBuffer(contextWriter{ctx, w}, io.LimitReader(contextReader{ctx, r}, max), buffer)
}
func (m *Module) validate(ctx context.Context, f *os.File) (string, int, int, error) {
	if err := ctx.Err(); err != nil {
		return "", 0, 0, err
	}
	select {
	case m.decoders <- struct{}{}:
		defer func() { <-m.decoders }()
	default:
		return "", 0, 0, ErrLimited
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return "", 0, 0, err
	}
	// Only selected decoders are called: another imported package cannot add an
	// active/complex format through the process-global image registry.
	header := make([]byte, 8)
	if _, err := io.ReadFull(contextReader{ctx, f}, header); err != nil {
		if ctx.Err() != nil {
			return "", 0, 0, ctx.Err()
		}
		return "", 0, 0, ErrInput
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return "", 0, 0, err
	}
	mime := ""
	var config image.Config
	var err error
	switch {
	case string(header) == "\x89PNG\r\n\x1a\n":
		mime = "image/png"
		config, err = png.DecodeConfig(contextReader{ctx, f})
	case header[0] == 0xff && header[1] == 0xd8 && header[2] == 0xff:
		mime = "image/jpeg"
		config, err = jpeg.DecodeConfig(contextReader{ctx, f})
	default:
		return "", 0, 0, ErrInput
	}
	if err != nil || config.Width < 1 || config.Height < 1 || config.Width > MaxDimension || config.Height > MaxDimension || int64(config.Width)*int64(config.Height) > MaxPixels {
		if ctx.Err() != nil {
			return "", 0, 0, ctx.Err()
		}
		return "", 0, 0, ErrInput
	}
	if _, err = f.Seek(0, io.SeekStart); err != nil {
		return "", 0, 0, err
	}
	// Full decode rejects malformed/truncated pixel streams. Go decoders are
	// synchronous: bounded compressed input, pixels and two slots are the CPU/
	// allocation envelope; cancellation is checked at every underlying Read.
	var decoded image.Image
	if mime == "image/png" {
		decoded, err = png.Decode(contextReader{ctx, f})
	} else {
		decoded, err = jpeg.Decode(contextReader{ctx, f})
	}
	if err != nil {
		if ctx.Err() != nil {
			return "", 0, 0, ctx.Err()
		}
		return "", 0, 0, ErrInput
	}
	if decoded.Bounds().Dx() != config.Width || decoded.Bounds().Dy() != config.Height {
		return "", 0, 0, ErrInput
	}
	if err = ctx.Err(); err != nil {
		return "", 0, 0, err
	}
	return mime, config.Width, config.Height, nil
}
func (s *localStorage) write(ctx context.Context, m *Module, a Asset, input io.Reader) (Asset, error) {
	f, err := s.root.OpenFile(a.ID+".upload", os.O_CREATE|os.O_EXCL|os.O_RDWR|syscall.O_NOFOLLOW, 0600)
	if err != nil {
		return a, err
	}
	defer func() { _ = f.Close() }()
	h := sha256.New()
	n, err := copyBounded(ctx, io.MultiWriter(f, h), input, MaxUploadBytes+1)
	if err != nil {
		return a, err
	}
	if n == 0 || n > MaxUploadBytes {
		return a, ErrInput
	}
	a.Size = n
	a.SHA256 = hex.EncodeToString(h.Sum(nil))
	a.MIME, a.Width, a.Height, err = m.validate(ctx, f)
	if err != nil {
		return a, err
	}
	if err = ctx.Err(); err != nil {
		return a, err
	}
	if err = f.Sync(); err != nil {
		return a, err
	}
	if err = f.Close(); err != nil {
		return a, err
	}
	if err = s.root.Link(a.ID+".upload", a.ID); err != nil {
		if errors.Is(err, os.ErrExist) {
			return a, ErrUnknownOutcome
		}
		return a, err
	}
	if err = s.root.Remove(a.ID + ".upload"); err != nil {
		return a, err
	}
	if err = s.sync(); err != nil {
		return a, err
	}
	return a, ctx.Err()
}
func (s *localStorage) remove(id string) error {
	if !validID(id) {
		return ErrInput
	}
	for _, name := range []string{id + ".upload", id} {
		if err := s.root.Remove(name); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	return s.sync()
}
