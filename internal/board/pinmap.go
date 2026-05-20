/*
 * SPDX-FileCopyrightText: 2026 Luckfox Team
 *
 * SPDX-License-Identifier: Apache-2.0
 */

package board

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"luckfox-config/internal/chip"
)

// GPIOPin represents a parsed GPIO pin (e.g. GPIO1_B3 → bank=1, group="B", num=3).
type GPIOPin struct {
	Bank   int
	Group  string // "A", "B", "C", "D"
	Number int    // 0-7
}

// groupOffset maps GPIO group letter to its bank-relative pin offset.
var groupOffset = map[string]int{"A": 0, "B": 8, "C": 16, "D": 24}

var gpioRe = regexp.MustCompile(`^GPIO(\d+)_([A-D])(\d+)$`)

// ParseGPIO parses a string like "GPIO0_A3" into a GPIOPin.
func ParseGPIO(s string) (GPIOPin, error) {
	m := gpioRe.FindStringSubmatch(s)
	if m == nil {
		return GPIOPin{}, fmt.Errorf("invalid GPIO string: %q", s)
	}
	bank, _ := strconv.Atoi(m[1])
	num, _ := strconv.Atoi(m[3])
	return GPIOPin{Bank: bank, Group: m[2], Number: num}, nil
}

// RMIOToGPIO converts an RM_IO index to a GPIOPin using the SoC-specific mapping.
func RMIOToGPIO(c chip.Chip, rmio int) (GPIOPin, error) {
	raw, err := c.RMIOToGPIO(rmio)
	if err != nil {
		return GPIOPin{}, err
	}
	return RawGPIOToPin(raw), nil
}

// RawGPIOToPin converts a raw GPIO number (e.g. 41) to a GPIOPin struct.
func RawGPIOToPin(raw int) GPIOPin {
	bank := raw / 32
	pinInBank := raw % 32
	group := ""
	num := 0
	switch {
	case pinInBank < 8:
		group, num = "A", pinInBank
	case pinInBank < 16:
		group, num = "B", pinInBank-8
	case pinInBank < 24:
		group, num = "C", pinInBank-16
	default:
		group, num = "D", pinInBank-24
	}
	return GPIOPin{Bank: bank, Group: group, Number: num}
}

// RMIOFromGPIOString parses a GPIO string and finds the corresponding RM_IO index.
func RMIOFromGPIOString(c chip.Chip, gpioStr string) (int, error) {
	pin, err := ParseGPIO(gpioStr)
	if err != nil {
		return -1, err
	}
	off, ok := groupOffset[pin.Group]
	if !ok {
		return -1, fmt.Errorf("unknown group %q", pin.Group)
	}
	raw := pin.Bank*32 + off + pin.Number

	// Since we don't have a reverse map in the Chip interface yet,
	// we iterate through all 32 RM_IO indices.
	for i := 0; i < 32; i++ {
		v, err := c.RMIOToGPIO(i)
		if err == nil && v == raw {
			return i, nil
		}
	}
	return -1, fmt.Errorf("GPIO %s not in RM_IO map for chip %s", gpioStr, c.Name())
}

// RMIOName returns the canonical name string for an RM_IO index.
func RMIOName(rmio int) string {
	return fmt.Sprintf("RM_IO%d", rmio)
}

// ParseRMIO parses "RM_IO12" → 12.
func ParseRMIO(s string) (int, error) {
	s = strings.TrimPrefix(s, "RM_IO")
	n, err := strconv.Atoi(s)
	if err != nil {
		return -1, fmt.Errorf("invalid RM_IO string: %q", s)
	}
	if n < 0 || n >= 32 {
		return -1, fmt.Errorf("RM_IO index %d out of range", n)
	}
	return n, nil
}

// Raw returns the raw GPIO number (e.g. Bank*32 + groupOff + Number).
func (g GPIOPin) Raw() int {
	return g.Bank*32 + groupOffset[g.Group] + g.Number
}

// String returns "GPIOx_Yy" form.
func (g GPIOPin) String() string {
	return fmt.Sprintf("GPIO%d_%s%d", g.Bank, g.Group, g.Number)
}
