/*
 * SPDX-FileCopyrightText: 2026 Luckfox Team
 *
 * SPDX-License-Identifier: Apache-2.0
 */

package tui

import (
	"time"

	"github.com/charmbracelet/bubbletea"
)

func newSDMMCMenu(appCtx *AppContext) ListMenuModel {
	items := []MenuItem{
		{Label: "Enable", Value: "enable"},
		{Label: "Disable", Value: "disable"},
	}
	return NewListMenu("SDMMC Configuration", items, func(item MenuItem) tea.Cmd {
		p, err := appCtx.Registry.Get("sdmmc")
		if err != nil {
			return Status(err.Error(), true)
		}
		if item.Value == "enable" {
			if err := p.Enable(appCtx.Ctx); err != nil {
				return Status(err.Error(), true)
			}
			return Loading("SDMMC enabled.", 1*time.Second)
		}
		if err := p.Disable(appCtx.Ctx); err != nil {
			return Status(err.Error(), true)
		}
		return Loading("SDMMC disabled.", 1*time.Second)
	})
}
