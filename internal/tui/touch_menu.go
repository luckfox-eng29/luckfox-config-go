/*
 * SPDX-FileCopyrightText: 2026 Luckfox Team
 *
 * SPDX-License-Identifier: Apache-2.0
 */

package tui

import (
	"context"
	"fmt"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"luckfox-config/internal/peripheral"
)

func newTouchMenu(appCtx *AppContext) ListMenuModel {
	items := []MenuItem{
		{Label: "Enable", Value: "enable"},
		{Label: "Disable", Value: "disable"},
	}
	return NewListMenu("TouchScreen Configuration", items, func(item MenuItem) tea.Cmd {
		ts := getTouchscreenPeripheral(appCtx)
		if ts == nil {
			return Status("TouchScreen peripheral not available", true)
		}

		switch item.Value {
		case "enable":
			addrs := ts.Addresses()
			if len(addrs) > 1 {
				return NavigatePush(newTouchAddressMenu(ts, addrs))
			}
			return enableTouchscreen(ts)
		case "disable":
			if err := ts.Disable(context.Background()); err != nil {
				return Status(err.Error(), true)
			}
			return Loading("TouchScreen disabled (reboot required)", 500*time.Millisecond)
		}
		return nil
	})
}

func newTouchAddressMenu(ts *peripheral.Touchscreen, addrs []string) ListMenuModel {
	var items []MenuItem
	for _, addr := range addrs {
		items = append(items, MenuItem{Label: "I2C address " + addr, Value: addr})
	}
	return NewListMenu("Touch GT911 Address", items, func(item MenuItem) tea.Cmd {
		if err := ts.SetAddress(item.Value); err != nil {
			return Status(err.Error(), true)
		}
		return enableTouchscreen(ts)
	})
}

func enableTouchscreen(ts *peripheral.Touchscreen) tea.Cmd {
	if err := ts.Enable(context.Background()); err != nil {
		return Status(err.Error(), true)
	}
	return Loading(fmt.Sprintf("TouchScreen enabled: %s (reboot required)", ts.Address()), 500*time.Millisecond)
}

func getTouchscreenPeripheral(appCtx *AppContext) *peripheral.Touchscreen {
	p, err := appCtx.Registry.Get("touchscreen")
	if err != nil {
		return nil
	}
	ts, ok := p.(*peripheral.Touchscreen)
	if !ok {
		return nil
	}
	return ts
}
