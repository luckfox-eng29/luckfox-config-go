/*
 * SPDX-FileCopyrightText: 2026 Luckfox Team
 *
 * SPDX-License-Identifier: Apache-2.0
 */

package overlay

import (
	"fmt"
	"os/exec"
	"strconv"
	"strings"
)

// StaticResource defines the interface for SoC-specific DTB storage logic.
type StaticResource interface {
	// DTB returns the current in-memory DTB bytes.
	DTB() []byte
	// SetDTB replaces the in-memory DTB bytes.
	SetDTB(dtb []byte)
	// Flush writes the DTB and its metadata (hash, size) back to the storage device.
	Flush() error
}

// fdtGetUint32 reads a 32-bit integer property from a node of a DTB file.
// fdtget prints integer properties without a base prefix, so base 0 parsing
// accepts both decimal and 0x-prefixed values.
func fdtGetUint32(path, node, prop string) (uint32, error) {
	out, err := exec.Command("fdtget", path, node, prop).CombinedOutput()
	if err != nil {
		return 0, fmt.Errorf("fdtget %s %s: %w: %s", node, prop, err, strings.TrimSpace(string(out)))
	}
	trimmed := strings.TrimSpace(string(out))
	v, err := strconv.ParseUint(trimmed, 0, 32)
	if err != nil {
		return 0, fmt.Errorf("parse %s %s value %q: %w", node, prop, trimmed, err)
	}
	return uint32(v), nil
}

// NewStaticResource is a factory that creates the appropriate StaticResource for the chip.
func NewStaticResource(chipName string, device string) (StaticResource, error) {
	switch chipName {
	case "rk3506", "rv1126b":
		return OpenNormalResource(device)
	case "rv1106":
		return OpenRV1106Resource(device)
	default:
		return nil, fmt.Errorf("unsupported chip for static overlay: %q", chipName)
	}
}
