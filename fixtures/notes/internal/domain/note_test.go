// SPDX-License-Identifier: MPL-2.0
package domain_test

import (
	"crypto/rand"
	"example.com/achrix-notes/internal/domain"
	"strings"
	"testing"
)

func TestNoteInvariants(t *testing.T) {
	for _, v := range []string{"", "  \t\n", "\u00a0\u2003\u3000", strings.Repeat("ا", 201), "text\x00", string([]byte{0xff})} {
		if domain.ValidateText(v) == nil {
			t.Fatal("invalid text accepted")
		}
	}
	for _, v := range []string{"یادداشت ساده", strings.Repeat("ا", 200)} {
		if err := domain.ValidateText(v); err != nil {
			t.Fatal(err)
		}
	}
	if err := domain.ValidateID(rand.Text()); err != nil {
		t.Fatal(err)
	}
	if domain.ValidateID("database-sequence-1") == nil {
		t.Fatal("non-opaque ID accepted")
	}
}
