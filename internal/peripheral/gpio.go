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
	"luckfox-config/internal/pindiagram"
)

// PullMode represents a GPIO pull resistor configuration.
type PullMode int

const (
	PullNone PullMode = 0
	PullUp   PullMode = 1
	PullDown PullMode = 2
)

// SavePinSetting records one GPIO pin setting in the config store so the
// boot-time load re-applies it. It is the single write path for pin-level
// config, shared by the "gpio" subcommands, the GPIO peripheral and the
// compatible apps: nothing outside the pin layer owns a pin's config.
//
// A pin carries no STATUS flag. The file records boot overrides, and the
// presence of any of pinSettingKeys is the override: a pin is always usable, so
// there is no state for a flag to describe.
//
// module is the pin's config module (a GPIO peripheral's ConfigModule for pins
// the board pin diagram exposes, otherwise the canonical GPIO name). A nil
// store is accepted so callers that never persist stay simple.
func SavePinSetting(cfg *config.Store, module, key, value string) error {
	if cfg == nil {
		return nil
	}
	if err := cfg.Set(module, key, value); err != nil {
		return fmt.Errorf("save %s for %s: %w", key, module, err)
	}
	return nil
}

// pinSettingKeys are the keys a pin override can carry. Any of them makes the
// module an override; see SavePinSetting for why there is no STATUS.
var pinSettingKeys = []string{"PULL", "DRIVE", "MODE"}

// hasPinSetting reports whether a pin config module carries any pin override.
func hasPinSetting(entries map[string]string) bool {
	for _, key := range pinSettingKeys {
		if entries[key] != "" {
			return true
		}
	}
	return false
}

// HasPinSetting reports whether cfg carries any override for the pin module.
func HasPinSetting(cfg *config.Store, module string) bool {
	if cfg == nil {
		return false
	}
	return hasPinSetting(cfg.Module(module))
}

// PullModeName returns the name of a pull mode as written to the config file and
// accepted on the command line.
func PullModeName(m PullMode) string {
	switch m {
	case PullUp:
		return "up"
	case PullDown:
		return "down"
	}
	return "none"
}

// ParsePullMode parses a pull mode name. Besides up/down/none it accepts the
// legacy numeric encoding (0/1/2) written by earlier versions.
func ParsePullMode(s string) (PullMode, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "none", "no", "off", "0":
		return PullNone, nil
	case "up", "1":
		return PullUp, nil
	case "down", "2":
		return PullDown, nil
	}
	return PullNone, fmt.Errorf("invalid pull %q: expected none, up or down", s)
}

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

// GetPull reads the pull resistor currently programmed for a pin. It is the
// counterpart of SetPull: each pin owns the 2 bits [2N+1:2N] of its bank/group
// pull register, 1 meaning pull-up, 2 pull-down and 0 no pull.
func GetPull(c chip.Chip, gpio int) (PullMode, error) {
	pin := board.RawGPIOToPin(gpio)
	base, err := c.GetGPIOPullBase(pin.Bank, pin.Group)
	if err != nil {
		return PullNone, err
	}
	val, err := hwio.ReadReg32(base)
	if err != nil {
		return PullNone, err
	}
	switch (val >> (pin.Number * 2)) & 0x3 {
	case 1:
		return PullUp, nil
	case 2:
		return PullDown, nil
	default:
		return PullNone, nil
	}
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
	mode int // iomux mux mode; 0 is GPIO mode
	// modeSet records whether the config store carries a MODE key for this pin.
	// A pin configured with only a pull resistor or a drive strength says nothing
	// about its mux, so IsActive must not judge it by the iomux mode.
	modeSet bool
	deps    Deps
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
	return strings.ToLower(g.name)
}

func (g *GPIO) IsActive() bool {
	// Only the keys the config actually records are part of the pin's state. A
	// pin configured with just a pull resistor or a drive strength never claimed
	// a mux, and neither the pull nor the drive strength can be read back from
	// the hardware (the ioctl only exposes the mux), so there is nothing left to
	// verify and the recorded state counts as in effect.
	if !g.modeSet {
		return true
	}
	m, err := GetPinMode(g.deps.Chip, g.gpio)
	if err != nil {
		return false
	}
	return m == g.mode
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
	return []string{p + "PULL", p + "DRIVE", p + "MODE"}
}

func (g *GPIO) Enable(ctx context.Context) error {
	// A pin's own configuration — mux mode, pull resistor, drive strength — is
	// independent of whatever else the board routes through the same connector
	// row: those registers are per pin, and writing them does not take the pin
	// away from another interface. A claimed row is therefore reported but never
	// blocks the write, and never aborts the boot-time load.
	if conflict, err := g.deps.Diagram.CheckConflict([]string{g.name}); err != nil {
		return err
	} else if conflict != "" {
		logger.Warn("gpio: pin is claimed by another interface", "pin", g.name, "conflict", conflict)
	}

	// The mux only belongs to us when the config recorded one: a pull-only or
	// drive-only override says nothing about the pin's function, so it must leave
	// whatever the pin currently serves alone.
	if g.modeSet {
		if err := SetPinMode(g.deps.Chip, g.gpio, g.mode); err != nil {
			return err
		}
	}
	if err := SetPull(g.deps.Chip, g.gpio, g.pull); err != nil {
		return err
	}
	if err := SetDriveStrength(g.deps.Chip, g.gpio, g.ds); err != nil {
		return err
	}

	logger.Info("GPIO configured", "pin", g.name, "pull", g.pull, "ds", g.ds)

	// Recorded without a STATUS flag: the module's presence is the override.
	// MODE is only written back once one was configured, so a later boot load
	// does not start claiming a mux this override never claimed.
	pairs := map[string]string{
		"PULL":  PullModeName(g.pull),
		"DRIVE": strconv.Itoa(g.ds),
	}
	if g.modeSet {
		pairs["MODE"] = strconv.Itoa(g.mode)
	}
	return g.deps.Cfg.SetMulti(g.cfgModule(), pairs)
}

func (g *GPIO) Disable(ctx context.Context) error {
	logger.Info("GPIO disabled", "pin", g.name)
	return ReleasePin(g.deps.Chip, g.deps.Cfg, g.deps.Diagram, g.gpio, g.name, g.cfgModule())
}

// ReleasePin puts a pin back into its default state (input, no pull, default
// drive strength) and drops its saved overrides, so the boot-time load stops
// re-applying them. Any GPIO is accepted, including one the board pin diagram
// does not expose; the diagram is only updated when it knows the pin.
//
// name and module must be the ones the pin was recorded under: the pin diagram
// name and the pin's config module respectively. Hardware failures are not
// fatal — the saved override still has to go so the next boot leaves the pin
// alone.
func ReleasePin(chip chip.Chip, cfg *config.Store, diagram *pindiagram.PinDiagram, gpio int, name, module string) error {
	if chip == nil || cfg == nil {
		return fmt.Errorf("release pin %s: missing chip or config store", name)
	}
	if err := ResetPinMode(chip, gpio); err != nil {
		logger.Debug("gpio: reset pin mode failed", "pin", name, "error", err)
	}
	if err := SetPull(chip, gpio, PullNone); err != nil {
		logger.Debug("gpio: clear pin pull failed", "pin", name, "error", err)
	}
	if diagram != nil {
		diagram.MarkPin(name, false)
	}
	// Releasing removes the override outright: the module goes away keys and all,
	// since a pin has no STATUS left to flip.
	return cfg.DeleteModule(module)
}

func (g *GPIO) LoadFromConfig(ctx context.Context, cfg *config.Store, apply bool) error {
	g.deps.Cfg = cfg
	mod := g.cfgModule()
	if !hasPinSetting(cfg.Module(mod)) {
		return nil
	}
	if v := cfg.Get(mod, "PULL"); v != "" {
		p, err := ParsePullMode(v)
		if err != nil {
			logger.Warn("gpio: ignoring unknown saved pull", "module", mod, "value", v)
		} else {
			g.pull = p
		}
	}
	if v := cfg.Get(mod, "DRIVE"); v != "" {
		g.ds, _ = strconv.Atoi(v)
	}
	if v := cfg.Get(mod, "MODE"); v != "" {
		g.mode, _ = strconv.Atoi(v)
		g.modeSet = true
	}
	if apply {
		return g.Enable(ctx)
	}
	return nil
}

// IsConfigEnabled reports whether the config carries an override for the pin.
func (g *GPIO) IsConfigEnabled(cfg *config.Store) bool {
	return hasPinSetting(cfg.Module(g.cfgModule()))
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
