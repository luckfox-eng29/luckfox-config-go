/*
 * SPDX-FileCopyrightText: 2026 Luckfox Team
 *
 * SPDX-License-Identifier: Apache-2.0
 */

package tui

import (
	"github.com/charmbracelet/bubbletea"
	"luckfox-config/internal/peripheral"
)

func newUSBMenu(appCtx *AppContext) ListMenuModel {
	items := []MenuItem{
		{Label: "Device (peripheral)", Value: "peripheral"},
		{Label: "Host", Value: "host"},
	}
	return NewListMenu("USB Mode", items, func(item MenuItem) tea.Cmd {
		p, err := appCtx.Registry.Get("usb")
		if err != nil {
			return Status(err.Error(), true)
		}
		usb := p.(*peripheral.USB)
		appCtx.Cfg.Set("USB", "MODE", item.Value)
		if err := usb.Enable(appCtx.Ctx); err != nil {
			return Status(err.Error(), true)
		}
		return Status("USB mode set to "+item.Value+". Reboot required.", false)
	})
}
