/*
 * SPDX-FileCopyrightText: 2026 Luckfox Team
 *
 * SPDX-License-Identifier: Apache-2.0
 */

package peripheral

import (
	"context"
	"fmt"
	"strconv"

	"luckfox-config/internal/board"
	"luckfox-config/internal/chip"
	"luckfox-config/internal/config"
	"luckfox-config/internal/hwio"
	"luckfox-config/internal/logger"
)

// PullMode represents a GPIO pull resistor configuration.
type PullMode int

const (
	PullNone PullMode = 0
	PullUp   PullMode = 1
	PullDown PullMode = 2
)

// dsLevelData maps drive-strength level (0-5) to the 6-bit data value written to the register field.
// Each 32-bit DS register holds 2 pins: even pin in bits[5:0], odd pin in bits[13:8]; write-mask in upper 16 bits.
var dsLevelData = []int64{0x01, 0x03, 0x07, 0x0f, 0x1f, 0x3f}

// SetPull sets the pull resistor mode for a GPIO pin.
// Calls `io -4 <addr> <value>` to write the GRF register.
func SetPull(c chip.Chip, gpio int, mode PullMode) error {
	pin := board.RawGPIOToPin(gpio)

	// Chip returns the exact pull register address for this bank+group.
	base, err := c.GetGPIOPullBase(pin.Bank, pin.Group)
	if err != nil {
		return err
	}

	// Each pin uses 2 bits; pin N occupies bits [2N+1:2N] with write-mask at [2N+17:2N+16].
	pinOff := int64(pin.Number * 2)
	val := (1 << (pinOff + 16)) | (1 << (pinOff + 17)) // write-mask for 2 bits
	switch mode {
	case PullUp:
		val |= 1 << pinOff
	case PullDown:
		val |= 2 << pinOff
	}

	return hwio.WriteReg32(base, uint32(val))
}

// SetDriveStrength sets the drive strength for a GPIO pin.
// Level 0-5 (0=weakest, 5=strongest).
func SetDriveStrength(c chip.Chip, gpio int, level int) error {
	if level < 0 || level >= len(dsLevelData) {
		return fmt.Errorf("drive strength level %d out of range (0-5)", level)
	}
	pin := board.RawGPIOToPin(gpio)

	// Chip returns the first DS register address for this bank+group.
	base, err := c.GetGPIODSBase(pin.Bank, pin.Group)
	if err != nil {
		return err
	}

	// Each 32-bit DS register holds 2 pins:
	//   even pin N: data in bits[5:0],  write-mask in bits[21:16]
	//   odd  pin N: data in bits[13:8], write-mask in bits[29:24]
	regOff := int64((pin.Number / 2) * 4)
	bitOff := int64((pin.Number % 2) * 8)
	mask := int64(0x3f) << (bitOff + 16)
	data := dsLevelData[level] << bitOff
	val := mask | data

	return hwio.WriteReg32(base+uint32(regOff), uint32(val))
}

// ─── GPIO Peripheral ──────────────────────────────────────────────────────────

// GPIO manages a single GPIO pin.
type GPIO struct {
	pin  int
	pull PullMode
	ds   int
	deps Deps
}

func NewGPIO(rmio int, deps Deps) *GPIO {
	return &GPIO{pin: rmio, deps: deps, pull: PullNone, ds: 2}
}

func (g *GPIO) ID() string { return fmt.Sprintf("gpio%d", g.pin) }
func (g *GPIO) IsActive() bool {
	gpio := g.deps.MapRMIOToGPIO(g.pin)
	m, err := GetPinMode(g.deps.Chip, gpio)
	if err != nil {
		return false
	}
	// Mode 0 is GPIO mode.
	return m == 0
}

func (g *GPIO) PinsUsed() []int {
	if g.pin < 0 {
		return nil
	}
	return []int{g.pin}
}

func (g *GPIO) ConfigKeys() []string {
	p := fmt.Sprintf("GPIO%d_", g.pin)
	return []string{p + "STATUS", p + "PULL", p + "DRIVE"}
}

func (g *GPIO) Enable(ctx context.Context) error {
	if g.pin < 0 {
		return fmt.Errorf("gpio: pin not set")
	}
	pinName := board.RMIOName(g.pin)
	if err := g.deps.Diagram.CheckConflictErr([]string{pinName}); err != nil {
		return err
	}

	gpio := g.deps.MapRMIOToGPIO(g.pin)
	if err := SetPinMode(g.deps.Chip, gpio, 0); err != nil {
		return err
	}
	if err := SetPull(g.deps.Chip, gpio, g.pull); err != nil {
		return err
	}
	if err := SetDriveStrength(g.deps.Chip, gpio, g.ds); err != nil {
		return err
	}

	g.deps.Diagram.MarkPin(pinName, true)
	logger.Info("GPIO enabled", "pin", pinName, "pull", g.pull, "ds", g.ds)

	return g.deps.Cfg.SetMulti(fmt.Sprintf("GPIO%d", g.pin), map[string]string{
		"STATUS": "1",
		"PULL":   strconv.Itoa(int(g.pull)),
		"DRIVE":  strconv.Itoa(g.ds),
	})
}

func (g *GPIO) Disable(ctx context.Context) error {
	if g.pin >= 0 {
		gpio := g.deps.MapRMIOToGPIO(g.pin)
		_ = ResetPinMode(g.deps.Chip, gpio)
		_ = SetPull(g.deps.Chip, gpio, PullNone)
		g.deps.Diagram.MarkPin(board.RMIOName(g.pin), false)
		g.pin = -1
	}
	return g.deps.Cfg.Set(fmt.Sprintf("GPIO%d", g.pin), "STATUS", "0")
}

func (g *GPIO) LoadFromConfig(ctx context.Context, cfg *config.Store, apply bool) error {
	g.deps.Cfg = cfg
	mod := fmt.Sprintf("GPIO%d", g.pin)
	if cfg.Get(mod, "STATUS") != "1" {
		return nil
	}
	if v := cfg.Get(mod, "PULL"); v != "" {
		p, _ := strconv.Atoi(v)
		g.pull = PullMode(p)
	}
	if v := cfg.Get(mod, "DRIVE"); v != "" {
		g.ds, _ = strconv.Atoi(v)
	}
	if apply {
		return g.Enable(ctx)
	}
	g.deps.Diagram.MarkPin(board.RMIOName(g.pin), true)
	return nil
}

func (g *GPIO) IsConfigEnabled(cfg *config.Store) bool {
	return cfg.Get(fmt.Sprintf("GPIO%d", g.pin), "STATUS") == "1"
}

func (g *GPIO) SetPull(m PullMode) { g.pull = m }
func (g *GPIO) SetDS(level int)    { g.ds = level }

var _ Peripheral = (*GPIO)(nil)
