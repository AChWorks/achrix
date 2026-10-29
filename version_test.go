// SPDX-License-Identifier: MPL-2.0
package achrix

import (
	"runtime/debug"
	"testing"
)

func TestSourceVersionBuildMetadata(t *testing.T) {
	const path = "github.com/AChWorks/achrix"
	const pseudo = "v0.1.1-0.20261002091741-73e9e217624c"
	for _, tt := range []struct {
		name string
		info debug.BuildInfo
		want string
	}{
		{"tagged main", debug.BuildInfo{Main: debug.Module{Path: path, Version: "v0.2.0"}}, "v0.2.0"},
		{"tagged dependency", debug.BuildInfo{Deps: []*debug.Module{{Path: path, Version: "v0.2.0"}}}, "v0.2.0"},
		{"pseudo-version", debug.BuildInfo{Deps: []*debug.Module{{Path: path, Version: pseudo}}}, pseudo},
		{"local main", debug.BuildInfo{Main: debug.Module{Path: path, Version: "(devel)"}}, "development"},
		{"local dependency", debug.BuildInfo{Deps: []*debug.Module{{Path: path, Version: "(devel)"}}}, "development"},
		{"unversioned dependency", debug.BuildInfo{Deps: []*debug.Module{{Path: path}}}, "development"},
		{"local replacement", debug.BuildInfo{Deps: []*debug.Module{{Path: path, Version: pseudo, Replace: &debug.Module{Path: "../achrix"}}}}, "development-replacement"},
		{"versioned replacement", debug.BuildInfo{Deps: []*debug.Module{{Path: path, Version: pseudo, Replace: &debug.Module{Path: "example.com/fork", Version: "v1.0.0"}}}}, "development-replacement"},
		{"unrelated metadata", debug.BuildInfo{Main: debug.Module{Path: "example.com/product", Version: "v1.0.0"}, Deps: []*debug.Module{{Path: "example.com/other", Version: "v1.0.0"}}}, "development"},
		{"missing metadata", debug.BuildInfo{}, "development"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := sourceVersion(&tt.info); got != tt.want {
				t.Fatalf("source identity = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestIndependentDependencyVersions(t *testing.T) {
	info := &debug.BuildInfo{Deps: []*debug.Module{
		{Path: "github.com/AChWorks/achrix", Version: "v0.4.0"},
		{Path: "github.com/AChWorks/achrix/audit", Version: "v0.1.2"},
		{Path: "github.com/AChWorks/achrix/media", Version: "v0.2.3"},
		{Path: "example.com/replaced", Version: "v1.0.0", Replace: &debug.Module{Path: "../local"}},
	}}
	for path, want := range map[string]string{
		"github.com/AChWorks/achrix":       "v0.4.0",
		"github.com/AChWorks/achrix/audit": "v0.1.2",
		"github.com/AChWorks/achrix/media": "v0.2.3",
		"example.com/replaced":             "development-replacement",
		"example.com/absent":               "development",
		"":                                 "development",
	} {
		if got := dependencyVersion(info, path); got != want {
			t.Errorf("%s: got %q, want %q", path, got, want)
		}
	}
	if got := dependencyVersion(nil, "example.com/absent"); got != "development" {
		t.Fatal("nil build information did not report development")
	}
}
