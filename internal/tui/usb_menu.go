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
	current := ""
	if p, err := appCtx.Registry.Get("usb"); err == nil {
		current = p.(*peripheral.USB).GetMode()
	}
	mark := func(label, mode string) string {
		if mode == current {
			return label + " (current)"
		}
		return label
	}
	items := []MenuItem{
		{Label: mark("Device (peripheral)", "peripheral"), Value: "peripheral"},
		{Label: mark("Host", "host"), Value: "host"},
	}
	return NewListMenu("USB Mode", items, func(item MenuItem) tea.Cmd {
		p, err := appCtx.Registry.Get("usb")
		if err != nil {
			return Status(err.Error(), true)
		}
		usb := p.(*peripheral.USB)
		if err := usb.SetMode(item.Value); err != nil {
			return Status(err.Error(), true)
		}
		if err := usb.Enable(appCtx.Ctx); err != nil {
			return Status(err.Error(), true)
		}
		return Status("USB mode set to "+item.Value+". Reboot required.", false)
	})
}
