// Copyright 2026 Blink Labs Software
// SPDX-License-Identifier: Apache-2.0

package webhook

import (
	"fmt"
)

func httpStatusClass(status int) string {
	if status < 100 || status >= 600 {
		return "other"
	}
	return fmt.Sprintf("%dxx", status/100)
}
