/*
 * SPDX-FileCopyrightText: 2026 Luckfox Team
 *
 * SPDX-License-Identifier: Apache-2.0
 */

package peripheral

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"

	"luckfox-config/internal/board"
	"luckfox-config/internal/config"
	"luckfox-config/internal/logger"
)

// pwmModes maps (controller, channel) to iomux mode value.
// PWM0 channels start at mode 45, PWM1 at mode 49.
func pwmMode(controller, channel int) int {
	if controller == 0 {
		return 45 + channel
	}
	return 49 + channel
}

// PWM manages a single PWM channel via dynamic DTS overlay.
type PWM struct {
	controller int // 0 or 1 (only for RMIO mode)
	channel    int // 0-7 (PWM1), 0-3 (PWM0) in RMIO mode; or 0-11 in non-RMIO mode
	mux        int // Only used in non-RMIO mode
	deps       Deps
	pin        int // RM_IO, -1 if unset (only for RMIO mode)
}

func NewPWM(controller, channel int, deps Deps) *PWM {
	return &PWM{controller: controller, channel: channel, mux: 0, deps: deps, pin: -1}
}

func (p *PWM) ID() string {
	if strings.ToLower(p.deps.ConfigMode) == "rmio" {
		return fmt.Sprintf("pwm%d_ch%d", p.controller, p.channel)
	}
	if p.deps.PinctrlNaming == "v2" {
		return fmt.Sprintf("pwm%d_%dm%d", p.controller, p.channel, p.mux)
	}
	return fmt.Sprintf("pwm%dm%d", p.channel, p.mux)
}

func (p *PWM) pinName() string {
	if p.deps.PinctrlNaming == "v2" {
		return fmt.Sprintf("PWM%d_M%d_CH%d", p.controller, p.mux, p.channel)
	}
	return fmt.Sprintf("PWM%d_M%d", p.channel, p.mux)
}

func (p *PWM) configKey() string {
	if strings.ToLower(p.deps.ConfigMode) == "rmio" {
		return fmt.Sprintf("PWM%d_M%d", p.controller, p.channel)
	}
	if p.deps.PinctrlNaming == "v2" {
		return fmt.Sprintf("PWM%d_%d_M%d", p.controller, p.channel, p.mux)
	}
	return fmt.Sprintf("PWM%d_M%d", p.channel, p.mux)
}

func (p *PWM) PinsUsed() []int {
	if strings.ToLower(p.deps.ConfigMode) == "rmio" {
		if p.pin < 0 {
			return nil
		}
		return []int{p.pin}
	}
	return nil
}

func (p *PWM) ConfigKeys() []string {
	k := p.configKey() + "_"
	if strings.ToLower(p.deps.ConfigMode) == "rmio" {
		return []string{k + "STATUS", k + "RM_IO"}
	}
	return []string{k + "STATUS"}
}

func (p *PWM) buildDTS(fullPath, status string) string {
	target := fullPath
	if !strings.HasPrefix(target, "/") {
		target = "&" + target
	} else {
		target = "&{" + target + "}"
	}

	pinctrl := ""
	if status == "okay" && strings.ToLower(p.deps.ConfigMode) != "rmio" {
		pinctrlName := fmt.Sprintf("pwm%dm%d_pins", p.channel, p.mux)
		if p.deps.PinctrlNaming == "v2" {
			pinctrlName = fmt.Sprintf("pwm%dm%d_ch%d_pins", p.controller, p.mux, p.channel)
		}
		pinctrl = fmt.Sprintf("\tpinctrl-0 = <&%s>;\n", pinctrlName)
	}

	return fmt.Sprintf(`/dts-v1/;
/plugin/;

%s {
%s	status = "%s";
};
`, target, pinctrl, status)
}

func (p *PWM) Enable(ctx context.Context) error {
	if strings.ToLower(p.deps.ConfigMode) == "rmio" {
		if p.pin < 0 {
			return fmt.Errorf("%s: pin must be set before enabling", p.ID())
		}

		pinName := board.RMIOName(p.pin)
		if err := p.deps.Diagram.CheckConflictErr([]string{pinName}); err != nil {
			return err
		}

		alias := p.deps.Chip.PWMAlias(p.controller, p.channel)
		fullPath, err := p.deps.ResolveAlias(alias)
		if err != nil {
			fullPath = alias
		}

		dts := p.buildDTS(fullPath, "okay")
		if err := p.deps.Dynamic.Apply(p.ID(), dts); err != nil {
			return err
		}

		if err := SetPinMode(p.deps.Chip, p.deps.MapRMIOToGPIO(p.pin), pwmMode(p.controller, p.channel)); err != nil {
			return err
		}

		p.deps.Diagram.MarkPin(pinName, true)

		logger.Info("PWM enabled", "ctrl", p.controller, "ch", p.channel, "pin", pinName)

		mod := p.configKey()
		return p.deps.Cfg.SetMulti(mod, map[string]string{
			"STATUS": "1",
			"RM_IO":  strconv.Itoa(p.pin),
		})
	}

	// Non-RMIO mode
	pinName := p.pinName()
	if err := p.deps.Diagram.CheckConflictErr([]string{pinName}); err != nil {
		return err
	}

	alias := p.deps.Chip.PWMAlias(p.controller, p.channel)
	fullPath, err := p.deps.ResolveAlias(alias)
	if err != nil {
		fullPath = alias
	}

	dts := p.buildDTS(fullPath, "okay")
	if err := p.deps.Dynamic.Apply(p.ID(), dts); err != nil {
		return err
	}

	p.deps.Diagram.MarkPin(pinName, true)

	logger.Info("PWM enabled", "ctrl", p.controller, "ch", p.channel, "mux", p.mux)

	mod := p.configKey()
	return p.deps.Cfg.Set(mod, "STATUS", "1")
}

func (p *PWM) Disable(ctx context.Context) error {
	if strings.ToLower(p.deps.ConfigMode) == "rmio" {
		alias := p.deps.Chip.PWMAlias(p.controller, p.channel)
		fullPath, err := p.deps.ResolveAlias(alias)
		if err != nil {
			fullPath = alias
		}

		dts := p.buildDTS(fullPath, "disabled")
		if err := p.deps.Dynamic.Apply(p.ID(), dts); err != nil {
			return err
		}

		if p.pin >= 0 {
			_ = ResetPinMode(p.deps.Chip, p.deps.MapRMIOToGPIO(p.pin))
			p.deps.Diagram.MarkPin(board.RMIOName(p.pin), false)
			p.pin = -1
		}

		logger.Info("PWM disabled", "ctrl", p.controller, "ch", p.channel)

		return p.deps.Cfg.Set(p.configKey(), "STATUS", "0")
	}

	// Non-RMIO mode
	alias := p.deps.Chip.PWMAlias(p.controller, p.channel)
	fullPath, err := p.deps.ResolveAlias(alias)
	if err != nil {
		fullPath = alias
	}

	dts := p.buildDTS(fullPath, "disabled")
	if err := p.deps.Dynamic.Apply(p.ID(), dts); err != nil {
		return err
	}

	pinName := p.pinName()
	p.deps.Diagram.MarkPin(pinName, false)

	logger.Info("PWM disabled", "ctrl", p.controller, "ch", p.channel, "mux", p.mux)

	return p.deps.Cfg.Set(p.configKey(), "STATUS", "0")
}

func (p *PWM) LoadFromConfig(ctx context.Context, cfg *config.Store, apply bool) error {
	p.deps.Cfg = cfg
	mod := p.configKey()

	if strings.ToLower(p.deps.ConfigMode) == "rmio" {
		if cfg.Get(mod, "STATUS") != "1" {
			if !apply && p.IsActive() {
				p.deps.Diagram.MarkPin(board.RMIOName(p.pin), true)
			}
			return nil
		}
		pinStr := cfg.Get(mod, "RM_IO")
		if pinStr == "" {
			return nil
		}
		pin, err := strconv.Atoi(pinStr)
		if err != nil {
			return err
		}
		p.pin = pin
		if apply {
			return p.Enable(ctx)
		}
		p.deps.Diagram.MarkPin(board.RMIOName(p.pin), true)
		return nil
	}

	// Non-RMIO mode
	if cfg.Get(mod, "STATUS") != "1" {
		if !apply && p.IsActive() {
			p.deps.Diagram.MarkPin(p.pinName(), true)
		}
		return nil
	}
	if apply {
		return p.Enable(ctx)
	}
	p.deps.Diagram.MarkPin(p.pinName(), true)
	return nil
}

func (p *PWM) IsActive() bool {
	// Check if the ConfigFS overlay exists for this peripheral
	overlayPath := "/sys/kernel/config/device-tree/overlays/" + p.ID()
	if _, err := os.Stat(overlayPath); err == nil {
		return true
	}
	return false
}

func (p *PWM) IsConfigEnabled(cfg *config.Store) bool {
	return cfg.Get(p.configKey(), "STATUS") == "1"
}

func (p *PWM) SetPin(rmio int) { p.pin = rmio }
func (p *PWM) SetMux(mux int)  { p.mux = mux }

var _ Peripheral = (*PWM)(nil)
