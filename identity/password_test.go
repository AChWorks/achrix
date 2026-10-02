// SPDX-License-Identifier: MPL-2.0
package identity

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestPasswordPolicyAndRehash(t *testing.T) {
	h, err := newPasswords(DefaultPasswordPolicy(), 1)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	hash, err := h.hash(ctx, "a long test password")
	if err != nil {
		t.Fatal(err)
	}
	p, err := decodePassword(hash)
	if err != nil || p.Memory != 19*1024 || p.Iterations != 2 || p.Parallelism != 1 {
		t.Fatal("PHC metadata not preserved")
	}
	if ok, rehash, err := h.verify(ctx, "a long test password", hash); !ok || rehash || err != nil {
		t.Fatalf("valid verification: %t %t %v", ok, rehash, err)
	}
	if ok, _, err := h.verify(ctx, "different long password", hash); ok || err != nil {
		t.Fatal("wrong password accepted")
	}
	stronger, err := newPasswords(PasswordPolicy{19 * 1024, 3, 1}, 1)
	if err != nil {
		t.Fatal(err)
	}
	if ok, rehash, err := stronger.verify(ctx, "a long test password", hash); !ok || !rehash || err != nil {
		t.Fatal("policy advance not detected")
	}
	updated, err := stronger.hash(ctx, "a long test password")
	if err != nil {
		t.Fatal(err)
	}
	if ok, rehash, err := h.verify(ctx, "a long test password", updated); !ok || rehash || err != nil {
		t.Fatal("stronger credential must not downgrade")
	}
	for _, value := range []string{"", "short", strings.Repeat("x", 1025), string([]byte{255})} {
		if _, err = h.hash(ctx, value); !errors.Is(err, ErrInvalid) {
			t.Fatal("password bound", err)
		}
	}
}
func TestPasswordUntrustedBounds(t *testing.T) {
	for _, hash := range []string{"", strings.Repeat("x", 257), "$argon2id$v=19$m=4294967295,t=2,p=1$c29tZQ$YWJj", "$argon2id$v=19$m=19456,t=0,p=1$c29tZQ$YWJj", "$argon2id$v=19$m=19456,t=2,p=0$c29tZQ$YWJj", "$argon2i$v=19$m=19456,t=2,p=1$c29tZQ$YWJj"} {
		if _, err := decodePassword(hash); err == nil {
			t.Fatal("unsafe PHC accepted")
		}
	}
	for _, policy := range []PasswordPolicy{{1, 2, 1}, {19 * 1024, 0, 1}, {19 * 1024, 2, 0}, {65 * 1024, 2, 1}, {19 * 1024, 5, 1}} {
		if _, err := newPasswords(policy, 1); !errors.Is(err, ErrConfiguration) {
			t.Fatal("unsafe policy accepted")
		}
	}
	h, err := newPasswords(DefaultPasswordPolicy(), 1)
	if err != nil {
		t.Fatal(err)
	}
	h.slots <- struct{}{}
	if _, err = h.hash(context.Background(), "a long test password"); !errors.Is(err, ErrLimited) {
		t.Fatal("hash admission queued")
	}
	<-h.slots
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err = h.hash(ctx, "a long test password"); !errors.Is(err, context.Canceled) {
		t.Fatal("cancellation ignored")
	}
}
func TestSessionTokenAndFormatting(t *testing.T) {
	s, record := newSession("opaque-account", 1, time.Now().Add(time.Hour))
	if len(s.Token) != 43 || len(record.tokenHash) != 32 || s.Token == s.CSRF || !csrfMatches(record.csrfHash, s.CSRF) {
		t.Fatal("invalid session material")
	}
	if csrfMatches(record.csrfHash, s.Token) {
		t.Fatal("token accepted as CSRF")
	}
	if strings.Contains(s.String(), s.Token) || strings.Contains(s.GoString(), s.CSRF) {
		t.Fatal("secret formatting")
	}
	for _, value := range []string{"", s.Token + "x", "http://example.test/?session=" + s.Token, strings.Repeat("!", 43)} {
		if _, err := tokenHash(value); !errors.Is(err, ErrAuthentication) {
			t.Fatal("token syntax accepted")
		}
	}
}
func BenchmarkPasswordHash(b *testing.B) {
	h, err := newPasswords(DefaultPasswordPolicy(), 1)
	if err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		if _, err = h.hash(context.Background(), "a long test password"); err != nil {
			b.Fatal(err)
		}
	}
}
