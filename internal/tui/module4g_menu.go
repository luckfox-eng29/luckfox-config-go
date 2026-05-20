/*
 * SPDX-FileCopyrightText: 2026 Luckfox Team
 *
 * SPDX-License-Identifier: Apache-2.0
 */

package tui

import (
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"luckfox-config/internal/peripheral"
)

func newModule4GMenu(appCtx *AppContext) ListMenuModel {
	items := []MenuItem{
		{Label: "Enable 4G Module", Value: "enable"},
		{Label: "Disable 4G Module", Value: "disable"},
	}
	return NewListMenu("LTE Module (4G)", items, func(item MenuItem) tea.Cmd {
		p, err := appCtx.Registry.Get("module_4g")
		if err != nil {
			return Status("4G module not available on this board", true)
		}
		m4g := p.(*peripheral.Module4G)
		_ = m4g // used below

		if item.Value == "disable" {
			return tea.Batch(
				LoadingFull("Disabling 4G module...", 0, 0),
				func() tea.Msg {
					if err := p.Disable(appCtx.Ctx); err != nil {
						return StatusMsg{Text: err.Error(), IsError: true}
					}
					return DoneLoadingMsg{Text: "4G module disabled", PopCount: -1}
				},
			)
		}
		return NavigatePush(newModule4GModeMenu(appCtx))
	})
}

func newModule4GModeMenu(appCtx *AppContext) ListMenuModel {
	items := []MenuItem{
		{Label: "WWAN (simcom-cm)", Value: "wwan"},
		{Label: "NDIS (USB gadget)", Value: "ndis"},
		{Label: "PPP", Value: "ppp"},
	}
	return NewListMenu("Select 4G Connection Mode", items, func(item MenuItem) tea.Cmd {
		if item.Value == "ppp" {
			return NavigatePush(newModule4GAPNMenu(appCtx))
		}
		// Set mode and enable
		if err := appCtx.Cfg.SetMulti("MODULE_4G", map[string]string{
			"ENABLE": "1",
			"MODE":   item.Value,
		}); err != nil {
			return Status(err.Error(), true)
		}
		p, _ := appCtx.Registry.Get("module_4g")

		return tea.Batch(
			LoadingFull("Switching mode, please wait patiently...", 0, 0),
			func() tea.Msg {
				start := time.Now()
				if err := p.Enable(appCtx.Ctx); err != nil {
					return StatusMsg{Text: err.Error(), IsError: true}
				}
				// Ensure animation stays for at least a few seconds if it was too fast
				elapsed := time.Since(start)
				if elapsed < 5*time.Second {
					time.Sleep(5*time.Second - elapsed)
				}
				return DoneLoadingMsg{Text: fmt.Sprintf("4G module enabled in %s mode", strings.ToUpper(item.Value)), PopCount: -1}
			},
		)
	})
}

func newModule4GAPNMenu(appCtx *AppContext) ListMenuModel {
	items := []MenuItem{
		{Label: "China Telecom (ctnet)", Value: "ctnet"},
		{Label: "China Mobile (cmnet)", Value: "cmnet"},
		{Label: "China Unicom (3gwap)", Value: "3gwap"},
		{Label: "Custom APN", Value: "custom"},
	}
	return NewListMenu("Select APN", items, func(item MenuItem) tea.Cmd {
		if item.Value != "custom" {
			return enablePPP(appCtx, item.Value)
		}
		return NavigatePush(newPinInputForm(
			"Enter custom APN",
			[]string{"APN string"},
			func(values []string) tea.Cmd {
				apn := strings.TrimSpace(values[0])
				if apn == "" {
					return Status("APN cannot be empty", true)
				}
				return enablePPP(appCtx, apn)
			},
		))
	})
}

func enablePPP(appCtx *AppContext, apn string) tea.Cmd {
	if err := appCtx.Cfg.SetMulti("MODULE_4G", map[string]string{
		"ENABLE": "1",
		"MODE":   "ppp",
		"APN":    apn,
	}); err != nil {
		return Status(err.Error(), true)
	}
	p, _ := appCtx.Registry.Get("module_4g")

	return tea.Batch(
		LoadingFull("Switching mode, please wait patiently...", 0, 0),
		func() tea.Msg {
			start := time.Now()
			if err := p.Enable(appCtx.Ctx); err != nil {
				return StatusMsg{Text: err.Error(), IsError: true}
			}
			elapsed := time.Since(start)
			if elapsed < 5*time.Second {
				time.Sleep(5*time.Second - elapsed)
			}
			return DoneLoadingMsg{Text: fmt.Sprintf("4G PPP enabled with APN=%s", apn), PopCount: -1}
		},
	)
}
