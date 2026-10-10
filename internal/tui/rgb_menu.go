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

// rgbPreset is one complete RGB panel timing. The built-in entries mirror the
// Pico Ultra boot-key resolution switch in the shell tool
// (luckfox_rgb_app in ref/luckfox-config-rv1106-new.sh).
type rgbPreset struct {
	name       string
	clk        int
	h, v       int
	hb, hf     int
	vb, vf     int
	hLen, vLen int
	hActive    int
	vActive    int
	deActive   int
	pclkActive int
}

var rgbPresets = []rgbPreset{
	{
		name: "480x480",
		clk:  16500000, h: 480, v: 480,
		hb: 10, hf: 50, vb: 8, vf: 8,
		hLen: 4, vLen: 10,
		hActive: 0, vActive: 0, deActive: 0, pclkActive: 1,
	},
	{
		name: "720x720",
		clk:  30000000, h: 720, v: 720,
		hb: 44, hf: 46, vb: 18, vf: 16,
		hLen: 2, vLen: 2,
		hActive: 0, vActive: 0, deActive: 0, pclkActive: 0,
	},
}

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
		return NavigatePush(newRGBPresetMenu(appCtx))
	})
}

// newRGBPresetMenu offers the built-in panel resolutions plus a Custom entry
// that opens the raw timing form.
func newRGBPresetMenu(appCtx *AppContext) ListMenuModel {
	items := make([]MenuItem, 0, len(rgbPresets)+1)
	for _, p := range rgbPresets {
		items = append(items, MenuItem{Label: p.name, Value: p.name})
	}
	items = append(items, MenuItem{Label: "Custom", Value: "custom"})

	return NewListMenu("Select RGB Panel", items, func(item MenuItem) tea.Cmd {
		if item.Value == "custom" {
			return NavigatePush(newRGBParamsMenu(appCtx))
		}
		for _, p := range rgbPresets {
			if p.name == item.Value {
				return applyRGBPreset(appCtx, p, "RGB "+p.name+" enabled. Reboot required.")
			}
		}
		return nil
	})
}

// applyRGBPreset writes the timing into the config and enables RGB, which
// applies a static overlay and takes effect after a reboot.
func applyRGBPreset(appCtx *AppContext, p rgbPreset, msg string) tea.Cmd {
	per, err := appCtx.Registry.Get("rgb")
	if err != nil {
		return Status(err.Error(), true)
	}
	rgb := per.(*peripheral.RGB)
	rgb.SetParams(p.clk, p.h, p.v, p.hb, p.hf, p.vb, p.vf, p.hLen, p.vLen,
		p.hActive, p.vActive, p.deActive, p.pclkActive)
	if err := rgb.Enable(appCtx.Ctx); err != nil {
		return Status(err.Error(), true)
	}
	return Loading(msg, 1*time.Second)
}

func newRGBParamsMenu(appCtx *AppContext) tea.Model {
	fields := []pinField{
		newField("Clock (Hz)", "e.g. 74250000", numberOnly("Clock (Hz)")),
		newField("Hactive", "e.g. 1280", numberOnly("Hactive")),
		newField("Vactive", "e.g. 800", numberOnly("Vactive")),
		newField("H Back Porch", "", numberOnly("H Back Porch")),
		newField("H Front Porch", "", numberOnly("H Front Porch")),
		newField("V Back Porch", "", numberOnly("V Back Porch")),
		newField("V Front Porch", "", numberOnly("V Front Porch")),
		newField("HSync Len", "", numberOnly("HSync Len")),
		newField("VSync Len", "", numberOnly("VSync Len")),
		newField("HSync Active", "0 or 1", bitFlag("HSync Active")),
		newField("VSync Active", "0 or 1", bitFlag("VSync Active")),
		newField("DE Active", "0 or 1", bitFlag("DE Active")),
		newField("PCLK Active", "0 or 1", bitFlag("PCLK Active")),
	}

	return newPinInputForm("RGB Parameters", fields, func(values []string) tea.Cmd {
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

		return applyRGBPreset(appCtx, rgbPreset{
			name: "custom",
			clk:  clk, h: h, v: v,
			hb: hb, hf: hf, vb: vb, vf: vf,
			hLen: hl, vLen: vl,
			hActive: ha, vActive: va, deActive: da, pclkActive: pa,
		}, "RGB enabled. Reboot required.")
	})
}
