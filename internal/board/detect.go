/*
 * SPDX-FileCopyrightText: 2026 Luckfox Team
 *
 * SPDX-License-Identifier: Apache-2.0
 */

package board

import (
	"os"
	"path/filepath"
	"strings"
)

const (
	modelPath   = "/proc/device-tree/model"
	aliasesPath = "/proc/device-tree/aliases"
)

// DetectModel reads the board model from the device tree.
func DetectModel() (string, error) {
	data, err := os.ReadFile(modelPath)
	if err != nil {
		return "", err
	}
	return strings.TrimRight(string(data), "\x00\n"), nil
}

// ResolveAlias finds the full device tree path for a given alias.
// e.g. "can0" -> "/can@ff320000"
func ResolveAlias(alias string) (string, error) {
	path := filepath.Join(aliasesPath, alias)
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	// DT paths in proc are null-terminated
	return strings.TrimRight(string(data), "\x00"), nil
}
