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

type USB struct {
	deps Deps
}

func NewUSB(deps Deps) *USB {
	return &USB{deps: deps}
}

func (u *USB) ID() string      { return "usb" }
func (u *USB) PinsUsed() []int { return nil }
func (u *USB) ConfigKeys() []string {
	return []string{"USB_MODE"}
}

func (u *USB) IsActive() bool {
	return true
}

func (u *USB) IsConfigEnabled(cfg *config.Store) bool {
	return cfg.Get("USB", "MODE") != ""
}

func (u *USB) Enable(ctx context.Context) error {
	mode := u.deps.Cfg.Get("USB", "MODE")
	if mode == "" {
		mode = "peripheral"
	}
	return u.applyMode(mode)
}

func (u *USB) Disable(ctx context.Context) error {
	return u.applyMode("peripheral")
}

func (u *USB) applyMode(mode string) error {
	if u.deps.Static == nil {
		return fmt.Errorf("static overlay not available")
	}

	dts := fmt.Sprintf(`/dts-v1/;
/plugin/;

&{/usbdrd/usb@ffb00000} {
	dr_mode = "%s";
};
`, mode)

	if err := u.deps.Static.Apply(dts); err != nil {
		return err
	}

	logger.Info("USB mode set", "mode", mode)
	return u.deps.Cfg.Set("USB", "MODE", mode)
}

func (u *USB) LoadFromConfig(ctx context.Context, cfg *config.Store, apply bool) error {
	u.deps.Cfg = cfg
	if cfg.Get("USB", "MODE") == "" {
		return nil
	}
	if apply {
		return u.Enable(ctx)
	}
	return nil
}

func (u *USB) GetMode() string {
	return u.deps.Cfg.Get("USB", "MODE")
}

var _ Peripheral = (*USB)(nil)
