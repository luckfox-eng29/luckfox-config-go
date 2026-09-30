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
	"strings"

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

// defaultGPIODriveStrength is the level used when the user does not pick one.
// Level 2 is the Rockchip pinctrl default and the value the shell scripts
// restored when releasing a pin.
const defaultGPIODriveStrength = 2

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
//
// On chips with an RM_IO table (RK3506) a pin is addressed by its RM_IO index,
// which is how the board exposes it to the user. On chips without one
// (RV1106/RV1126B) the pin is addressed directly as "GPIOx_Yy".
type GPIO struct {
	rmio int    // RM_IO index; -1 when the pin is addressed directly
	gpio int    // raw GPIO number (bank*32 + group offset + number)
	name string // "RM_IO12" or "GPIO1_C6"
	pull PullMode
	ds   int
	deps Deps
}

// NewGPIO creates an RM_IO addressed GPIO.
func NewGPIO(rmio int, deps Deps) *GPIO {
	return &GPIO{
		rmio: rmio,
		gpio: deps.MapRMIOToGPIO(rmio),
		name: board.RMIOName(rmio),
		pull: PullNone,
		ds:   defaultGPIODriveStrength,
		deps: deps,
	}
}

// NewGPIOByName creates a directly addressed GPIO from its "GPIOx_Yy" name.
func NewGPIOByName(name string, deps Deps) (*GPIO, error) {
	pin, err := board.ParseGPIO(name)
	if err != nil {
		return nil, err
	}
	return &GPIO{
		rmio: -1,
		gpio: pin.Raw(),
		name: pin.String(),
		pull: PullNone,
		ds:   defaultGPIODriveStrength,
		deps: deps,
	}, nil
}

// cfgModule returns the config store module prefix for this pin.
func (g *GPIO) cfgModule() string {
	if g.rmio >= 0 {
		return fmt.Sprintf("GPIO%d", g.rmio)
	}
	return g.name
}

func (g *GPIO) ID() string {
	if g.rmio >= 0 {
		return fmt.Sprintf("gpio%d", g.rmio)
	}
	return "gpio" + strings.ToLower(g.name)
}

func (g *GPIO) IsActive() bool {
	m, err := GetPinMode(g.deps.Chip, g.gpio)
	if err != nil {
		return false
	}
	// Mode 0 is GPIO mode.
	return m == 0
}

func (g *GPIO) PinsUsed() []int {
	if g.rmio < 0 {
		// Directly addressed pins have no RM_IO index to report.
		return nil
	}
	return []int{g.rmio}
}

func (g *GPIO) ConfigKeys() []string {
	p := g.cfgModule() + "_"
	return []string{p + "STATUS", p + "PULL", p + "DRIVE"}
}

func (g *GPIO) Enable(ctx context.Context) error {
	if err := g.deps.Diagram.CheckConflictErr([]string{g.name}); err != nil {
		return err
	}

	if err := SetPinMode(g.deps.Chip, g.gpio, 0); err != nil {
		return err
	}
	if err := SetPull(g.deps.Chip, g.gpio, g.pull); err != nil {
		return err
	}
	if err := SetDriveStrength(g.deps.Chip, g.gpio, g.ds); err != nil {
		return err
	}

	g.deps.Diagram.MarkPin(g.name, true)
	logger.Info("GPIO enabled", "pin", g.name, "pull", g.pull, "ds", g.ds)

	return g.deps.Cfg.SetMulti(g.cfgModule(), map[string]string{
		"STATUS": "1",
		"PULL":   strconv.Itoa(int(g.pull)),
		"DRIVE":  strconv.Itoa(g.ds),
	})
}

func (g *GPIO) Disable(ctx context.Context) error {
	_ = ResetPinMode(g.deps.Chip, g.gpio)
	_ = SetPull(g.deps.Chip, g.gpio, PullNone)
	g.deps.Diagram.MarkPin(g.name, false)
	logger.Info("GPIO disabled", "pin", g.name)
	return g.deps.Cfg.Set(g.cfgModule(), "STATUS", "0")
}

func (g *GPIO) LoadFromConfig(ctx context.Context, cfg *config.Store, apply bool) error {
	g.deps.Cfg = cfg
	mod := g.cfgModule()
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
	g.deps.Diagram.MarkPin(g.name, true)
	return nil
}

func (g *GPIO) IsConfigEnabled(cfg *config.Store) bool {
	return cfg.Get(g.cfgModule(), "STATUS") == "1"
}

// Name returns the user visible pin name ("RM_IO12" or "GPIO1_C6").
func (g *GPIO) Name() string { return g.name }

// ConfigModule returns the config store module holding this pin's state.
func (g *GPIO) ConfigModule() string { return g.cfgModule() }

// Pin returns the raw GPIO number of the pin (bank*32 + group offset + number),
// which identifies the same physical pin across the RM_IO and GPIO name forms.
func (g *GPIO) Pin() int { return g.gpio }

// Pull returns the configured pull resistor mode.
func (g *GPIO) Pull() PullMode { return g.pull }

// DriveStrength returns the configured drive strength level.
func (g *GPIO) DriveStrength() int { return g.ds }

func (g *GPIO) SetPull(m PullMode) { g.pull = m }
func (g *GPIO) SetDS(level int)    { g.ds = level }

var _ Peripheral = (*GPIO)(nil)
