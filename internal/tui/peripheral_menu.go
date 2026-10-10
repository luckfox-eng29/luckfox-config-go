/*
 * SPDX-FileCopyrightText: 2026 Luckfox Team
 *
 * SPDX-License-Identifier: Apache-2.0
 */

package tui

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"luckfox-config/internal/peripheral"
)

func parsePeripheralString(s string) (int, int, int) {
	parts := strings.Split(s, "_")
	if len(parts) == 0 || parts[0] == "" {
		return -1, 0, 0
	}
	num, _ := strconv.Atoi(parts[0])
	mux := 0
	extra := 0
	if len(parts) > 1 {
		if strings.HasPrefix(parts[1], "M") {
			m := strings.TrimPrefix(parts[1], "M")
			mux, _ = strconv.Atoi(m)
		} else {
			extra, _ = strconv.Atoi(parts[1])
			if len(parts) > 2 && strings.HasPrefix(parts[2], "M") {
				m := strings.TrimPrefix(parts[2], "M")
				mux, _ = strconv.Atoi(m)
			}
		}
	}
	return num, extra, mux
}

// bitOf renders a bool as the shell's 1/0 enable flag.
func bitOf(b bool) int {
	if b {
		return 1
	}
	return 0
}

// parseBit parses a shell-style 1/0 enable flag, falling back to def when empty.
func parseBit(v string, def bool) bool {
	switch strings.TrimSpace(v) {
	case "1", "true", "yes", "y":
		return true
	case "0", "false", "no", "n":
		return false
	}
	return def
}

// ─── PWM ─────────────────────────────────────────────────────────────────────

func newPWMMenu(appCtx *AppContext) ListMenuModel {
	cfg := appCtx.Board.Peripherals.PWM
	chip := strings.ToLower(appCtx.Board.Chip)
	isRMIO := strings.ToLower(appCtx.Board.ConfigMode) == "rmio"

	// Group PWMs by controller ID (the first part of the string)
	groups := make(map[int][]string)
	var controllers []int

	for _, s := range cfg {
		parts := strings.Split(s, "_")
		if len(parts) == 0 || parts[0] == "" {
			continue
		}
		ctrl, err := strconv.Atoi(parts[0])
		if err != nil {
			continue
		}
		if _, ok := groups[ctrl]; !ok {
			controllers = append(controllers, ctrl)
		}
		groups[ctrl] = append(groups[ctrl], s)
	}

	// Sort controllers
	for i := 0; i < len(controllers); i++ {
		for j := i + 1; j < len(controllers); j++ {
			if controllers[i] > controllers[j] {
				controllers[i], controllers[j] = controllers[j], controllers[i]
			}
		}
	}

	refresh := func() []MenuItem {
		var items []MenuItem
		for _, ctrl := range controllers {
			label := fmt.Sprintf("PWM%d", ctrl)
			hasAny := false
			for _, s := range groups[ctrl] {
				var id string
				parts := strings.Split(s, "_")
				if isRMIO {
					ch, _ := strconv.Atoi(parts[1])
					id = fmt.Sprintf("pwm%d_ch%d", ctrl, ch)
				} else {
					switch chip {
					case "rv1126b":
						if len(parts) >= 3 {
							ch, _ := strconv.Atoi(parts[1])
							m := strings.TrimPrefix(parts[2], "M")
							mux, _ := strconv.Atoi(m)
							id = fmt.Sprintf("pwm%d_%dm%d", ctrl, ch, mux)
						}
					case "rk3506":
						if len(parts) >= 2 {
							ch, _ := strconv.Atoi(parts[1])
							id = fmt.Sprintf("pwm%d_ch%d", ctrl, ch)
						}
					case "rv1106":
						if len(parts) >= 2 {
							m := strings.TrimPrefix(parts[1], "M")
							mux, _ := strconv.Atoi(m)
							id = fmt.Sprintf("pwm%dm%d", ctrl, mux)
						}
					}
				}
				if p, err := appCtx.Registry.Get(id); err == nil && p.IsActive() {
					hasAny = true
					break
				}
			}
			if hasAny {
				label += " (Enabled)"
			}
			items = append(items, MenuItem{
				Label: label,
				Value: strconv.Itoa(ctrl),
			})
		}
		return items
	}

	return NewListMenu("PWM Configuration", refresh(), func(item MenuItem) tea.Cmd {
		ctrl, _ := strconv.Atoi(item.Value)
		return NavigatePush(newPWMSubMenu(appCtx, ctrl, groups[ctrl], chip, isRMIO))
	}).WithRefresh(refresh)
}

func newPWMSubMenu(appCtx *AppContext, ctrl int, variants []string, chip string, isRMIO bool) ListMenuModel {
	refresh := func() []MenuItem {
		// Sort variants
		sorted := make([]string, len(variants))
		copy(sorted, variants)
		for i := 0; i < len(sorted); i++ {
			for j := i + 1; j < len(sorted); j++ {
				if sorted[i] > sorted[j] {
					sorted[i], sorted[j] = sorted[j], sorted[i]
				}
			}
		}

		var items []MenuItem
		for _, s := range sorted {
			parts := strings.Split(s, "_")
			var label, id string
			var mux int

			if isRMIO {
				ch, _ := strconv.Atoi(parts[1])
				label = fmt.Sprintf("Channel %d", ch)
				id = fmt.Sprintf("pwm%d_ch%d", ctrl, ch)
				mux = 0
			} else {
				switch chip {
				case "rv1126b":
					if len(parts) >= 3 {
						ch, _ := strconv.Atoi(parts[1])
						m := strings.TrimPrefix(parts[2], "M")
						mux, _ = strconv.Atoi(m)
						label = fmt.Sprintf("Channel %d (M%d)", ch, mux)
						id = fmt.Sprintf("pwm%d_%dm%d", ctrl, ch, mux)
					}
				case "rk3506":
					if len(parts) >= 2 {
						ch, _ := strconv.Atoi(parts[1])
						label = fmt.Sprintf("Channel %d", ch)
						id = fmt.Sprintf("pwm%d_ch%d", ctrl, ch)
					}
				case "rv1106":
					if len(parts) >= 2 {
						m := strings.TrimPrefix(parts[1], "M")
						mux, _ = strconv.Atoi(m)
						label = fmt.Sprintf("Group M%d", mux)
						id = fmt.Sprintf("pwm%dm%d", ctrl, mux)
					}
				default:
					label = s
					id = s
				}
			}

			// Check runtime state and system ownership
			reserved := false
			if p, err := appCtx.Registry.Get(id); err == nil {
				if p.IsActive() {
					label += " (Enabled)"
				}
				if pw, ok := p.(*peripheral.PWM); ok && pw.SystemReserved() {
					reserved = true
					label += " [system]"
				}
			}

			items = append(items, MenuItem{
				Label:    label,
				Value:    fmt.Sprintf("%s:%d", id, mux),
				Disabled: reserved,
			})
		}
		return items
	}

	title := fmt.Sprintf("PWM%d Options", ctrl)
	return NewListMenu(title, refresh(), func(item MenuItem) tea.Cmd {
		parts := strings.SplitN(item.Value, ":", 2)
		id := parts[0]
		mux, _ := strconv.Atoi(parts[1])
		return NavigatePush(newPWMActionMenu(appCtx, id, mux, item.Label))
	}).WithRefresh(refresh)
}

func newPWMActionMenu(appCtx *AppContext, id string, defaultMux int, title string) ListMenuModel {
	items := []MenuItem{
		{Label: "Enable", Value: "enable"},
		{Label: "Disable", Value: "disable"},
	}
	return NewListMenu(title, items, func(item MenuItem) tea.Cmd {
		p, err := appCtx.Registry.Get(id)
		if err != nil {
			return Status(err.Error(), true)
		}
		if item.Value == "disable" {
			if err := p.Disable(appCtx.Ctx); err != nil {
				return Status(err.Error(), true)
			}
			msg := fmt.Sprintf("%s disabled", title)
			if strings.ToLower(appCtx.Board.ConfigMode) == "rmio" {
				msg += " (reboot to release)"
			}
			return Loading(msg, 500*time.Millisecond)
		}

		if strings.ToLower(appCtx.Board.ConfigMode) == "rmio" {
			return NavigatePush(newPinInputForm(
				fmt.Sprintf("%s — Enter RM_IO pin number", title),
				[]pinField{newField("RM_IO pin", "0-31", pinRange("RM_IO pin"))},
				func(values []string) tea.Cmd {
					pin, err := strconv.Atoi(strings.TrimSpace(values[0]))
					if err != nil || pin < 0 || pin > 31 {
						return Status("invalid pin number", true)
					}
					p.(*peripheral.PWM).SetPin(pin)
					if err := p.Enable(appCtx.Ctx); err != nil {
						return Status(err.Error(), true)
					}
					return LoadingFull(fmt.Sprintf("%s enabled on RM_IO%d", title, pin), 500*time.Millisecond, 2)
				},
			))
		}

		// Non-RMIO mode: use default mux from config
		pwm := p.(*peripheral.PWM)
		pwm.SetMux(defaultMux)
		if err := pwm.Enable(appCtx.Ctx); err != nil {
			return Status(err.Error(), true)
		}
		return Loading(fmt.Sprintf("%s enabled on M%d", title, defaultMux), 500*time.Millisecond)
	})
}

// ─── UART ────────────────────────────────────────────────────────────────────

func newUARTMenu(appCtx *AppContext) ListMenuModel {
	refresh := func() []MenuItem {
		var items []MenuItem
		for _, s := range appCtx.Board.Peripherals.UART {
			num, _, mux := parsePeripheralString(s)
			if num < 0 {
				continue
			}
			label := fmt.Sprintf("UART%d", num)
			if mux > 0 || strings.Contains(s, "_M") {
				label = fmt.Sprintf("UART%d (M%d)", num, mux)
			}

			// Check if active
			id := fmt.Sprintf("uart%d", num)
			if strings.ToLower(appCtx.Board.ConfigMode) != "rmio" {
				id = fmt.Sprintf("uart%dm%d", num, mux)
			}
			if p, err := appCtx.Registry.Get(id); err == nil && p.IsActive() {
				label += " (Enabled)"
			}

			items = append(items, MenuItem{Label: label, Value: s})
		}
		return items
	}

	return NewListMenu("UART Configuration", refresh(), func(item MenuItem) tea.Cmd {
		num, _, mux := parsePeripheralString(item.Value)
		return NavigatePush(newUARTActionMenu(appCtx, num, mux))
	}).WithRefresh(refresh)
}

func newUARTActionMenu(appCtx *AppContext, n int, defaultMux int) ListMenuModel {
	items := []MenuItem{
		{Label: "Enable", Value: "enable"},
		{Label: "Disable", Value: "disable"},
	}
	return NewListMenu(fmt.Sprintf("UART%d", n), items, func(item MenuItem) tea.Cmd {
		id := fmt.Sprintf("uart%d", n)
		if strings.ToLower(appCtx.Board.ConfigMode) != "rmio" {
			id = fmt.Sprintf("uart%dm%d", n, defaultMux)
		}
		p, err := appCtx.Registry.Get(id)
		if err != nil {
			return Status(err.Error(), true)
		}
		if item.Value == "disable" {
			if err := p.Disable(appCtx.Ctx); err != nil {
				return Status(err.Error(), true)
			}
			return Loading(fmt.Sprintf("UART%d disabled", n), 500*time.Millisecond)
		}

		if strings.ToLower(appCtx.Board.ConfigMode) == "rmio" {
			return NavigatePush(newPinInputForm(
				fmt.Sprintf("UART%d — Enter pin numbers", n),
				[]pinField{
					newField("TX RM_IO pin", "0-31", pinRange("TX RM_IO pin")),
					newField("RX RM_IO pin", "0-31", pinRange("RX RM_IO pin")),
				},
				func(values []string) tea.Cmd {
					tx, err1 := strconv.Atoi(strings.TrimSpace(values[0]))
					rx, err2 := strconv.Atoi(strings.TrimSpace(values[1]))
					if err1 != nil || err2 != nil || tx < 0 || tx > 31 || rx < 0 || rx > 31 {
						return Status("invalid pin numbers", true)
					}
					p.(*peripheral.UART).SetPins(tx, rx)
					if err := p.Enable(appCtx.Ctx); err != nil {
						return Status(err.Error(), true)
					}
					return LoadingFull(fmt.Sprintf("UART%d enabled TX=RM_IO%d RX=RM_IO%d", n, tx, rx), 500*time.Millisecond, 2)
				},
			))
		}

		// Non-RMIO mode: use default mux from config
		u := p.(*peripheral.UART)
		u.SetMux(defaultMux)
		if err := u.Enable(appCtx.Ctx); err != nil {
			return Status(err.Error(), true)
		}
		return Loading(fmt.Sprintf("UART%d enabled on M%d", n, defaultMux), 500*time.Millisecond)
	})
}

// ─── I2C ─────────────────────────────────────────────────────────────────────

func newI2CMenu(appCtx *AppContext) ListMenuModel {
	refresh := func() []MenuItem {
		var items []MenuItem
		for _, s := range appCtx.Board.Peripherals.I2C {
			num, _, mux := parsePeripheralString(s)
			if num < 0 {
				continue
			}
			label := fmt.Sprintf("I2C%d", num)
			if mux > 0 || strings.Contains(s, "_M") {
				label = fmt.Sprintf("I2C%d (M%d)", num, mux)
			}

			// Check if active
			id := fmt.Sprintf("i2c%d", num)
			if strings.ToLower(appCtx.Board.ConfigMode) != "rmio" {
				id = fmt.Sprintf("i2c%dm%d", num, mux)
			}
			if p, err := appCtx.Registry.Get(id); err == nil && p.IsActive() {
				label += " (Enabled)"
			}

			items = append(items, MenuItem{Label: label, Value: s})
		}
		return items
	}

	return NewListMenu("I2C Configuration", refresh(), func(item MenuItem) tea.Cmd {
		num, _, mux := parsePeripheralString(item.Value)
		return NavigatePush(newI2CActionMenu(appCtx, num, mux))
	}).WithRefresh(refresh)
}

func newI2CActionMenu(appCtx *AppContext, n int, defaultMux int) ListMenuModel {
	items := []MenuItem{
		{Label: "Enable", Value: "enable"},
		{Label: "Disable", Value: "disable"},
	}
	return NewListMenu(fmt.Sprintf("I2C%d", n), items, func(item MenuItem) tea.Cmd {
		id := fmt.Sprintf("i2c%d", n)
		if strings.ToLower(appCtx.Board.ConfigMode) != "rmio" {
			id = fmt.Sprintf("i2c%dm%d", n, defaultMux)
		}
		p, err := appCtx.Registry.Get(id)
		if err != nil {
			return Status(err.Error(), true)
		}
		if item.Value == "disable" {
			if err := p.Disable(appCtx.Ctx); err != nil {
				return Status(err.Error(), true)
			}
			return Loading(fmt.Sprintf("I2C%d disabled", n), 500*time.Millisecond)
		}

		if strings.ToLower(appCtx.Board.ConfigMode) == "rmio" {
			return NavigatePush(newPinInputForm(
				fmt.Sprintf("I2C%d — Enter pin numbers", n),
				[]pinField{
					newField("SDA RM_IO pin", "0-31", pinRange("SDA RM_IO pin")),
					newField("SCL RM_IO pin", "0-31", pinRange("SCL RM_IO pin")),
					newField("Speed Hz", "default 100000", numberOnly("Speed Hz")),
				},
				func(values []string) tea.Cmd {
					sda, e1 := strconv.Atoi(strings.TrimSpace(values[0]))
					scl, e2 := strconv.Atoi(strings.TrimSpace(values[1]))
					if e1 != nil || e2 != nil {
						return Status("invalid pin numbers", true)
					}
					speed := 100000
					if s := strings.TrimSpace(values[2]); s != "" {
						speed, _ = strconv.Atoi(s)
					}
					i := p.(*peripheral.I2C)
					i.SetPins(sda, scl)
					i.SetSpeed(speed)
					if err := p.Enable(appCtx.Ctx); err != nil {
						return Status(err.Error(), true)
					}
					return LoadingFull(fmt.Sprintf("I2C%d enabled SDA=RM_IO%d SCL=RM_IO%d", n, sda, scl), 500*time.Millisecond, 2)
				},
			))
		}

		// Non-RMIO mode: ask for speed then enable
		return NavigatePush(newPinInputForm(
			fmt.Sprintf("I2C%d — Enter parameters", n),
			[]pinField{newField("Speed Hz", "default 100000", numberOnly("Speed Hz"))},
			func(values []string) tea.Cmd {
				speed := 100000
				if s := strings.TrimSpace(values[0]); s != "" {
					speed, _ = strconv.Atoi(s)
				}
				i := p.(*peripheral.I2C)
				i.SetMux(defaultMux)
				i.SetSpeed(speed)
				if err := i.Enable(appCtx.Ctx); err != nil {
					return Status(err.Error(), true)
				}
				return LoadingFull(fmt.Sprintf("I2C%d enabled on M%d", n, defaultMux), 500*time.Millisecond, 2)
			},
		))
	})
}

// ─── SPI ─────────────────────────────────────────────────────────────────────

func newSPIMenu(appCtx *AppContext) ListMenuModel {
	refresh := func() []MenuItem {
		var items []MenuItem
		for _, s := range appCtx.Board.Peripherals.SPI {
			num, _, mux := parsePeripheralString(s)
			if num < 0 {
				continue
			}
			label := fmt.Sprintf("SPI%d", num)
			if mux > 0 || strings.Contains(s, "_M") {
				label = fmt.Sprintf("SPI%d (M%d)", num, mux)
			}

			// Check if active
			id := fmt.Sprintf("spi%d", num)
			if strings.ToLower(appCtx.Board.ConfigMode) != "rmio" {
				id = fmt.Sprintf("spi%dm%d", num, mux)
			}
			if p, err := appCtx.Registry.Get(id); err == nil && p.IsActive() {
				label += " (Enabled)"
			}

			items = append(items, MenuItem{Label: label, Value: s})
		}
		return items
	}

	return NewListMenu("SPI Configuration", refresh(), func(item MenuItem) tea.Cmd {
		num, _, mux := parsePeripheralString(item.Value)
		return NavigatePush(newSPIActionMenu(appCtx, num, mux))
	}).WithRefresh(refresh)
}

func newSPIActionMenu(appCtx *AppContext, n int, defaultMux int) ListMenuModel {
	items := []MenuItem{
		{Label: "Enable", Value: "enable"},
		{Label: "Disable", Value: "disable"},
	}
	return NewListMenu(fmt.Sprintf("SPI%d", n), items, func(item MenuItem) tea.Cmd {
		id := fmt.Sprintf("spi%d", n)
		if strings.ToLower(appCtx.Board.ConfigMode) != "rmio" {
			id = fmt.Sprintf("spi%dm%d", n, defaultMux)
		}
		p, err := appCtx.Registry.Get(id)
		if err != nil {
			return Status(err.Error(), true)
		}
		if item.Value == "disable" {
			if err := p.Disable(appCtx.Ctx); err != nil {
				return Status(err.Error(), true)
			}
			return Loading(fmt.Sprintf("SPI%d disabled", n), 500*time.Millisecond)
		}

		if strings.ToLower(appCtx.Board.ConfigMode) == "rmio" {
			return NavigatePush(newPinInputForm(
				fmt.Sprintf("SPI%d — Enter pin numbers", n),
				[]pinField{
					newField("SCLK RM_IO", "0-31", pinRange("SCLK RM_IO")),
					newField("MOSI RM_IO", "0-31", pinRange("MOSI RM_IO")),
					newField("MISO RM_IO", "0-31 or -", pinRangeOrDash("MISO RM_IO")),
					newField("CS RM_IO", "0-31 or -", pinRangeOrDash("CS RM_IO")),
					newField("Speed Hz", "default 10000000", numberOnly("Speed Hz")),
				},
				func(values []string) tea.Cmd {
					sclk, e1 := strconv.Atoi(strings.TrimSpace(values[0]))
					mosi, e2 := strconv.Atoi(strings.TrimSpace(values[1]))
					if e1 != nil || e2 != nil {
						return Status("SCLK and MOSI are required", true)
					}
					miso := -1
					cs := -1
					if v := strings.TrimSpace(values[2]); v != "" && v != "-" {
						miso, _ = strconv.Atoi(v)
					}
					if v := strings.TrimSpace(values[3]); v != "" && v != "-" {
						cs, _ = strconv.Atoi(v)
					}
					speed := 10000000
					if s := strings.TrimSpace(values[4]); s != "" {
						speed, _ = strconv.Atoi(s)
					}
					s := p.(*peripheral.SPI)
					s.SetPins(sclk, mosi, miso, cs)
					s.SetSpeed(speed)
					if err := p.Enable(appCtx.Ctx); err != nil {
						return Status(err.Error(), true)
					}
					return LoadingFull(fmt.Sprintf("SPI%d enabled", n), 500*time.Millisecond, 2)
				},
			))
		}

		// Non-RMIO mode: ask for speed then enable. Split-SPI boards (RV1106)
		// additionally let the user release the MISO / CS0 pins, matching the
		// shell's MISO_ENABLE / CS_ENABLE dialogs.
		spi := p.(*peripheral.SPI)
		splitSPI := appCtx.Board.SplitSPI
		fields := []pinField{newField("Speed Hz", "default 10000000", numberOnly("Speed Hz"))}
		if splitSPI {
			fields = append(fields,
				newField("CS0 pin", fmt.Sprintf("1/0, default %d", bitOf(spi.CSEnabled())), bitFlag("CS0 pin")),
				newField("MISO pin", fmt.Sprintf("1/0, default %d", bitOf(spi.MISOEnabled())), bitFlag("MISO pin")))
		}
		return NavigatePush(newPinInputForm(
			fmt.Sprintf("SPI%d — Enter parameters", n),
			fields,
			func(values []string) tea.Cmd {
				speed := 10000000
				if s := strings.TrimSpace(values[0]); s != "" {
					speed, _ = strconv.Atoi(s)
				}
				spi.SetMux(defaultMux)
				spi.SetSpeed(speed)
				if splitSPI {
					spi.SetSignalUsage(parseBit(values[1], spi.CSEnabled()), parseBit(values[2], spi.MISOEnabled()))
				}
				if err := spi.Enable(appCtx.Ctx); err != nil {
					return Status(err.Error(), true)
				}
				return LoadingFull(fmt.Sprintf("SPI%d enabled on M%d", n, defaultMux), 500*time.Millisecond, 2)
			},
		))
	})
}

// ─── CAN ─────────────────────────────────────────────────────────────────────

func newCANMenu(appCtx *AppContext) ListMenuModel {
	refresh := func() []MenuItem {
		var items []MenuItem
		for _, s := range appCtx.Board.Peripherals.CAN {
			num, _, mux := parsePeripheralString(s)
			if num < 0 {
				continue
			}
			label := fmt.Sprintf("CAN%d", num)
			if mux > 0 || strings.Contains(s, "_M") {
				label = fmt.Sprintf("CAN%d (M%d)", num, mux)
			}

			// Check if active
			id := fmt.Sprintf("can%d", num)
			if strings.ToLower(appCtx.Board.ConfigMode) != "rmio" {
				id = fmt.Sprintf("can%dm%d", num, mux)
			}
			if p, err := appCtx.Registry.Get(id); err == nil && p.IsActive() {
				label += " (Enabled)"
			}

			items = append(items, MenuItem{Label: label, Value: s})
		}
		return items
	}

	return NewListMenu("CAN Configuration", refresh(), func(item MenuItem) tea.Cmd {
		num, _, mux := parsePeripheralString(item.Value)
		return NavigatePush(newCANActionMenu(appCtx, num, mux))
	}).WithRefresh(refresh)
}

func newCANActionMenu(appCtx *AppContext, n int, defaultMux int) ListMenuModel {
	items := []MenuItem{
		{Label: "Enable", Value: "enable"},
		{Label: "Disable", Value: "disable"},
	}
	return NewListMenu(fmt.Sprintf("CAN%d", n), items, func(item MenuItem) tea.Cmd {
		id := fmt.Sprintf("can%d", n)
		if strings.ToLower(appCtx.Board.ConfigMode) != "rmio" {
			id = fmt.Sprintf("can%dm%d", n, defaultMux)
		}
		p, err := appCtx.Registry.Get(id)
		if err != nil {
			return Status(err.Error(), true)
		}
		if item.Value == "disable" {
			if err := p.Disable(appCtx.Ctx); err != nil {
				return Status(err.Error(), true)
			}
			return Loading(fmt.Sprintf("CAN%d disabled", n), 500*time.Millisecond)
		}

		if strings.ToLower(appCtx.Board.ConfigMode) == "rmio" {
			return NavigatePush(newPinInputForm(
				fmt.Sprintf("CAN%d — Enter pin numbers", n),
				[]pinField{
					newField("TX RM_IO", "0-31", pinRange("TX RM_IO")),
					newField("RX RM_IO", "0-31", pinRange("RX RM_IO")),
					newField("Clock rate Hz", "default 300000000", numberOnly("Clock rate Hz")),
				},
				func(values []string) tea.Cmd {
					tx, e1 := strconv.Atoi(strings.TrimSpace(values[0]))
					rx, e2 := strconv.Atoi(strings.TrimSpace(values[1]))
					if e1 != nil || e2 != nil {
						return Status("invalid pin numbers", true)
					}
					speed := 300000000
					if s := strings.TrimSpace(values[2]); s != "" {
						speed, _ = strconv.Atoi(s)
					}
					c := p.(*peripheral.CAN)
					c.SetPins(tx, rx)
					c.SetSpeed(speed)
					if err := p.Enable(appCtx.Ctx); err != nil {
						return Status(err.Error(), true)
					}
					return LoadingFull(fmt.Sprintf("CAN%d enabled TX=RM_IO%d RX=RM_IO%d", n, tx, rx), 500*time.Millisecond, 2)
				},
			))
		}

		// Non-RMIO mode: ask for speed then enable
		return NavigatePush(newPinInputForm(
			fmt.Sprintf("CAN%d — Enter parameters", n),
			[]pinField{newField("Clock rate Hz", "default 300000000", numberOnly("Clock rate Hz"))},
			func(values []string) tea.Cmd {
				speed := 300000000
				if s := strings.TrimSpace(values[0]); s != "" {
					speed, _ = strconv.Atoi(s)
				}
				c := p.(*peripheral.CAN)
				c.SetMux(defaultMux)
				c.SetSpeed(speed)
				if err := c.Enable(appCtx.Ctx); err != nil {
					return Status(err.Error(), true)
				}
				return LoadingFull(fmt.Sprintf("CAN%d enabled on M%d", n, defaultMux), 500*time.Millisecond, 2)
			},
		))
	})
}

// ─── Compatible Apps ─────────────────────────────────────────────────────────

func newCompatibleAppMenu(appCtx *AppContext) ListMenuModel {
	refresh := func() []MenuItem {
		var items []MenuItem
		for _, name := range appCtx.Board.Peripherals.CompatibleApps {
			label := name
			id := "app_" + name
			if p, err := appCtx.Registry.Get(id); err == nil && p.IsActive() {
				label += " (Enabled)"
			}
			items = append(items, MenuItem{Label: label, Value: name})
		}
		return items
	}

	return NewListMenu("Compatible Applications", refresh(), func(item MenuItem) tea.Cmd {
		return NavigatePush(newCompatibleAppActionMenu(appCtx, item.Value))
	}).WithRefresh(refresh)
}

func newCompatibleAppActionMenu(appCtx *AppContext, name string) ListMenuModel {
	items := []MenuItem{
		{Label: "Enable", Value: "enable"},
		{Label: "Disable", Value: "disable"},
	}
	return NewListMenu(name, items, func(item MenuItem) tea.Cmd {
		id := "app_" + name
		p, err := appCtx.Registry.Get(id)
		if err != nil {
			return Status(err.Error(), true)
		}
		if item.Value == "enable" {
			if err := p.Enable(appCtx.Ctx); err != nil {
				return Status(err.Error(), true)
			}
			return Loading(fmt.Sprintf("%s enabled", name), 500*time.Millisecond)
		}
		if err := p.Disable(appCtx.Ctx); err != nil {
			return Status(err.Error(), true)
		}
		return Loading(fmt.Sprintf("%s disabled", name), 500*time.Millisecond)
	})
}

// ─── FBTFT ───────────────────────────────────────────────────────────────────

func newFBTFTMenu(appCtx *AppContext) ListMenuModel {
	items := []MenuItem{
		{Label: "Enable", Value: "enable"},
		{Label: "Disable", Value: "disable"},
	}
	return NewListMenu("Display (FBTFT)", items, func(item MenuItem) tea.Cmd {
		p, err := appCtx.Registry.Get("fbtft")
		if err != nil {
			return Status(err.Error(), true)
		}
		if item.Value == "enable" {
			if err := p.Enable(appCtx.Ctx); err != nil {
				return Status(err.Error(), true)
			}
			return Loading("Display enabled", 500*time.Millisecond)
		}
		if err := p.Disable(appCtx.Ctx); err != nil {
			return Status(err.Error(), true)
		}
		return Loading("Display disabled", 500*time.Millisecond)
	})
}

// ─── Pin Input Form ───────────────────────────────────────────────────────────

// pinField is one row of a pinInputForm: a static label, a hint shown while
// the field is empty, and an optional validator. An empty value always passes
// because every call site treats it as "keep the built-in default".
type pinField struct {
	Label    string
	Hint     string
	Validate func(string) error
}

func newField(label, hint string, validate func(string) error) pinField {
	return pinField{Label: label, Hint: hint, Validate: validate}
}

// pinRange validates an optional RM_IO pin number.
func pinRange(label string) func(string) error {
	return func(v string) error {
		if v == "" {
			return nil
		}
		n, err := strconv.Atoi(v)
		if err != nil {
			return fmt.Errorf("%s: digits only", label)
		}
		if n < 0 || n > 31 {
			return fmt.Errorf("%s: must be 0-31", label)
		}
		return nil
	}
}

// pinRangeOrDash additionally accepts "-", meaning the pin is not used.
func pinRangeOrDash(label string) func(string) error {
	base := pinRange(label)
	return func(v string) error {
		if v == "-" {
			return nil
		}
		return base(v)
	}
}

// numberOnly validates an optional decimal value.
func numberOnly(label string) func(string) error {
	return func(v string) error {
		if v == "" {
			return nil
		}
		if _, err := strconv.Atoi(v); err != nil {
			return fmt.Errorf("%s: digits only", label)
		}
		return nil
	}
}

// bitFlag validates an optional 1/0 flag.
func bitFlag(label string) func(string) error {
	return func(v string) error {
		if v == "" || v == "0" || v == "1" {
			return nil
		}
		return fmt.Errorf("%s: must be 0 or 1", label)
	}
}

// pinInputForm is a multi-field text input screen. Labels are rendered as a
// static column so the field names stay visible while typing, and rejected
// edits are reported inline instead of surfacing as a late submit error.
type pinInputForm struct {
	title     string
	fields    []pinField
	inputs    []textinput.Model
	errs      []string
	labelW    int
	focused   int
	onSubmit  func(values []string) tea.Cmd
	message   string
	isError   bool
	isLoading bool
	popCount  int
	spinner   spinner.Model
}

func newPinInputForm(title string, fields []pinField, onSubmit func([]string) tea.Cmd) pinInputForm {
	inputs := make([]textinput.Model, len(fields))
	labelW := 0
	for i, f := range fields {
		t := textinput.New()
		t.Placeholder = f.Hint
		t.CharLimit = 24
		inputs[i] = t
		if w := len(f.Label); w > labelW {
			labelW = w
		}
	}
	inputs[0].Focus()

	s := spinner.New()
	s.Spinner = spinner.Dot
	s.Style = successStyle

	return pinInputForm{
		title:    title,
		fields:   fields,
		inputs:   inputs,
		errs:     make([]string, len(fields)),
		labelW:   labelW,
		onSubmit: onSubmit,
		spinner:  s,
	}
}

func (f pinInputForm) Init() tea.Cmd { return textinput.Blink }

func (f pinInputForm) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case spinner.TickMsg:
		if f.isLoading {
			var cmd tea.Cmd
			f.spinner, cmd = f.spinner.Update(msg)
			return f, cmd
		}
	case LoadingMsg:
		f.isLoading = true
		f.message = msg.Text
		f.isError = false
		f.popCount = msg.PopCount
		cmds := []tea.Cmd{f.spinner.Tick}
		if msg.Duration > 0 {
			cmds = append(cmds, tea.Tick(msg.Duration, func(t time.Time) tea.Msg {
				return DoneLoadingMsg{Text: msg.Text, PopCount: f.popCount}
			}))
		}
		return f, tea.Batch(cmds...)
	case StatusMsg:
		f.message = msg.Text
		f.isError = msg.IsError
		return f, nil
	case tea.KeyMsg:
		if f.isLoading {
			return f, nil
		}
		switch msg.String() {
		case "esc":
			return f, NavigatePop()
		case "tab", "down":
			f.inputs[f.focused].Blur()
			f.focused = (f.focused + 1) % len(f.inputs)
			f.inputs[f.focused].Focus()
		case "shift+tab", "up":
			f.inputs[f.focused].Blur()
			f.focused = (f.focused - 1 + len(f.inputs)) % len(f.inputs)
			f.inputs[f.focused].Focus()
		case "enter":
			if f.focused < len(f.inputs)-1 {
				// advance to next field
				f.inputs[f.focused].Blur()
				f.focused++
				f.inputs[f.focused].Focus()
			} else {
				// submit
				values := make([]string, len(f.inputs))
				for i, inp := range f.inputs {
					values[i] = inp.Value()
				}
				return f, f.onSubmit(values)
			}
		}
	}

	// Edit the focused field, then revert the change if it fails validation.
	prev := f.inputs[f.focused].Value()
	var cmd tea.Cmd
	f.inputs[f.focused], cmd = f.inputs[f.focused].Update(msg)
	f.validate(f.focused, prev)
	return f, cmd
}

// validate re-runs the focused field's validator. A rejected edit is rolled
// back and reported inline, so invalid input never reaches the submit handler.
func (f *pinInputForm) validate(i int, prev string) {
	f.errs[i] = ""
	if f.fields[i].Validate == nil {
		return
	}
	if err := f.fields[i].Validate(strings.TrimSpace(f.inputs[i].Value())); err != nil {
		f.inputs[i].SetValue(prev)
		f.errs[i] = err.Error()
	}
}

func (f pinInputForm) View() string {
	var sb strings.Builder
	sb.WriteString(titleStyle.Render(f.title))
	sb.WriteByte('\n')
	for i, inp := range f.inputs {
		marker := "  "
		label := normalStyle.Render(f.fields[i].Label)
		if i == f.focused {
			marker = selectedStyle.Render("> ")
			label = selectedStyle.Render(f.fields[i].Label)
		}
		gap := strings.Repeat(" ", f.labelW-len(f.fields[i].Label)+2)
		sb.WriteString(marker + label + gap + inp.View() + "\n")
		if f.errs[i] != "" {
			sb.WriteString("  " + strings.Repeat(" ", f.labelW+2) + errorStyle.Render("✗ "+f.errs[i]) + "\n")
		}
	}
	if f.message != "" {
		sb.WriteByte('\n')
		if f.isLoading {
			sb.WriteString(f.spinner.View() + " " + successStyle.Render(f.message))
		} else if f.isError {
			sb.WriteString(errorStyle.Render("✗ " + f.message))
		} else {
			sb.WriteString(successStyle.Render("✓ " + f.message))
		}
	}
	sb.WriteByte('\n')
	sb.WriteString(helpStyle.Render("tab/↓ next field • enter confirm • esc back"))
	return sb.String()
}

// Ensure interfaces are satisfied
var _ tea.Model = pinInputForm{}
