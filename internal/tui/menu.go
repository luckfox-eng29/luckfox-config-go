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

	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
)

// MenuItem represents a single entry in a list menu.
type MenuItem struct {
	Label string
	Value string
	Desc  string
}

// ListMenuModel is a reusable arrow-key list selection component.
type ListMenuModel struct {
	title        string
	items        []MenuItem
	cursor       int
	onSelect     func(item MenuItem) tea.Cmd
	refreshItems func() []MenuItem
	message      string // status/error message
	isError      bool
	isLoading    bool
	spinner      spinner.Model
}

// NewListMenu creates a menu with the given items and selection callback.
func NewListMenu(title string, items []MenuItem, onSelect func(MenuItem) tea.Cmd) ListMenuModel {
	s := spinner.New()
	s.Spinner = spinner.Dot
	s.Style = successStyle

	return ListMenuModel{
		title:    title,
		items:    items,
		onSelect: onSelect,
		spinner:  s,
	}
}

// WithRefresh sets a function to re-generate items, called when receiving StatusMsg.
func (m ListMenuModel) WithRefresh(f func() []MenuItem) ListMenuModel {
	m.refreshItems = f
	return m
}

func (m ListMenuModel) Init() tea.Cmd { return nil }

func (m ListMenuModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case spinner.TickMsg:
		if m.isLoading {
			var cmd tea.Cmd
			m.spinner, cmd = m.spinner.Update(msg)
			return m, cmd
		}
	case LoadingMsg:
		m.isLoading = true
		m.message = msg.Text
		m.isError = false
		if m.refreshItems != nil {
			m.items = m.refreshItems()
		}
		cmds := []tea.Cmd{m.spinner.Tick}
		if msg.Duration > 0 {
			cmds = append(cmds, tea.Tick(msg.Duration, func(t time.Time) tea.Msg {
				return DoneLoadingMsg{Text: msg.Text, PopCount: msg.PopCount}
			}))
		}
		return m, tea.Batch(cmds...)
	case tea.KeyMsg:
		if m.isLoading {
			return m, nil
		}
		switch msg.String() {
		case "up", "k":
			if m.cursor > 0 {
				m.cursor--
			}
		case "down", "j":
			if m.cursor < len(m.items)-1 {
				m.cursor++
			}
		case "left", "h":
			return m, NavigatePop()
		case "right", "l", "enter", " ":
			if len(m.items) > 0 && m.onSelect != nil {
				return m, m.onSelect(m.items[m.cursor])
			}
		case "1", "2", "3", "4", "5", "6", "7", "8", "9":
			idx := int(msg.String()[0] - '1')
			if idx < len(m.items) {
				m.cursor = idx
				if m.onSelect != nil {
					return m, m.onSelect(m.items[m.cursor])
				}
			}
		case "a", "b", "c", "d", "e", "f", "g", "i", "m", "n", "o", "p", "r", "s", "t", "u", "v", "w", "x", "y", "z":
			char := msg.String()[0]
			idx := int(char-'a') + 9
			if idx < len(m.items) {
				m.cursor = idx
				if m.onSelect != nil {
					return m, m.onSelect(m.items[m.cursor])
				}
			}
		case "esc", "q":
			return m, NavigatePop()
		}
	case StatusMsg:
		m.isLoading = false
		m.message = msg.Text
		m.isError = msg.IsError
		if m.refreshItems != nil {
			m.items = m.refreshItems()
		}
	}
	return m, nil
}

func (m ListMenuModel) View() string {
	var sb strings.Builder

	sb.WriteString(titleStyle.Render(m.title))
	sb.WriteByte('\n')

	for i, item := range m.items {
		cursor := "  "
		style := normalStyle
		if i == m.cursor {
			cursor = "> "
			style = selectedStyle
		}
		// Show index for numeric selection (1-based) or alphabet (for > 9)
		idxStr := "  "
		if i < 9 {
			idxStr = fmt.Sprintf("%d.", i+1)
		} else if i < 35 { // 9 + 26 letters
			char := rune('a' + (i - 9))
			// Skip keys used for navigation: h, j, k, l, q
			if char == 'h' || char == 'j' || char == 'k' || char == 'l' || char == 'q' {
				idxStr = "  "
			} else {
				idxStr = string(char) + "."
			}
		}
		line := fmt.Sprintf("%s %s %s", cursor, idxStr, item.Label)
		if item.Desc != "" {
			line += dimStyle.Render("  " + item.Desc)
		}
		sb.WriteString(style.Render(line))
		sb.WriteByte('\n')
	}

	if m.message != "" {
		sb.WriteByte('\n')
		if m.isLoading {
			sb.WriteString(m.spinner.View() + " " + successStyle.Render(m.message))
		} else if m.isError {
			sb.WriteString(errorStyle.Render("✗ " + m.message))
		} else {
			sb.WriteString(successStyle.Render("✓ " + m.message))
		}
		sb.WriteByte('\n')
	}

	sb.WriteByte('\n')
	sb.WriteString(helpStyle.Render(helpKeys))
	return sb.String()
}

// SetMessage sets a status message on the menu.
func (m *ListMenuModel) SetMessage(text string, isErr bool) {
	m.message = text
	m.isError = isErr
}
