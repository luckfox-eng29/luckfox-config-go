/*
 * SPDX-FileCopyrightText: 2026 Luckfox Team
 *
 * SPDX-License-Identifier: Apache-2.0
 */

package chip

import (
	"fmt"
)

// Chip represents a SoC chip (e.g. RK3506, RV1106).
type Chip interface {
	// Name returns the chip name.
	Name() string

	// GetGPIOPullBase returns the GRF register address for the pull config of the given bank+group.
	// The caller adds only the per-pin bit offset; no group offset is applied by the caller.
	GetGPIOPullBase(bank int, group string) (uint32, error)

	// GetGPIODSBase returns the GRF register base for the drive-strength config of the given bank+group.
	// The caller adds (pin.Number/2)*4 to reach the specific pin's register; no group offset is applied by the caller.
	GetGPIODSBase(bank int, group string) (uint32, error)

	// GetGPIOBase returns the register base for a GPIO bank.
	GetGPIOBase(bank int) (uint32, error)

	// RMIOToGPIO converts an RM_IO index to a raw GPIO number.
	// Only applicable for chips that use RM_IO (like RK3506).
	RMIOToGPIO(rmio int) (int, error)

	// PWMAlias returns the device tree alias/label for a PWM controller and channel.
	PWMAlias(controller, channel int) string
}

// Chip name constants for use across packages.
const (
	ChipRK3506  = "rk3506"
	ChipRV1106  = "rv1106"
	ChipRV1126B = "rv1126b"
)

var chips = make(map[string]Chip)

// Register registers a chip implementation.
func Register(c Chip) {
	chips[c.Name()] = c
}

// Get returns the chip implementation for the given name.
func Get(name string) (Chip, error) {
	if c, ok := chips[name]; ok {
		return c, nil
	}
	return nil, fmt.Errorf("unsupported chip: %q", name)
}
