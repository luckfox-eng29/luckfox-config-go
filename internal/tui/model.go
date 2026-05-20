/*
 * SPDX-FileCopyrightText: 2026 Luckfox Team
 *
 * SPDX-License-Identifier: Apache-2.0
 */

// Package tui implements the bubbletea-based terminal user interface.
package tui

import (
	"context"
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"luckfox-config/internal/board"
	"luckfox-config/internal/config"
	"luckfox-config/internal/peripheral"
	"luckfox-config/internal/pindiagram"
)

// NavigatePushMsg pushes a new screen onto the navigation stack.
type NavigatePushMsg struct{ Model tea.Model }

// NavigatePopMsg pops the current screen from the stack.
type NavigatePopMsg struct{}

// StatusMsg carries a status/error message to display on the current screen.
type StatusMsg struct {
	Text    string
	IsError bool
}

// LoadingMsg triggers a loading animation for a fixed duration.
type LoadingMsg struct {
	Text     string
	Duration time.Duration
	PopCount int // Number of levels to pop after loading
}

// DoneLoadingMsg is sent after a loading period to trigger pop and status.
type DoneLoadingMsg struct {
	Text     string
	PopCount int
}

// NavigatePush returns a Cmd that pushes a new screen.
func NavigatePush(m tea.Model) tea.Cmd {
	return func() tea.Msg { return NavigatePushMsg{Model: m} }
}

// NavigatePop returns a Cmd that pops the current screen.
func NavigatePop() tea.Cmd {
	return func() tea.Msg { return NavigatePopMsg{} }
}

// NavigatePopDelayed returns a Cmd that pops the current screen after a delay.
func NavigatePopDelayed(d time.Duration) tea.Cmd {
	return tea.Tick(d, func(t time.Time) tea.Msg {
		return NavigatePopMsg{}
	})
}

// Status returns a Cmd that sends a status message.
func Status(text string, isErr bool) tea.Cmd {
	return func() tea.Msg { return StatusMsg{Text: text, IsError: isErr} }
}

// Loading returns a Cmd that shows a loading spinner then pops the screen.
func Loading(text string, d time.Duration) tea.Cmd {
	return func() tea.Msg { return LoadingMsg{Text: text, Duration: d, PopCount: 1} }
}

// LoadingFull returns a Cmd that shows a loading spinner then pops multiple screens.
func LoadingFull(text string, d time.Duration, popCount int) tea.Cmd {
	return func() tea.Msg { return LoadingMsg{Text: text, Duration: d, PopCount: popCount} }
}

// AppContext holds shared state passed to all screens.
type AppContext struct {
	Board    *board.BoardConfig
	Diagram  *pindiagram.PinDiagram
	Cfg      *config.Store
	Registry *peripheral.Registry
	Panels   []peripheral.DSIPanelFamily
	Ctx      context.Context
	Warnings []string
}

// AppModel is the root bubbletea model. It manages a navigation stack of screens.
type AppModel struct {
	stack  []tea.Model
	appCtx *AppContext
	width  int
	height int
}

// New creates the root AppModel with the main menu as the initial screen.
// Default dimensions are set for serial terminals that don't report window size.
func New(appCtx *AppContext) AppModel {
	mainMenu := newMainMenu(appCtx)
	return AppModel{
		stack:  []tea.Model{mainMenu},
		appCtx: appCtx,
		width:  80,
		height: 24,
	}
}

func (a AppModel) Init() tea.Cmd {
	if len(a.stack) > 0 {
		return a.stack[0].Init()
	}
	return nil
}

func (a AppModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		a.width, a.height = msg.Width, msg.Height
		// Fall through to delegate to top model
	case NavigatePushMsg:
		a.stack = append(a.stack, msg.Model)
		return a, tea.Batch(tea.ClearScreen, msg.Model.Init())

	case NavigatePopMsg:
		if len(a.stack) > 1 {
			a.stack = a.stack[:len(a.stack)-1]
			// Clear message on main menu when returning to it
			if len(a.stack) == 1 {
				if mm, ok := a.stack[0].(ListMenuModel); ok {
					mm.SetMessage("", false)
					a.stack[0] = mm
				}
			}
		} else {
			return a, tea.Quit
		}
		return a, tea.ClearScreen

	case DoneLoadingMsg:
		pop := msg.PopCount
		if pop == -1 {
			// Pop until main menu
			if len(a.stack) > 1 {
				a.stack = a.stack[:1]
			}
			return a, tea.Batch(tea.ClearScreen, Status(msg.Text, false))
		}
		if pop > 0 {
			for i := 0; i < pop; i++ {
				if len(a.stack) > 1 {
					a.stack = a.stack[:len(a.stack)-1]
				}
			}
			return a, tea.Batch(tea.ClearScreen, Status(msg.Text, false))
		}
		return a, Status(msg.Text, false)

	case tea.KeyMsg:
		if msg.String() == "ctrl+c" {
			return a, tea.Quit
		}
	}

	// Delegate to active screen
	if len(a.stack) == 0 {
		return a, tea.Quit
	}
	top := a.stack[len(a.stack)-1]
	updated, cmd := top.Update(msg)
	a.stack[len(a.stack)-1] = updated
	return a, cmd
}

func (a AppModel) View() string {
	if len(a.stack) == 0 {
		return ""
	}
	v := a.stack[len(a.stack)-1].View()
	return padScreen(v, a.width, a.height)
}

// padScreen pads every line to exactly width columns and the whole view to
// exactly height lines, so that on serial terminals each repaint fully
// overwrites the previous frame — no leftover characters from old screens.
func padScreen(v string, width, height int) string {
	lines := strings.Split(v, "\n")
	// Pad each line to width with spaces (lipgloss.Width measures visual width,
	// correctly skipping ANSI escape codes).
	for i, line := range lines {
		if pad := width - lipgloss.Width(line); pad > 0 {
			lines[i] = line + strings.Repeat(" ", pad)
		}
	}
	// Append blank padded lines until we reach height.
	blank := strings.Repeat(" ", width)
	for len(lines) < height {
		lines = append(lines, blank)
	}
	return strings.Join(lines, "\n")
}

// newMainMenu builds the top-level menu based on board capabilities.
func newMainMenu(appCtx *AppContext) ListMenuModel {
	refresh := func() []MenuItem {
		items := []MenuItem{
			{Label: "Advanced Options", Value: "advanced"},
		}

		addWithCheck := func(label, value, id string) {
			if p, err := appCtx.Registry.Get(id); err == nil && p.IsActive() {
				label += " (Enabled)"
			}
			items = append(items, MenuItem{Label: label, Value: value})
		}

		if appCtx.Board.Features.DSIFDTNode != "" {
			label := "MIPI DSI"
			if p, err := appCtx.Registry.Get("dsi"); err == nil && p.IsActive() {
				if dsi, ok := p.(*peripheral.DSI); ok {
					h, v := dsi.GetResolution()
					if h != "" && v != "" {
						label += fmt.Sprintf(" (%sx%s)", h, v)
					}
				}
			}
			items = append(items, MenuItem{Label: label, Value: "dsi"})
		}
		if appCtx.Board.Features.Has4GModule {
			label := "LTE Module (4G)"
			if p, err := appCtx.Registry.Get("module_4g"); err == nil && p.IsActive() {
				if m4g, ok := p.(*peripheral.Module4G); ok {
					mode := m4g.GetMode()
					if mode != "" {
						label += fmt.Sprintf(" (%s)", mode)
					}
				}
			}
			items = append(items, MenuItem{Label: label, Value: "4g"})
		}
		if len(appCtx.Board.Peripherals.CompatibleApps) > 0 {
			// Check if any app is enabled
			label := "Compatible Applications"
			for _, name := range appCtx.Board.Peripherals.CompatibleApps {
				if p, err := appCtx.Registry.Get("app_" + name); err == nil && p.IsActive() {
					label += " (Enabled)"
					break
				}
			}
			items = append(items, MenuItem{Label: label, Value: "apps"})
		}
		if appCtx.Board.Features.HasFBTFT {
			addWithCheck("Display (FBTFT)", "fbtft", "fbtft")
		}

		items = append(items, MenuItem{Label: "Backup Rootfs", Value: "backup"})
		items = append(items, MenuItem{Label: "About / Pin Diagram", Value: "about"})
		return items
	}

	title := fmt.Sprintf("%s Config", appCtx.Board.ModelStrings[0])

	m := NewListMenu(title, refresh(), func(item MenuItem) tea.Cmd {
		switch item.Value {
		case "advanced":
			return NavigatePush(newAdvancedMenu(appCtx))
		case "dsi":
			return NavigatePush(newDSIMenu(appCtx))
		case "4g":
			return NavigatePush(newModule4GMenu(appCtx))
		case "apps":
			return NavigatePush(newCompatibleAppMenu(appCtx))
		case "fbtft":
			return NavigatePush(newFBTFTMenu(appCtx))
		case "backup":
			return NavigatePush(newBackupMenu(appCtx))
		case "about":
			return NavigatePush(newPinDiagramView(appCtx))
		}
		return nil
	}).WithRefresh(refresh)

	if len(appCtx.Warnings) > 0 {
		m.SetMessage(strings.Join(appCtx.Warnings, "; "), true)
	}

	return m
}

// newAdvancedMenu builds the Advanced Options sub-menu.
func newAdvancedMenu(appCtx *AppContext) ListMenuModel {
	items := []MenuItem{
		{Label: "PWM", Value: "pwm"},
		{Label: "UART", Value: "uart"},
		{Label: "I2C", Value: "i2c"},
		{Label: "SPI", Value: "spi"},
	}
	if len(appCtx.Board.Peripherals.CAN) > 0 {
		items = append(items, MenuItem{Label: "CAN", Value: "can"})
	}
	if appCtx.Board.Features.HasUSB {
		items = append(items, MenuItem{Label: "USB", Value: "usb"})
	}
	if appCtx.Board.Features.HasCSI {
		items = append(items, MenuItem{Label: "CSI", Value: "csi"})
	}
	if appCtx.Board.Features.HasRGB {
		items = append(items, MenuItem{Label: "RGB", Value: "rgb"})
	}
	if appCtx.Board.Features.HasSDMMC {
		items = append(items, MenuItem{Label: "SDMMC", Value: "sdmmc"})
	}

	return NewListMenu("Advanced Options", items, func(item MenuItem) tea.Cmd {
		switch item.Value {
		case "pwm":
			return NavigatePush(newPWMMenu(appCtx))
		case "uart":
			return NavigatePush(newUARTMenu(appCtx))
		case "i2c":
			return NavigatePush(newI2CMenu(appCtx))
		case "spi":
			return NavigatePush(newSPIMenu(appCtx))
		case "can":
			return NavigatePush(newCANMenu(appCtx))
		case "usb":
			return NavigatePush(newUSBMenu(appCtx))
		case "csi":
			return NavigatePush(newCSIMenu(appCtx))
		case "rgb":
			return NavigatePush(newRGBMenu(appCtx))
		case "sdmmc":
			return NavigatePush(newSDMMCMenu(appCtx))
		}
		return nil
	})
}

// pinDiagramView shows the scrollable ASCII pin diagram.
type pinDiagramView struct {
	content string
	offset  int
	height  int
}

func newPinDiagramView(appCtx *AppContext) pinDiagramView {
	return pinDiagramView{
		content: appCtx.Diagram.Render(),
		height:  30,
	}
}

func (v pinDiagramView) Init() tea.Cmd { return nil }

func (v pinDiagramView) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		v.height = msg.Height - 4
	case tea.KeyMsg:
		switch msg.String() {
		case "up", "k":
			if v.offset > 0 {
				v.offset--
			}
		case "down", "j":
			lines := strings.Count(v.content, "\n")
			if v.offset < lines-v.height {
				v.offset++
			}
		case "esc", "q", "h":
			return v, NavigatePop()
		}
	}
	return v, nil
}

func (v pinDiagramView) View() string {
	lines := strings.Split(v.content, "\n")
	end := v.offset + v.height
	if end > len(lines) {
		end = len(lines)
	}
	visible := lines[v.offset:end]
	return titleStyle.Render("Pin Diagram") + "\n" +
		strings.Join(visible, "\n") + "\n" +
		helpStyle.Render("↑/↓/jk scroll • h/esc back")
}
