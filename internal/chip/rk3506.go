/*
 * SPDX-FileCopyrightText: 2026 Luckfox Team
 *
 * SPDX-License-Identifier: Apache-2.0
 */

package chip

import (
	"fmt"
)

type RK3506 struct{}

func init() {
	Register(&RK3506{})
}

func (c *RK3506) Name() string {
	return "rk3506"
}

var rk3506PullBases = map[int]uint32{0: 0xff950200, 1: 0xff660210, 2: 0xff4d8220, 3: 0xff4d8230}
var rk3506DSBases = map[int]uint32{0: 0xff950100, 1: 0xff660140}
var gpioGroupOff = map[string]uint32{"A": 0x00, "B": 0x04, "C": 0x08, "D": 0x0c}
var gpioGroupDSOff = map[string]uint32{"A": 0x00, "B": 0x10, "C": 0x20, "D": 0x30}

func (c *RK3506) GetGPIOPullBase(bank int, group string) (uint32, error) {
	base, ok := rk3506PullBases[bank]
	if !ok {
		return 0, fmt.Errorf("RK3506: unsupported GPIO pull bank %d", bank)
	}
	off, ok := gpioGroupOff[group]
	if !ok {
		return 0, fmt.Errorf("RK3506: unsupported GPIO group %q", group)
	}
	return base + off, nil
}

func (c *RK3506) GetGPIODSBase(bank int, group string) (uint32, error) {
	base, ok := rk3506DSBases[bank]
	if !ok {
		return 0, fmt.Errorf("RK3506: unsupported GPIO DS bank %d", bank)
	}
	off, ok := gpioGroupDSOff[group]
	if !ok {
		return 0, fmt.Errorf("RK3506: unsupported GPIO group %q", group)
	}
	return base + off, nil
}

var rk3506GPIOBases = [4]uint32{0xff950000, 0xff660000, 0xff4d8000, 0xff4d8800}

func (c *RK3506) GetGPIOBase(bank int) (uint32, error) {
	if bank < 0 || bank >= len(rk3506GPIOBases) {
		return 0, fmt.Errorf("RK3506: unsupported GPIO bank %d", bank)
	}
	return rk3506GPIOBases[bank], nil
}

var rk3506RMIOToRawGPIO = [32]int{
	0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15,
	16, 17, 18, 19, 20, 21, 22, 23,
	41, 42, 43, 50, 51, 57, 58, 59,
}

func (c *RK3506) RMIOToGPIO(rmio int) (int, error) {
	if rmio < 0 || rmio >= 32 {
		return 0, fmt.Errorf("RK3506: RM_IO index %d out of range (0-31)", rmio)
	}
	return rk3506RMIOToRawGPIO[rmio], nil
}

func (c *RK3506) PWMAlias(controller, channel int) string {
	// RK3506 uses RM_IO and has these PWM labels
	if controller == 0 {
		return fmt.Sprintf("pwm0_4ch_%d", channel)
	}
	return fmt.Sprintf("pwm1_8ch_%d", channel)
}
