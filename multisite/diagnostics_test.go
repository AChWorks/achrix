// SPDX-License-Identifier: MPL-2.0
package multisite_test

import (
	"errors"
	"github.com/AChWorks/achrix"
	"github.com/AChWorks/achrix/multisite"
	"strings"
	"testing"
)

func TestTrustedPlanningRetainsCoreDependencyCause(t *testing.T) {
	base, settings := managedConfig()
	settings.Sites[0].Modules = []string{"example.search"}
	_, err := multisite.NewManaged(base, settings)
	if !errors.Is(err, multisite.ErrConfiguration) || !errors.Is(err, achrix.ErrComposition) {
		t.Fatal("graph cause erased", err)
	}
	for _, want := range []string{"selections[0].modules", "example.search", "example.store"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("missing %q: %v", want, err)
		}
	}
}

func TestResolverConfigurationDetailsDoNotEchoInvalidValues(t *testing.T) {
	_, err := multisite.New(multisite.Config{Sites: []multisite.Site{{ID: "a", Authorities: []string{"SECRET@invalid/path"}}}})
	if !errors.Is(err, multisite.ErrConfiguration) || !strings.Contains(err.Error(), "sites[0].authorities[0]") || !strings.Contains(err.Error(), "canonical host") {
		t.Fatal(err)
	}
	if strings.Contains(err.Error(), "SECRET") {
		t.Fatal("invalid authority echoed", err)
	}
}
