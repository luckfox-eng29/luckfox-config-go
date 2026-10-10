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
	"luckfox-config/internal/config"
	"luckfox-config/internal/logger"
)

// PinReplay re-applies the GPIO pin settings saved by the "gpio" subcommands for
// pins the board does not expose in its pin diagram.
//
// Those pins have no *GPIO peripheral to own their config, so their PULL/DRIVE/
// MODE entries would otherwise be written once and lost on reboot. Pins a GPIO
// peripheral does own are skipped here: that peripheral replays them itself in
// LoadFromConfig.
type PinReplay struct {
	deps Deps
	reg  *Registry
}

// NewPinReplay creates the pin replay peripheral. reg is used to find out which
// pin modules the GPIO peripherals already handle.
func NewPinReplay(deps Deps, reg *Registry) *PinReplay {
	return &PinReplay{deps: deps, reg: reg}
}

func (p *PinReplay) ID() string { return "pinreplay" }

// PinsUsed is nil: the pins it touches come from the config store, not from a
// fixed set.
func (p *PinReplay) PinsUsed() []int { return nil }

// ConfigKeys is nil: the modules it replays depend on what the user configured.
func (p *PinReplay) ConfigKeys() []string { return nil }

func (p *PinReplay) IsActive() bool { return false }

func (p *PinReplay) IsConfigEnabled(*config.Store) bool { return false }

func (p *PinReplay) Enable(context.Context) error { return p.apply() }

func (p *PinReplay) Disable(context.Context) error { return nil }

// LoadFromConfig only replays on the boot-time load. The TUI path passes
// apply=false and must leave the hardware alone.
func (p *PinReplay) LoadFromConfig(_ context.Context, cfg *config.Store, apply bool) error {
	p.deps.Cfg = cfg
	if !apply {
		return nil
	}
	return p.apply()
}

// apply replays every saved pin module that no GPIO peripheral owns.
func (p *PinReplay) apply() error {
	if p.deps.Cfg == nil {
		return nil
	}
	owned := p.ownedModules()
	for _, module := range p.deps.Cfg.Modules() {
		if owned[module] {
			continue
		}
		// Only canonical GPIO pin names describe a pin this can replay.
		if _, err := board.ParseGPIO(module); err != nil {
			continue
		}
		entries := p.deps.Cfg.Module(module)
		if !hasPinSetting(entries) {
			continue
		}
		if err := p.applyModule(module, entries); err != nil {
			return err
		}
	}
	return nil
}

// ownedModules is the set of config modules handled by the board's GPIO
// peripherals.
func (p *PinReplay) ownedModules() map[string]bool {
	out := make(map[string]bool)
	for _, per := range p.reg.All() {
		if g, ok := per.(*GPIO); ok {
			out[g.ConfigModule()] = true
		}
	}
	return out
}

// applyModule writes one saved pin module back to the hardware, in the same
// order GPIO.Enable uses: mux mode, then pull resistor, then drive strength.
func (p *PinReplay) applyModule(module string, entries map[string]string) error {
	pin, err := board.ParseGPIO(module)
	if err != nil {
		return err
	}
	raw := pin.Raw()

	if v := entries["MODE"]; v != "" {
		mode, err := strconv.Atoi(v)
		if err != nil {
			return fmt.Errorf("%s MODE %q: %w", module, v, err)
		}
		if err := SetPinMode(p.deps.Chip, raw, mode); err != nil {
			return fmt.Errorf("restore %s: %w", module, err)
		}
	}
	if v := entries["PULL"]; v != "" {
		mode, err := ParsePullMode(v)
		if err != nil {
			return fmt.Errorf("%s PULL: %w", module, err)
		}
		if err := SetPull(p.deps.Chip, raw, mode); err != nil {
			return fmt.Errorf("restore %s: %w", module, err)
		}
	}
	if v := entries["DRIVE"]; v != "" {
		level, err := strconv.Atoi(v)
		if err != nil {
			return fmt.Errorf("%s DRIVE %q: %w", module, v, err)
		}
		if err := SetDriveStrength(p.deps.Chip, raw, level); err != nil {
			return fmt.Errorf("restore %s: %w", module, err)
		}
	}

	logger.Info("GPIO pin config restored", "module", module)
	return nil
}
