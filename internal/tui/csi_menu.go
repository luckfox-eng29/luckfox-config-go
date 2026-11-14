/*
 * SPDX-FileCopyrightText: 2026 Luckfox Team
 *
 * SPDX-License-Identifier: Apache-2.0
 */

package tui

import (
	"fmt"
	"time"

	"github.com/charmbracelet/bubbletea"
	"luckfox-config/internal/peripheral"
)

func newCSIMenu(appCtx *AppContext) ListMenuModel {
	items := []MenuItem{
		{Label: "Enable", Value: "enable"},
		{Label: "Disable", Value: "disable"},
	}
	return NewListMenu("CSI Configuration", items, func(item MenuItem) tea.Cmd {
		p, err := appCtx.Registry.Get("csi")
		if err != nil {
			return Status(err.Error(), true)
		}
		if item.Value == "disable" {
			if err := p.Disable(appCtx.Ctx); err != nil {
				return Status(err.Error(), true)
			}
			return Loading("CSI disabled. Reboot required.", 1*time.Second)
		}
		return NavigatePush(newCSIUniteMenu(appCtx))
	})
}

func newCSIUniteMenu(appCtx *AppContext) ListMenuModel {
	current := -1
	if p, err := appCtx.Registry.Get("csi"); err == nil {
		current = p.(*peripheral.CSI).UniteEnabled()
	}
	mark := func(label, value string) string {
		if value == fmt.Sprintf("%d", current) {
			return label + " (current)"
		}
		return label
	}
	items := []MenuItem{
		{Label: mark("Disable (standard)", "0"), Value: "0"},
		{Label: mark("Enable (3840x2160)", "1"), Value: "1"},
	}
	return NewListMenu("CSI Unite Mode", items, func(item MenuItem) tea.Cmd {
		p, err := appCtx.Registry.Get("csi")
		if err != nil {
			return Status(err.Error(), true)
		}
		csi := p.(*peripheral.CSI)
		csi.SetUnite(item.Value == "1")
		if err := csi.Enable(appCtx.Ctx); err != nil {
			return Status(err.Error(), true)
		}
		return Loading("CSI enabled. Reboot required.", 1*time.Second)
	})
}
