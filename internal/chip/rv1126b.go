/*
 * SPDX-FileCopyrightText: 2026 Luckfox Team
 *
 * SPDX-License-Identifier: Apache-2.0
 */

package chip

import (
	"fmt"
)

type RV1126B struct{}

func init() {
	Register(&RV1126B{})
}

func (c *RV1126B) Name() string {
	return "rv1126b"
}

// rv1126bPullAddrs maps (bank, group) → pull register address (from RV1126B GPIO manual).
// Bank 0 groups C,D live in a separate GRF sub-block (0x201A8xxx vs 0x201A0xxx for A,B).
// Bank 4 group B starts at a non-standard offset due to group A having only 6 pins.
var rv1126bPullAddrs = map[int]map[string]uint32{
	0: {"A": 0x201a0300, "B": 0x201a0304, "C": 0x201a8308, "D": 0x201a830c},
	1: {"A": 0x201b0310, "B": 0x201b0314},
	2: {"A": 0x201b8320},
	3: {"A": 0x201c0330, "B": 0x201c0334},
	4: {"A": 0x201c8340, "B": 0x201c8344},
	5: {"A": 0x201d0350, "B": 0x201d0354, "C": 0x201d0358, "D": 0x201d035c},
	6: {"A": 0x201d8360, "B": 0x201d8364, "C": 0x201d8368},
	7: {"A": 0x201e0370, "B": 0x201e0374},
}

func (c *RV1126B) GetGPIOPullBase(bank int, group string) (uint32, error) {
	groups, ok := rv1126bPullAddrs[bank]
	if !ok {
		return 0, fmt.Errorf("RV1126B: unsupported GPIO pull bank %d", bank)
	}
	addr, ok := groups[group]
	if !ok {
		return 0, fmt.Errorf("RV1126B: bank %d has no GPIO group %q", bank, group)
	}
	return addr, nil
}

// rv1126bDSAddrs maps (bank, group) → first DS register address (from RV1126B GPIO manual).
// Bank 0 groups C,D are in sub-block 0x201A8xxx. Bank 4 group B starts at non-standard +0x0C offset.
var rv1126bDSAddrs = map[int]map[string]uint32{
	0: {"A": 0x201a0100, "B": 0x201a0110, "C": 0x201a8120, "D": 0x201a8130},
	1: {"A": 0x201b0140, "B": 0x201b0150},
	2: {"A": 0x201b8180},
	3: {"A": 0x201c01c0, "B": 0x201c01d0},
	4: {"A": 0x201c8200, "B": 0x201c820c},
	5: {"A": 0x201d0240, "B": 0x201d0250, "C": 0x201d0260, "D": 0x201d0270},
	6: {"A": 0x201d8280, "B": 0x201d8290, "C": 0x201d82a0},
	7: {"A": 0x201e02c0, "B": 0x201e02d0},
}

func (c *RV1126B) GetGPIODSBase(bank int, group string) (uint32, error) {
	groups, ok := rv1126bDSAddrs[bank]
	if !ok {
		return 0, fmt.Errorf("RV1126B: unsupported GPIO DS bank %d", bank)
	}
	addr, ok := groups[group]
	if !ok {
		return 0, fmt.Errorf("RV1126B: bank %d has no GPIO DS group %q", bank, group)
	}
	return addr, nil
}

var rv1126bGPIOBases = [8]uint32{
	0x20600000, 0x21300000, 0x21700000, 0x21e00000,
	0x21800000, 0x21900000, 0x21a00000, 0x21b00000,
}

func (c *RV1126B) GetGPIOBase(bank int) (uint32, error) {
	if bank < 0 || bank >= len(rv1126bGPIOBases) {
		return 0, fmt.Errorf("RV1126B: unsupported GPIO bank %d", bank)
	}
	return rv1126bGPIOBases[bank], nil
}

func (c *RV1126B) RMIOToGPIO(rmio int) (int, error) {
	return 0, fmt.Errorf("RV1126B: RM_IO not supported")
}

func (c *RV1126B) PWMAlias(controller, channel int) string {
	// RV1126B PWM node labels:
	// pwm0_8ch_X, pwm1_4ch_X, pwm2_8ch_X, pwm3_8ch_X
	if controller == 1 {
		return fmt.Sprintf("pwm%d_4ch_%d", controller, channel)
	}
	return fmt.Sprintf("pwm%d_8ch_%d", controller, channel)
}
