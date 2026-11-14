/*
 * SPDX-FileCopyrightText: 2026 Luckfox Team
 *
 * SPDX-License-Identifier: Apache-2.0
 */

package peripheral

import (
	"context"
	"fmt"

	"luckfox-config/internal/config"
	"luckfox-config/internal/logger"
)

// usbDrModeProp is the property that selects the role of the OTG controller.
const usbDrModeProp = "dr_mode"

// USB manages the role of the USB OTG controller through a static device tree
// overlay.
//
// The controller node comes from the board description because the device tree
// layout differs per SoC. The current role is read back from the running device
// tree, never from the config store.
type USB struct {
	deps Deps
	node string // USB OTG controller node
	mode string // role to be written by the next Enable
}

func NewUSB(deps Deps, node string) *USB {
	return &USB{deps: deps, node: node}
}

func (u *USB) ID() string      { return "usb" }
func (u *USB) PinsUsed() []int { return nil }

// ConfigKeys returns nil: the USB role is described by the device tree, not by
// the config store.
func (u *USB) ConfigKeys() []string { return nil }

// IsActive reports whether the running device tree puts the controller into host
// mode. The node itself is always present, so its status says nothing.
func (u *USB) IsActive() bool {
	return u.GetMode() == usbModeHost
}

// IsConfigEnabled is always false: USB has no config store representation.
func (u *USB) IsConfigEnabled(*config.Store) bool { return false }

func (u *USB) Enable(ctx context.Context) error {
	mode := u.mode
	if mode == "" {
		mode = u.GetMode()
	}
	if mode == "" {
		mode = usbModePeripheral
	}
	return u.applyMode(mode)
}

func (u *USB) Disable(ctx context.Context) error {
	return u.applyMode(usbModePeripheral)
}

const (
	usbModePeripheral = "peripheral"
	usbModeHost       = "host"
)

// validUSBModes are the dr_mode values the USB controller accepts.
var validUSBModes = map[string]bool{usbModePeripheral: true, usbModeHost: true}

// SetMode selects the role for the next Enable call. It only touches memory;
// the device tree is written by Enable.
func (u *USB) SetMode(mode string) error {
	if !validUSBModes[mode] {
		return fmt.Errorf("invalid USB mode %q: must be %s or %s", mode, usbModePeripheral, usbModeHost)
	}
	u.mode = mode
	return nil
}

func (u *USB) applyMode(mode string) error {
	if !validUSBModes[mode] {
		return fmt.Errorf("invalid USB mode %q: must be %s or %s", mode, usbModePeripheral, usbModeHost)
	}
	if u.deps.Static == nil {
		return fmt.Errorf("static overlay not available")
	}

	dts := fmt.Sprintf(`/dts-v1/;
/plugin/;

&{%s} {
	%s = "%s";
};
`, u.node, usbDrModeProp, mode)

	if err := u.deps.Static.Apply(dts); err != nil {
		return err
	}

	u.mode = mode
	logger.Info("USB mode set", "mode", mode)
	return nil
}

// LoadFromConfig syncs the in-memory role with the running device tree. Static
// overlays are not replayed: the device tree already holds the configuration,
// so load must not rewrite the boot partition.
func (u *USB) LoadFromConfig(ctx context.Context, cfg *config.Store, apply bool) error {
	u.deps.Cfg = cfg
	u.mode = u.GetMode()
	return nil
}

// GetMode returns the role the running device tree configures, or an empty
// string when the property is absent.
func (u *USB) GetMode() string {
	mode, err := GetNodeString(u.node, usbDrModeProp)
	if err != nil {
		return ""
	}
	return mode
}

var _ Peripheral = (*USB)(nil)
