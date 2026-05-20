/*
 * SPDX-FileCopyrightText: 2026 Luckfox Team
 *
 * SPDX-License-Identifier: Apache-2.0
 */

package tui

import (
	"fmt"
	"strings"

	"luckfox-config/internal/backup"

	tea "github.com/charmbracelet/bubbletea"
)

type backupMenu struct {
	appCtx       *AppContext
	cursor       int
	options      []string
	backingUp    bool
	progress     backup.Progress
	progressChan chan backup.Progress
	errChan      chan error
	err          error
}

func newBackupMenu(appCtx *AppContext) *backupMenu {
	return &backupMenu{
		appCtx:  appCtx,
		options: []string{"Local (/mnt)", "USB Disk (/mnt/udisk)", "SD Card (/mnt/sdcard)", "Back"},
	}
}

func (m *backupMenu) Init() tea.Cmd {
	return nil
}

type backupProgressMsg backup.Progress
type backupFinishedMsg struct{ err error }

func (m *backupMenu) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case backupProgressMsg:
		m.progress = backup.Progress(msg)
		return m, m.waitForProgress()

	case backupFinishedMsg:
		m.backingUp = false
		m.err = msg.err
		if m.err == nil {
			m.progress.Message = "Backup completed successfully!"
			m.progress.Percentage = 100
		}
		return m, nil

	case tea.KeyMsg:
		if m.backingUp {
			return m, nil
		}
		switch msg.String() {
		case "up", "k":
			if m.cursor > 0 {
				m.cursor--
			}
		case "down", "j":
			if m.cursor < len(m.options)-1 {
				m.cursor++
			}
		case "left", "h":
			return m, NavigatePop()
		case "right", "l", "enter":
			if m.cursor == len(m.options)-1 {
				return m, NavigatePop()
			}
			m.backingUp = true
			m.err = nil
			m.progress = backup.Progress{Percentage: 0, Message: "Initializing..."}
			m.progressChan = make(chan backup.Progress)
			m.errChan = make(chan error, 1)

			var media backup.MediaClass
			switch m.cursor {
			case 0:
				media = backup.Local
			case 1:
				media = backup.USBDisk
			case 2:
				media = backup.SDCard
			}

			// Start backup in a goroutine
			go func() {
				m.errChan <- backup.RootfsBackup(media, m.progressChan)
			}()

			return m, tea.Batch(m.waitForProgress(), m.waitForFinished())
		case "1", "2", "3", "4":
			idx := int(msg.String()[0] - '1')
			if idx < len(m.options) {
				m.cursor = idx
				// Trigger the same logic as "enter"
				if m.cursor == len(m.options)-1 {
					return m, NavigatePop()
				}
				m.backingUp = true
				m.err = nil
				m.progress = backup.Progress{Percentage: 0, Message: "Initializing..."}
				m.progressChan = make(chan backup.Progress)
				m.errChan = make(chan error, 1)

				var media backup.MediaClass
				switch m.cursor {
				case 0:
					media = backup.Local
				case 1:
					media = backup.USBDisk
				case 2:
					media = backup.SDCard
				}

				go func() {
					m.errChan <- backup.RootfsBackup(media, m.progressChan)
				}()
				return m, tea.Batch(m.waitForProgress(), m.waitForFinished())
			}
		case "q", "esc":
			return m, NavigatePop()
		}
	}
	return m, nil
}

func (m *backupMenu) waitForProgress() tea.Cmd {
	return func() tea.Msg {
		p, ok := <-m.progressChan
		if !ok {
			return nil
		}
		return backupProgressMsg(p)
	}
}

func (m *backupMenu) waitForFinished() tea.Cmd {
	return func() tea.Msg {
		err := <-m.errChan
		return backupFinishedMsg{err: err}
	}
}

func (m *backupMenu) View() string {
	var sb strings.Builder

	sb.WriteString(titleStyle.Render("Backup Rootfs to Image"))
	sb.WriteString("\n")

	if m.backingUp {
		sb.WriteString(fmt.Sprintf("[%d%%] %s\n\n", m.progress.Percentage, m.progress.Message))
		sb.WriteString(dimStyle.Render("Please wait..."))
	} else {
		if m.err != nil {
			errText := strings.ReplaceAll(m.err.Error(), "\n", " | ")
			sb.WriteString(errorStyle.Render(fmt.Sprintf("✗ Error: %s", errText)))
			sb.WriteString("\n\n")
		} else if m.progress.Percentage == 100 {
			sb.WriteString(successStyle.Render("✓ Backup successful!"))
			sb.WriteString("\n\n")
		}

		for i, option := range m.options {
			cursor := "  "
			style := normalStyle
			if m.cursor == i {
				cursor = "> "
				style = selectedStyle
			}

			// Show index for selection
			idxStr := fmt.Sprintf("%d.", i+1)
			line := fmt.Sprintf("%s %s %s", cursor, idxStr, option)
			sb.WriteString(style.Render(line))
			sb.WriteByte('\n')
		}
		sb.WriteByte('\n')
		sb.WriteString(helpStyle.Render(helpKeys))
	}
	return sb.String()
}
