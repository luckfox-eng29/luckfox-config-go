/*
 * SPDX-FileCopyrightText: 2026 Luckfox Team
 *
 * SPDX-License-Identifier: Apache-2.0
 */

package peripheral

import (
	"luckfox-config/internal/board"
	"luckfox-config/internal/chip"
	"luckfox-config/internal/hwio"
)

var iomuxGroupOff = map[string]int{"A": 0, "B": 8, "C": 16, "D": 24}

// SetPinMode sets the iomux multiplexing function for a GPIO pin via /dev/iomux ioctl.
func SetPinMode(c chip.Chip, gpio int, mode int) error {
	pin := board.RawGPIOToPin(gpio)
	return hwio.IomuxSet(pin.Bank, iomuxGroupOff[pin.Group]+pin.Number, mode)
}

// GetPinMode reads the current iomux mode for a GPIO pin via /dev/iomux ioctl.
func GetPinMode(c chip.Chip, gpio int) (int, error) {
	pin := board.RawGPIOToPin(gpio)
	return hwio.IomuxGet(pin.Bank, iomuxGroupOff[pin.Group]+pin.Number)
}

// ResetPinMode sets a pin back to GPIO mode (mode 0).
func ResetPinMode(c chip.Chip, gpio int) error {
	return SetPinMode(c, gpio, 0)
}

// ResetPinModeIfMode7 resets a pin to GPIO mode (0) only when its current
// iomux mode is 7. Mirrors the shell scripts' disable logic, which only
// touches pins that were left in mode 7.
func ResetPinModeIfMode7(c chip.Chip, gpio int) error {
	mode, err := GetPinMode(c, gpio)
	if err != nil {
		return err
	}
	if mode == 7 {
		return SetPinMode(c, gpio, 0)
	}
	return nil
}
