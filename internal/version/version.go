/*
 * SPDX-FileCopyrightText: 2026 Luckfox Team
 *
 * SPDX-License-Identifier: Apache-2.0
 */

package version

import (
	"fmt"
	"strings"
)

var (
	Version   = "dev"
	Commit    = "none"
	BuildDate = "unknown"
)

func String() string {
	meta := make([]string, 0, 2)
	if Commit != "" && Commit != "none" {
		meta = append(meta, "commit "+Commit)
	}
	if BuildDate != "" && BuildDate != "unknown" {
		meta = append(meta, "date "+BuildDate)
	}
	if len(meta) == 0 {
		return Version
	}
	return fmt.Sprintf("%s (%s)", Version, strings.Join(meta, ", "))
}
