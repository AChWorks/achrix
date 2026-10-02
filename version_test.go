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
