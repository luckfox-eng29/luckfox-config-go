/*
 * SPDX-FileCopyrightText: 2026 Luckfox Team
 *
 * SPDX-License-Identifier: Apache-2.0
 */

package tui

import (
	"strconv"
	"time"

	"github.com/charmbracelet/bubbletea"
	"luckfox-config/internal/peripheral"
)

func newRGBMenu(appCtx *AppContext) ListMenuModel {
	items := []MenuItem{
		{Label: "Enable", Value: "enable"},
		{Label: "Disable", Value: "disable"},
	}
	return NewListMenu("RGB Configuration", items, func(item MenuItem) tea.Cmd {
		p, err := appCtx.Registry.Get("rgb")
		if err != nil {
			return Status(err.Error(), true)
		}
		if item.Value == "disable" {
			if err := p.Disable(appCtx.Ctx); err != nil {
				return Status(err.Error(), true)
			}
			return Loading("RGB disabled. Reboot required.", 1*time.Second)
		}
		return NavigatePush(newRGBParamsMenu(appCtx))
	})
}

func newRGBParamsMenu(appCtx *AppContext) tea.Model {
	labels := []string{
		"Clock (Hz)", "Hactive", "Vactive",
		"H Back Porch", "H Front Porch", "V Back Porch", "V Front Porch",
		"HSync Len", "VSync Len",
		"HSync Active (0/1)", "VSync Active (0/1)", "DE Active (0/1)", "PCLK Active (0/1)",
	}

	return newPinInputForm("RGB Parameters", labels, func(values []string) tea.Cmd {
		clk, _ := strconv.Atoi(values[0])
		h, _ := strconv.Atoi(values[1])
		v, _ := strconv.Atoi(values[2])
		hb, _ := strconv.Atoi(values[3])
		hf, _ := strconv.Atoi(values[4])
		vb, _ := strconv.Atoi(values[5])
		vf, _ := strconv.Atoi(values[6])
		hl, _ := strconv.Atoi(values[7])
		vl, _ := strconv.Atoi(values[8])
		ha, _ := strconv.Atoi(values[9])
		va, _ := strconv.Atoi(values[10])
		da, _ := strconv.Atoi(values[11])
		pa, _ := strconv.Atoi(values[12])

		p, err := appCtx.Registry.Get("rgb")
		if err != nil {
			return Status(err.Error(), true)
		}
		rgb := p.(*peripheral.RGB)
		rgb.SetParams(clk, h, v, hb, hf, vb, vf, hl, vl, ha, va, da, pa)
		if err := rgb.Enable(appCtx.Ctx); err != nil {
			return Status(err.Error(), true)
		}
		return Loading("RGB enabled. Reboot required.", 1*time.Second)
	})
}
