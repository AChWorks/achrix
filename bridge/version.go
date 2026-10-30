// SPDX-License-Identifier: MPL-2.0
package bridge

import "github.com/AChWorks/achrix"

// Version identifies this component's actual packaged source dependency.
func Version() string { return achrix.DependencyVersion("github.com/AChWorks/achrix/bridge") }
