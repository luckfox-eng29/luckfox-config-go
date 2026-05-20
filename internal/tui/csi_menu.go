/*
 * SPDX-FileCopyrightText: 2026 Luckfox Team
 *
 * SPDX-License-Identifier: Apache-2.0
 */

package tui

import (
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
	items := []MenuItem{
		{Label: "Disable (standard)", Value: "0"},
		{Label: "Enable (3840x2160)", Value: "1"},
	}
	return NewListMenu("CSI Unite Mode", items, func(item MenuItem) tea.Cmd {
		p, err := appCtx.Registry.Get("csi")
		if err != nil {
			return Status(err.Error(), true)
		}
		csi := p.(*peripheral.CSI)
		if item.Value == "1" {
			csi.SetUnite(true)
		}
		if err := csi.Enable(appCtx.Ctx); err != nil {
			return Status(err.Error(), true)
		}
		return Loading("CSI enabled. Reboot required.", 1*time.Second)
	})
}
