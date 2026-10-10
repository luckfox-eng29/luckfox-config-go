/*
 * SPDX-FileCopyrightText: 2026 Luckfox Team
 *
 * SPDX-License-Identifier: Apache-2.0
 */

package peripheral

import (
	"context"
	"errors"
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
	return p.groupKey(p.mux)
}

// groupKey returns the config key of the mux group `mux` of p's controller and
// channel. The mux groups of one controller share a single device tree node, so
// their keys only differ in the mux number.
func (p *PWM) groupKey(mux int) string {
	if p.deps.PinctrlNaming == "v2" {
		return fmt.Sprintf("PWM%d_%d_M%d", p.controller, p.channel, mux)
	}
	return fmt.Sprintf("PWM%d_M%d", p.channel, mux)
}

// maxMuxGroups bounds the search for sibling mux groups (M0..M7).
const maxMuxGroups = 8

// ErrMuxGroupBusy reports that another mux group of the same PWM controller is
// enabled. The mux groups of one controller share a single device tree node, so
// enabling a second group would overwrite the first group's status property and
// silently stop it.
var ErrMuxGroupBusy = errors.New("only one PWM mux group per controller can be enabled")

// busyGroup returns the config key of the enabled sibling mux group occupying
// the same device tree node as p, or "" when none is enabled.
func (p *PWM) busyGroup() string {
	for mux := 0; mux < maxMuxGroups; mux++ {
		if mux == p.mux {
			continue
		}
		key := p.groupKey(mux)
		if p.deps.Cfg.Get(key, "STATUS") == "1" {
			return key
		}
	}
	return ""
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
	if status == "okay" {
		if strings.ToLower(p.deps.ConfigMode) == "rmio" {
			// RMIO: pin mux done via iomux, overlay references the per-pin
			// pinctrl node like the shell script does.
			pinctrl = p.rmioPinctrl()
		} else if s := p.deps.pinSnippet(p.ID(), []string{p.deps.pinName("pwm", p.controller, p.mux, p.channel)}, nil); s != "" {
			pinctrl = "\t" + s + "\n"
		}
	}

	return fmt.Sprintf(`/dts-v1/;
/plugin/;

%s {
%s	status = "%s";
};
`, target, pinctrl, status)
}

// rmioPinctrl returns the overlay snippet referencing the per-pin pinctrl
// node, mirroring the shell script (pinctrl-names + pinctrl-0 = <phandle>).
func (p *PWM) rmioPinctrl() string {
	if p.pin < 0 {
		return ""
	}
	name := fmt.Sprintf("rm-io%d-pwm%d-ch%d", p.pin, p.controller, p.channel)
	if phandle, err := FindNodePhandle(name); err == nil {
		return fmt.Sprintf("\tpinctrl-names = \"active\";\n\tpinctrl-0 = <%d>;\n", phandle)
	}
	logger.Warn("pinctrl node not found, overlay without pinctrl-0", "node", name)
	return ""
}

func (p *PWM) Enable(ctx context.Context) error {
	// A channel the base device tree already enables from boot is owned by the
	// system: writing our overlay would trigger a revert on re-apply and
	// reverting panics this BSP kernel, so leave it alone.
	if p.SystemReserved() {
		logger.Warn("PWM enabled by the base device tree, enable skipped", "id", p.ID())
		return nil
	}

	if strings.ToLower(p.deps.ConfigMode) == "rmio" {
		if p.pin < 0 {
			return fmt.Errorf("%s: pin must be set before enabling", p.ID())
		}

		pinName := board.RMIOName(p.pin)
		if err := p.deps.Diagram.CheckConflictErrSelf([]string{pinName}); err != nil {
			return err
		}

		alias := p.deps.Chip.PWMAlias(p.controller, p.channel)
		fullPath, err := p.deps.ResolveAlias(alias)
		if err != nil {
			fullPath = alias
		}

		// The pwm platform device may already exist (enabled in the base device
		// tree, or enabled earlier in this boot). Dynamic.Apply reverts the
		// existing overlay before writing the new one, and reverting tears the
		// pwm platform device down - which panics this RK3506 BSP kernel in
		// pwmchip_remove(). Skip the overlay and refresh the pin mux only.
		if IsNodeEnabled(fullPath) {
			logger.Info("PWM node already enabled, overlay apply skipped", "id", p.ID(), "node", fullPath)
		} else {
			dts := p.buildDTS(fullPath, "okay")
			if err := p.deps.Dynamic.Apply(p.ID(), dts); err != nil {
				return err
			}
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
	// The mux groups of one controller share a single device tree node, so only
	// one of them may be enabled at a time.
	if other := p.busyGroup(); other != "" {
		return fmt.Errorf("%s: %w (%s), disable it first", p.configKey(), ErrMuxGroupBusy, other)
	}

	pinName := p.pinName()
	if err := p.deps.Diagram.CheckConflictErrSelf([]string{pinName}); err != nil {
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

		// Never revert the overlay here on RK3506: the revert makes the kernel
		// destroy the pwm platform device and this BSP kernel panics in
		// pwmchip_remove() (the vendor shell script oopses the same way). Only
		// release the pin and record the config; the device tree node falls back
		// to its base state on the next reboot.
		if IsNodeEnabled(fullPath) {
			logger.Warn("PWM stays enabled until reboot (kernel pwmchip teardown bug), overlay not reverted",
				"id", p.ID(), "node", fullPath)
		}

		if p.pin >= 0 {
			_ = ResetPinModeIfMode7(p.deps.Chip, p.deps.MapRMIOToGPIO(p.pin))
			p.deps.Diagram.MarkPin(board.RMIOName(p.pin), false)
			p.pin = -1
		}

		logger.Info("PWM disabled (takes effect after reboot)", "ctrl", p.controller, "ch", p.channel)

		return p.deps.Cfg.Set(p.configKey(), "STATUS", "0")
	}

	// Non-RMIO mode
	pinName := p.pinName()

	// A group that is neither enabled nor active has nothing to switch off, and
	// its status="disabled" overlay would land on the device tree node shared
	// with the sibling group that is actually enabled, silently stopping it.
	if p.deps.Cfg.Get(p.configKey(), "STATUS") != "1" && !p.IsActive() {
		logger.Info("PWM already disabled, overlay not applied", "id", p.ID())
		p.deps.Diagram.MarkPin(pinName, false)
		return p.deps.Cfg.Set(p.configKey(), "STATUS", "0")
	}

	alias := p.deps.Chip.PWMAlias(p.controller, p.channel)
	fullPath, err := p.deps.ResolveAlias(alias)
	if err != nil {
		fullPath = alias
	}

	dts := p.buildDTS(fullPath, "disabled")
	if err := p.deps.Dynamic.Apply(p.ID(), dts); err != nil {
		return err
	}

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
		// A config that enables two mux groups of one controller at once (hand
		// edited, or written by an older version) is inconsistent. Skip the group
		// that loses the shared device tree node instead of failing: an error here
		// aborts LoadAll and leaves every peripheral registered after PWM unapplied.
		if err := p.Enable(ctx); err != nil {
			if errors.Is(err, ErrMuxGroupBusy) {
				logger.Warn("PWM mux group skipped, another group is enabled", "id", p.ID(), "error", err)
				return nil
			}
			return err
		}
		return nil
	}
	p.deps.Diagram.MarkPin(p.pinName(), true)
	return nil
}

func (p *PWM) IsActive() bool {
	overlayPath := "/sys/kernel/config/device-tree/overlays/" + p.ID()
	_, overlayErr := os.Stat(overlayPath)
	hasOverlay := overlayErr == nil

	alias := p.deps.Chip.PWMAlias(p.controller, p.channel)
	nodeEnabled := false
	if fullPath, err := p.deps.ResolveAlias(alias); err == nil {
		nodeEnabled = IsNodeEnabled(fullPath)
	}

	if strings.ToLower(p.deps.ConfigMode) == "rmio" {
		// RMIO (RK3506): the base device tree may enable the node from boot,
		// in which case Enable() never creates an overlay for it, so a still
		// enabled node counts as active on its own. Disable() intentionally
		// never reverts the overlay (the BSP kernel panics in pwmchip_remove),
		// so this keeps reporting enabled until reboot.
		return hasOverlay || nodeEnabled
	}

	// Non-RMIO: the mux groups (M0, M1, ...) of one controller share a single
	// device tree node, so the node status alone cannot tell them apart.
	// Require our per-mux overlay to be present AND the node to be enabled:
	// Disable() applies a status="disabled" overlay and leaves its directory
	// behind, so neither signal is sufficient on its own.
	return hasOverlay && nodeEnabled
}

// SystemReserved reports whether the base device tree already enables the pwm
// node, i.e. the channel is owned by the system from boot instead of by a
// dynamic overlay of ours. Such a channel can neither be claimed nor released
// at runtime - reverting its node panics this BSP kernel in pwmchip_remove() -
// so the menus show it as not selectable.
func (p *PWM) SystemReserved() bool {
	if strings.ToLower(p.deps.ConfigMode) != "rmio" {
		return false
	}
	// An overlay of ours owns the node, so it is not a system channel.
	if _, err := os.Stat("/sys/kernel/config/device-tree/overlays/" + p.ID()); err == nil {
		return false
	}
	alias := p.deps.Chip.PWMAlias(p.controller, p.channel)
	fullPath, err := p.deps.ResolveAlias(alias)
	if err != nil {
		return false
	}
	return IsNodeEnabled(fullPath)
}

func (p *PWM) IsConfigEnabled(cfg *config.Store) bool {
	return cfg.Get(p.configKey(), "STATUS") == "1"
}

func (p *PWM) SetPin(rmio int) { p.pin = rmio }
func (p *PWM) SetMux(mux int)  { p.mux = mux }

var _ Peripheral = (*PWM)(nil)
