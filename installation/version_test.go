// SPDX-License-Identifier: MPL-2.0
package installation

import (
	"runtime/debug"
	"testing"
)

func TestSourceVersion(t *testing.T) {
	const path = "github.com/AChWorks/achrix/installation"
	for _, tc := range []struct {
		info debug.BuildInfo
		want string
	}{
		{debug.BuildInfo{Main: debug.Module{Path: path, Version: "v0.1.0"}}, "v0.1.0"},
		{debug.BuildInfo{Deps: []*debug.Module{{Path: path, Version: "v0.0.0-20261001000000-0123456789ab"}}}, "v0.0.0-20261001000000-0123456789ab"},
		{debug.BuildInfo{Deps: []*debug.Module{{Path: path, Version: "v0.1.0", Replace: &debug.Module{Path: "../installation"}}}}, "development-replacement"},
		{debug.BuildInfo{Main: debug.Module{Path: path, Version: "(devel)"}}, "development"},
		{debug.BuildInfo{}, "development"},
		{debug.BuildInfo{Deps: []*debug.Module{{Path: path}}}, "development"},
	} {
		if got := sourceVersion(&tc.info); got != tc.want {
			t.Fatalf("got %q want %q", got, tc.want)
		}
	}
	if Version() == "" {
		t.Fatal("empty version")
	}
}
