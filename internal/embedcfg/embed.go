/*
 * SPDX-FileCopyrightText: 2026 Luckfox Team
 *
 * SPDX-License-Identifier: Apache-2.0
 */

// Package embedcfg provides the embedded board and panel JSON configurations.
package embedcfg

import "embed"

//go:embed boards/*.json panels/*.json
var FS embed.FS
