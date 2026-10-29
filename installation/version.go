// SPDX-License-Identifier: MPL-2.0
package installation

import "runtime/debug"

// Version reports this installation module's source-backed dependency version.
// Local or replaced builds identify themselves explicitly as development.
func Version() string {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return "development"
	}
	return sourceVersion(info)
}
func sourceVersion(info *debug.BuildInfo) string {
	const path = "github.com/AChWorks/achrix/installation"
	if info.Main.Path == path && info.Main.Version != "" && info.Main.Version != "(devel)" {
		return info.Main.Version
	}
	for _, d := range info.Deps {
		if d.Path != path {
			continue
		}
		if d.Replace != nil {
			return "development-replacement"
		}
		if d.Version == "" || d.Version == "(devel)" {
			return "development"
		}
		return d.Version
	}
	return "development"
}
