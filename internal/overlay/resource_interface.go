/*
 * SPDX-FileCopyrightText: 2026 Luckfox Team
 *
 * SPDX-License-Identifier: Apache-2.0
 */

package overlay

import (
	"fmt"
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
