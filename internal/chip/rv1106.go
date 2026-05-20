/*
 * SPDX-FileCopyrightText: 2026 Luckfox Team
 *
 * SPDX-License-Identifier: Apache-2.0
 */

package chip

import (
	"fmt"
)

type RV1106 struct{}

func init() {
	Register(&RV1106{})
}

func (c *RV1106) Name() string {
	return "rv1106"
}

var rv1106PullBases = [5]uint32{0xff388038, 0xff5381c0, 0xff5481d0, 0xff5581e0, 0xff568070}
var rv1106DSBases = [5]uint32{0xff388010, 0xff538080, 0xff5480c0, 0xff558100, 0xff568020}

// groupOff maps group letter to byte offset within a bank's pull/DS block.
var rv1106GroupPullOff = map[string]uint32{"A": 0x00, "B": 0x04, "C": 0x08, "D": 0x0c}
var rv1106GroupDSOff = map[string]uint32{"A": 0x00, "B": 0x10, "C": 0x20, "D": 0x30}

func (c *RV1106) GetGPIOPullBase(bank int, group string) (uint32, error) {
	if bank < 0 || bank >= len(rv1106PullBases) {
		return 0, fmt.Errorf("RV1106: unsupported GPIO pull bank %d", bank)
	}
	off, ok := rv1106GroupPullOff[group]
	if !ok {
		return 0, fmt.Errorf("RV1106: unsupported GPIO group %q", group)
	}
	return rv1106PullBases[bank] + off, nil
}

func (c *RV1106) GetGPIODSBase(bank int, group string) (uint32, error) {
	if bank < 0 || bank >= len(rv1106DSBases) {
		return 0, fmt.Errorf("RV1106: unsupported GPIO drive strength bank %d", bank)
	}
	off, ok := rv1106GroupDSOff[group]
	if !ok {
		return 0, fmt.Errorf("RV1106: unsupported GPIO group %q", group)
	}
	return rv1106DSBases[bank] + off, nil
}

var rv1106GPIOBases = [5]uint32{0xff380000, 0xff530000, 0xff540000, 0xff550000, 0xff560000}

func (c *RV1106) GetGPIOBase(bank int) (uint32, error) {
	if bank < 0 || bank >= len(rv1106GPIOBases) {
		return 0, fmt.Errorf("RV1106: unsupported GPIO bank %d", bank)
	}
	return rv1106GPIOBases[bank], nil
}

func (c *RV1106) RMIOToGPIO(rmio int) (int, error) {
	return 0, fmt.Errorf("RV1106: RM_IO not supported")
}

func (c *RV1106) PWMAlias(controller, channel int) string {
	// RV1106 has simple pwm0-pwm11 aliases
	return fmt.Sprintf("pwm%d", channel)
}
