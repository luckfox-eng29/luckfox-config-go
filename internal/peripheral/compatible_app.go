/*
 * SPDX-FileCopyrightText: 2026 Luckfox Team
 *
 * SPDX-License-Identifier: Apache-2.0
 */

package peripheral

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"luckfox-config/internal/board"
	"luckfox-config/internal/config"
	"luckfox-config/internal/logger"
)

// CompatibleApp represents a preset hardware combination (e.g. DHT11, Pico_LCD).
//
// Unlike other peripherals it does not own any config keys. Enabling it simply
// (1) checks that its declared pins are free, (2) turns on the real peripherals
// it needs through the regular registry path, and (3) applies pin pulls and
// device-tree node toggles. Pins that are only "declared" (no hardware action)
// are checked for occupancy but never physically released.
type CompatibleApp struct {
	spec    board.CompatibleAppSpec
	boardID string
	deps    Deps
	reg     *Registry
}

func NewCompatibleApp(spec board.CompatibleAppSpec, boardID string, deps Deps, reg *Registry) *CompatibleApp {
	return &CompatibleApp{spec: spec, boardID: boardID, deps: deps, reg: reg}
}

func (c *CompatibleApp) ID() string { return "app_" + c.spec.Name }

// IsActive is derived: every real peripheral the app enables must be active and
// every enabled node must be okay. Apps without any of those are never active.
func (c *CompatibleApp) IsActive() bool {
	if len(c.spec.Enable) == 0 && len(c.spec.Nodes) == 0 {
		return false
	}
	for _, id := range c.spec.Enable {
		p, err := c.reg.Get(id)
		if err != nil || !p.IsActive() {
			return false
		}
	}
	for _, n := range c.spec.Nodes {
		if n.Status == "okay" && !IsNodeEnabled(n.Path) {
			return false
		}
	}
	return true
}

// IsConfigEnabled is derived from the sub-peripherals the app depends on.
func (c *CompatibleApp) IsConfigEnabled(cfg *config.Store) bool {
	if len(c.spec.Enable) == 0 {
		return false
	}
	for _, id := range c.spec.Enable {
		p, err := c.reg.Get(id)
		if err != nil || !p.IsConfigEnabled(cfg) {
			return false
		}
	}
	return true
}

func (c *CompatibleApp) PinsUsed() []int { return nil }

func (c *CompatibleApp) ConfigKeys() []string { return nil }

// Enable checks pin occupancy, then enables the app's real peripherals and
// applies its pin pulls / node toggles.
func (c *CompatibleApp) Enable(ctx context.Context) error {
	if err := c.deps.Diagram.CheckConflictErrExcluding(c.requiredTokens(), c.claimedTokens()); err != nil {
		return err
	}

	c.applySPISignals()

	for _, id := range c.spec.Enable {
		p, err := c.reg.Get(id)
		if err != nil {
			// Missing target (e.g. i2c0m2 on a board that does not list it).
			// Not fatal: the rest of the app still comes up.
			logger.Warn("compatible app sub-peripheral missing", "app", c.spec.Name, "id", id, "error", err.Error())
			continue
		}
		if err := p.Enable(ctx); err != nil {
			return fmt.Errorf("%s: %w", id, err)
		}
	}

	if err := c.applyPinSettings(); err != nil {
		return err
	}
	if err := c.applyNodes(); err != nil {
		return err
	}

	logger.Info("Compatible app enabled", "name", c.spec.Name, "board", c.boardID)
	return nil
}

// applySPISignals narrows the SPI signal set to what the app actually wires up,
// before the SPI peripheral claims its pins. Without it an app would always come
// up with the interactive default (CS0 + MISO) instead of the subset the shell
// configures for the same app. SPI.Enable persists the selection in the
// CS_ENABLE / MISO_ENABLE config keys, so it survives a reboot.
func (c *CompatibleApp) applySPISignals() {
	if c.spec.SPI == nil {
		return
	}
	for _, id := range c.spec.Enable {
		p, err := c.reg.Get(id)
		if err != nil {
			continue
		}
		spi, ok := p.(*SPI)
		if !ok {
			continue
		}
		spi.SetSignalUsage(c.spec.SPI.CS, c.spec.SPI.MISO)
		logger.Info("Compatible app applied SPI signal usage", "name", c.spec.Name, "id", id,
			"cs", c.spec.SPI.CS, "miso", c.spec.SPI.MISO)
		return
	}
	logger.Warn("compatible app declares spi signals but enables no spi", "app", c.spec.Name)
}

// Check reports whether the app's required pins are currently free: the
// peripheral pins it declares in check[] plus the GPIOs it depends on. It
// performs no hardware change. The returned bool is true when all are free.
func (c *CompatibleApp) Check() (string, bool) {
	conflict, err := c.deps.Diagram.CheckConflictExcluding(c.requiredTokens(), c.claimedTokens())
	if err != nil {
		return err.Error(), false
	}
	if conflict != "" {
		return fmt.Sprintf("pin conflict: %s is already in use", conflict), false
	}
	return "all required pins are free", true
}

// Ready reports whether the app is up and correct right now: every interface it
// enables is active, none of the pins it depends on is occupied, and every pin
// mux mode and pull it declares is in force. All three must hold; the app list
// marks passing entries with "(Ready)".
//
// The pin condition reads the iomux and pull registers back, so it can only pass
// after Enable has applied them.
func (c *CompatibleApp) Ready() bool {
	if _, free := c.Check(); !free {
		return false
	}
	if !c.IsActive() {
		return false
	}
	return c.pinStateApplied()
}

// pinStateApplied reports whether the mux function and pull resistors the app
// declares are the ones currently programmed. A pin that cannot be parsed or
// read counts as not applied: a setting that cannot be verified is not in force.
func (c *CompatibleApp) pinStateApplied() bool {
	for _, g := range c.spec.GPIO {
		wantPull, hasPull := parsePull(g.Pull)
		if g.Mode == nil && !hasPull {
			continue // nothing declared for this pin
		}
		pin, err := board.ParseGPIO(g.Pin)
		if err != nil {
			logger.Debug("compatible app: unparsable pin", "app", c.spec.Name, "pin", g.Pin, "error", err)
			return false
		}
		if g.Mode != nil {
			got, err := GetPinMode(c.deps.Chip, pin.Raw())
			if err != nil {
				logger.Debug("compatible app: pin mode unreadable", "app", c.spec.Name, "pin", g.Pin, "error", err)
				return false
			}
			if got != *g.Mode {
				return false
			}
		}
		if !hasPull {
			continue
		}
		got, err := GetPull(c.deps.Chip, pin.Raw())
		if err != nil {
			logger.Debug("compatible app: pin pull unreadable", "app", c.spec.Name, "pin", g.Pin, "error", err)
			return false
		}
		if got != wantPull {
			return false
		}
	}
	return true
}

// Disable is not supported: a compatible app has no state of its own. Callers
// should turn off its sub-peripherals individually.
func (c *CompatibleApp) Disable(ctx context.Context) error {
	return fmt.Errorf("compatible app %q cannot be disabled; turn off its sub-peripherals individually", c.spec.Name)
}

// LoadFromConfig only refreshes the config store. A compatible app owns no
// config keys of its own: its state is derived from the sub-peripherals it
// enables.
func (c *CompatibleApp) LoadFromConfig(ctx context.Context, cfg *config.Store, apply bool) error {
	c.deps.Cfg = cfg
	return nil
}

// ─── helpers ──────────────────────────────────────────────────────────────────

var appIDRe = regexp.MustCompile(`^(pwm|uart|i2c|spi)(\d+)m(\d+)$`)

// expandID turns a peripheral ID (e.g. "spi0m0") into the pin-diagram tokens it
// occupies (e.g. "SPI0_M0_CLK"). Unknown IDs are returned unchanged.
func expandID(id string) []string {
	m := appIDRe.FindStringSubmatch(id)
	if m == nil {
		return []string{id}
	}
	kind, n, mux := m[1], m[2], m[3]
	switch kind {
	case "pwm":
		return []string{"PWM" + n + "_M" + mux}
	case "uart":
		return []string{"UART" + n + "_M" + mux + "_TX", "UART" + n + "_M" + mux + "_RX"}
	case "i2c":
		return []string{"I2C" + n + "_M" + mux + "_SDA", "I2C" + n + "_M" + mux + "_SCL"}
	case "spi":
		return []string{
			"SPI" + n + "_M" + mux + "_CLK",
			"SPI" + n + "_M" + mux + "_MOSI",
			"SPI" + n + "_M" + mux + "_MISO",
			"SPI" + n + "_M" + mux + "_CS0",
		}
	}
	return []string{id}
}

// checkTokens are the pin-diagram tokens the app declares as occupied. They
// form the candidate set of the occupancy check.
func (c *CompatibleApp) checkTokens() []string {
	return expandIDs(c.spec.Check)
}

// requiredTokens is everything that must be free before the app can be brought
// up: the pins it declares in check[] plus the GPIOs it depends on.
func (c *CompatibleApp) requiredTokens() []string {
	return append(c.checkTokens(), c.gpioTokens()...)
}

// gpioTokens are the pin-diagram tokens of the GPIOs the app depends on. The
// app never marks them itself (applyPullUps only writes the pull register), so
// an occupied entry means some other consumer owns that physical pin.
func (c *CompatibleApp) gpioTokens() []string {
	out := make([]string, 0, len(c.spec.GPIO))
	for _, g := range c.spec.GPIO {
		out = append(out, g.Pin)
	}
	return out
}

// claimedTokens are the tokens the app claims for itself: its check[] pins AND
// its enable[] targets. Both are excluded from the occupancy check so the app
// never conflicts with itself. Omitting check[] here made an app that lists one
// of its own enable pins in check[] report a phantom conflict (e.g. Pico_LCD
// enables spi0m0 and checks pwm0m0, so an active PWM0_M0 failed the check).
func (c *CompatibleApp) claimedTokens() []string {
	out := expandIDs(c.spec.Check)
	return append(out, expandIDs(c.spec.Enable)...)
}

// expandIDs flattens a list of peripheral IDs into pin-diagram tokens.
func expandIDs(ids []string) []string {
	var out []string
	for _, id := range ids {
		out = append(out, expandID(id)...)
	}
	return out
}

// applyPinSettings writes the app's per-pin settings: the iomux function it
// declares, then its pull resistor. Enabling the app is the app's job; the pin
// layer records both settings at the pin level, so the app owns no config keys
// of its own and the boot-time pin replay re-applies them.
func (c *CompatibleApp) applyPinSettings() error {
	for _, g := range c.spec.GPIO {
		pull, hasPull := parsePull(g.Pull)
		if g.Mode == nil && !hasPull {
			continue
		}
		pin, err := board.ParseGPIO(g.Pin)
		if err != nil {
			return fmt.Errorf("pin %s: %w", g.Pin, err)
		}
		// The mux goes first: a pull resistor on a pin that still serves another
		// function is written to a pin the app does not own yet.
		if g.Mode != nil {
			if err := SetPinMode(c.deps.Chip, pin.Raw(), *g.Mode); err != nil {
				return fmt.Errorf("mode %s: %w", g.Pin, err)
			}
			if err := SavePinSetting(c.deps.Cfg, pin.String(), "MODE", strconv.Itoa(*g.Mode)); err != nil {
				return fmt.Errorf("mode %s: %w", g.Pin, err)
			}
		}
		if !hasPull {
			continue
		}
		if err := SetPull(c.deps.Chip, pin.Raw(), pull); err != nil {
			return fmt.Errorf("pull %s: %w", g.Pin, err)
		}
		if err := SavePinSetting(c.deps.Cfg, pin.String(), "PULL", PullModeName(pull)); err != nil {
			return fmt.Errorf("pull %s: %w", g.Pin, err)
		}
	}
	return nil
}

func parsePull(s string) (PullMode, bool) {
	switch strings.ToLower(s) {
	case "up":
		return PullUp, true
	case "down":
		return PullDown, true
	case "none":
		return PullNone, true
	}
	return PullNone, false
}

func (c *CompatibleApp) applyNodes() error {
	for _, n := range c.spec.Nodes {
		overlayID := strings.Trim(strings.ReplaceAll(n.Path, "/", "_"), "_")
		dts := fmt.Sprintf("/dts-v1/;\n/plugin/;\n\n&{%s} {\n\tstatus = \"%s\";\n};\n", n.Path, n.Status)
		if err := c.deps.Dynamic.Apply(overlayID, dts); err != nil {
			return fmt.Errorf("node %s: %w", n.Path, err)
		}
	}
	return nil
}

var _ Peripheral = (*CompatibleApp)(nil)
