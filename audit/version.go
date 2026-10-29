// SPDX-License-Identifier: MPL-2.0
package audit

import "github.com/AChWorks/achrix"

// Version identifies this Module's actual packaged source dependency.
// Local and replacement builds report explicit development categories.
func Version() string { return achrix.DependencyVersion("github.com/AChWorks/achrix/audit") }
