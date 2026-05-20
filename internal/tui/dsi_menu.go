/*
 * SPDX-FileCopyrightText: 2026 Luckfox Team
 *
 * SPDX-License-Identifier: Apache-2.0
 */

package tui

import (
	"context"
	"fmt"
	"strconv"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"luckfox-config/internal/peripheral"
)

func newDSIMenu(appCtx *AppContext) ListMenuModel {
	items := []MenuItem{
		{Label: "Set Display Panel", Value: "panel"},
		{Label: "Logo Rotation", Value: "rotate"},
	}
	return NewListMenu("MIPI DSI Configuration", items, func(item MenuItem) tea.Cmd {
		switch item.Value {
		case "panel":
			return NavigatePush(newDSIFamilyMenu(appCtx))
		case "rotate":
			return NavigatePush(newDSIRotateMenu(appCtx))
		}
		return nil
	})
}

func newDSIFamilyMenu(appCtx *AppContext) ListMenuModel {
	var items []MenuItem
	for _, fam := range appCtx.Panels {
		items = append(items, MenuItem{
			Label: fam.DisplayName,
			Value: fam.Family,
		})
	}
	return NewListMenu("Select Panel Family", items, func(item MenuItem) tea.Cmd {
		// Find the family
		for _, fam := range appCtx.Panels {
			if fam.Family == item.Value {
				return NavigatePush(newDSIPanelMenu(appCtx, fam))
			}
		}
		return nil
	})
}

func newDSIPanelMenu(appCtx *AppContext, family peripheral.DSIPanelFamily) ListMenuModel {
	var items []MenuItem
	for _, p := range family.Panels {
		items = append(items, MenuItem{
			Label: p.DisplayName,
			Value: p.ID,
			Desc:  fmt.Sprintf("%dx%d", p.HActive, p.VActive),
		})
	}
	return NewListMenu(family.DisplayName, items, func(item MenuItem) tea.Cmd {
		dsi := getDSIPeripheral(appCtx)
		if dsi == nil {
			return Status("DSI peripheral not available", true)
		}
		if err := dsi.Apply(context.Background(), family.Family, item.Value); err != nil {
			return Status(err.Error(), true)
		}
		return Loading(fmt.Sprintf("DSI panel set: %s %s (reboot required)", family.Family, item.Label), 500*time.Millisecond)
	})
}

func newDSIRotateMenu(appCtx *AppContext) ListMenuModel {
	items := []MenuItem{
		{Label: "0°", Value: "0"},
		{Label: "90°", Value: "90"},
		{Label: "180°", Value: "180"},
		{Label: "270°", Value: "270"},
	}
	return NewListMenu("Logo Rotation", items, func(item MenuItem) tea.Cmd {
		angle, _ := strconv.Atoi(item.Value)
		dsi := getDSIPeripheral(appCtx)
		if dsi == nil {
			return Status("DSI peripheral not available", true)
		}
		if err := dsi.SetLogoRotate(context.Background(), angle); err != nil {
			return Status(err.Error(), true)
		}
		return Loading(fmt.Sprintf("Logo rotation set to %d° (reboot required)", angle), 500*time.Millisecond)
	})
}

func getDSIPeripheral(appCtx *AppContext) *peripheral.DSI {
	p, err := appCtx.Registry.Get("dsi")
	if err != nil {
		return nil
	}
	return p.(*peripheral.DSI)
}
