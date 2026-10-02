// SPDX-License-Identifier: MPL-2.0
package identity

import (
	"context"
	"errors"
	"fmt"
	"unicode/utf8"

	"github.com/alexedwards/argon2id"
)

// PasswordPolicy uses KiB of memory. Defaults follow the reviewed OWASP
// Argon2id minimum; products must measure before changing the supported bounds.
type PasswordPolicy struct {
	Memory, Iterations uint32
	Parallelism        uint8
}

func DefaultPasswordPolicy() PasswordPolicy { return PasswordPolicy{19 * 1024, 2, 1} }
func (p PasswordPolicy) valid() bool {
	return p.Memory >= 19*1024 && p.Memory <= 64*1024 && p.Iterations >= 2 && p.Iterations <= 4 && p.Parallelism >= 1 && p.Parallelism <= 2
}
func (p PasswordPolicy) params() *argon2id.Params {
	return &argon2id.Params{Memory: p.Memory, Iterations: p.Iterations, Parallelism: p.Parallelism, SaltLength: 16, KeyLength: 32}
}

type passwords struct {
	policy PasswordPolicy
	slots  chan struct{}
	dummy  string
}

func newPasswords(p PasswordPolicy, concurrency int) (*passwords, error) {
	if !p.valid() || concurrency < 1 || concurrency > 2 {
		return nil, ErrConfiguration
	}
	h := &passwords{policy: p, slots: make(chan struct{}, concurrency)}
	var err error
	h.dummy, err = argon2id.CreateHash("unused-dummy-credential", p.params())
	if err != nil {
		return nil, ErrUnavailable
	}
	return h, nil
}

// Admission has no queue or goroutine. The maintained Argon2 implementation is
// synchronous and not interruptible; cancellation is checked before and after
// each bounded hash. At most two admitted hashes retain their bounded resources.
func (h *passwords) admit(ctx context.Context) (func(), error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	select {
	case h.slots <- struct{}{}:
		return func() { <-h.slots }, nil
	default:
		return nil, ErrLimited
	}
}
func validPassword(password string, setting bool) bool {
	n := utf8.RuneCountInString(password)
	return utf8.ValidString(password) && len(password) <= 1024 && n <= 256 && ((!setting && n > 0) || (setting && n >= 15))
}
func (h *passwords) hash(ctx context.Context, password string) (string, error) {
	if !validPassword(password, true) {
		return "", ErrInvalid
	}
	release, err := h.admit(ctx)
	if err != nil {
		return "", err
	}
	defer release()
	encoded, err := argon2id.CreateHash(password, h.policy.params())
	if err != nil {
		return "", ErrUnavailable
	}
	if err = ctx.Err(); err != nil {
		return "", err
	}
	return encoded, nil
}

// Stored PHC records are also untrusted input. Bound their length and algorithm,
// version, cost, salt and key before allowing the library to allocate/hash.
func decodePassword(encoded string) (*argon2id.Params, error) {
	if len(encoded) > 256 {
		return nil, ErrUnavailable
	}
	p, _, _, err := argon2id.DecodeHash(encoded)
	if err != nil || p == nil {
		return nil, ErrUnavailable
	}
	policy := PasswordPolicy{p.Memory, p.Iterations, p.Parallelism}
	if !policy.valid() || p.SaltLength != 16 || p.KeyLength != 32 {
		return nil, ErrUnavailable
	}
	// Enforce one canonical encoding; the library decoder accepts numeric suffixes.
	// Salt/hash are decoded by the library, while the metadata prefix is exact.
	prefix := fmt.Sprintf("$argon2id$v=19$m=%d,t=%d,p=%d$", p.Memory, p.Iterations, p.Parallelism)
	if len(encoded) < len(prefix) || encoded[:len(prefix)] != prefix {
		return nil, ErrUnavailable
	}
	return p, nil
}
func (h *passwords) verify(ctx context.Context, password, encoded string) (bool, bool, error) {
	if !validPassword(password, false) {
		return false, false, ErrAuthentication
	}
	p, err := decodePassword(encoded)
	if err != nil {
		return false, false, err
	}
	release, err := h.admit(ctx)
	if err != nil {
		return false, false, err
	}
	defer release()
	ok, err := argon2id.ComparePasswordAndHash(password, encoded)
	if err != nil {
		return false, false, ErrUnavailable
	}
	if err = ctx.Err(); err != nil {
		return false, false, err
	}
	// Never silently weaken a stronger record when one configured dimension grows.
	rehash := ok && p.Memory <= h.policy.Memory && p.Iterations <= h.policy.Iterations && p.Parallelism <= h.policy.Parallelism && (p.Memory < h.policy.Memory || p.Iterations < h.policy.Iterations || p.Parallelism < h.policy.Parallelism)
	return ok, rehash, nil
}
func authenticationError(err error) error {
	if errors.Is(err, ErrInvalid) {
		return ErrAuthentication
	}
	return err
}
