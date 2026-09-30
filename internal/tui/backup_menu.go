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
	options      []backup.BackupOption
	mediaCursor  int
	mediaOptions []string
	backingUp    bool
	progress     backup.Progress
	progressChan chan backup.Progress
	errChan      chan error
	err          error
	selectedPart *backup.BackupOption
	needMedia    bool // Whether we need to ask for media selection
	notice       string
}

func newBackupMenu(appCtx *AppContext) *backupMenu {
	m := &backupMenu{
		appCtx:       appCtx,
		mediaOptions: []string{"Local (/mnt)", "USB Disk (/mnt/udisk)", "SD Card (/mnt/sdcard)"},
	}

	// Load partition options
	options, err := backup.GetBackupOptions()
	if err != nil {
		m.err = err
		m.options = []backup.BackupOption{}
	} else {
		m.options = options
	}

	return m
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

		// If we're in media selection mode
		if m.needMedia {
			return m.updateMediaSelection(msg)
		}

		// Main partition selection
		return m.updatePartitionSelection(msg)
	}
	return m, nil
}

func (m *backupMenu) updatePartitionSelection(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	// Any key dismisses a previously shown notice.
	m.notice = ""

	switch msg.String() {
	case "up", "k":
		if m.cursor > 0 {
			m.cursor--
		}
	case "down", "j":
		if m.cursor < len(m.options) {
			m.cursor++
		}
	case "left", "h":
		return m, NavigatePop()
	case "right", "l", "enter":
		if m.cursor == len(m.options) {
			// "Back" option
			return m, NavigatePop()
		}

		opt := m.options[m.cursor]
		if opt.BackupType == backup.BackupTypeUnsupported {
			// Can't backup unsupported partitions. Say why instead of silently
			// ignoring the key press.
			m.notice = opt.DisplayLabel + " cannot be backed up"
			if opt.Reason != "" {
				m.notice += ": " + opt.Reason
			}
			return m, nil
		}

		// Select this partition and ask for media
		m.selectedPart = &opt
		m.needMedia = true
		m.mediaCursor = 0
		return m, nil

	case "q", "esc":
		return m, NavigatePop()
	}
	return m, nil
}

func (m *backupMenu) updateMediaSelection(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "up", "k":
		if m.mediaCursor > 0 {
			m.mediaCursor--
		}
	case "down", "j":
		if m.mediaCursor < len(m.mediaOptions) {
			m.mediaCursor++
		}
	case "left", "h":
		// Go back to partition selection
		m.needMedia = false
		m.selectedPart = nil
		return m, nil
	case "right", "l", "enter":
		if m.mediaCursor == len(m.mediaOptions) {
			// "Back" option
			m.needMedia = false
			m.selectedPart = nil
			return m, nil
		}

		// Start backup
		m.backingUp = true
		// Leave the media screen: View() renders it first, so without this the
		// progress and result are never shown and the keys stay blocked forever.
		m.needMedia = false
		m.err = nil
		m.progress = backup.Progress{Percentage: 0, Message: "Initializing..."}
		m.progressChan = make(chan backup.Progress)
		m.errChan = make(chan error, 1)

		var media backup.MediaClass
		switch m.mediaCursor {
		case 0:
			media = backup.Local
		case 1:
			media = backup.USBDisk
		case 2:
			media = backup.SDCard
		}

		go func() {
			if m.selectedPart.BackupType == backup.BackupTypeRootFS {
				m.errChan <- backup.RootfsBackup(media, m.progressChan)
			} else if m.selectedPart.BackupType == backup.BackupTypeDD {
				m.errChan <- backup.DDBackup(m.selectedPart.Partition.Path, media, m.progressChan)
			}
		}()

		return m, tea.Batch(m.waitForProgress(), m.waitForFinished())

	case "q", "esc":
		m.needMedia = false
		m.selectedPart = nil
		return m, nil
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

	if m.needMedia {
		return m.viewMediaSelection()
	}

	sb.WriteString(titleStyle.Render("Backup Partition"))
	sb.WriteString("\n\n")

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

		if m.notice != "" {
			sb.WriteString(errorStyle.Render("✗ " + m.notice))
			sb.WriteString("\n\n")
		}

		if len(m.options) == 0 {
			sb.WriteString(dimStyle.Render("No partitions found."))
			sb.WriteString("\n\n")
		} else {
			sb.WriteString(dimStyle.Render("Select a partition to backup:"))
			sb.WriteString("\n\n")

			for i, opt := range m.options {
				cursor := "  "
				style := normalStyle
				if m.cursor == i {
					cursor = "> "
					style = selectedStyle
				}

				line := fmt.Sprintf("%s %s", cursor, opt.DisplayLabel)
				if opt.BackupType == backup.BackupTypeUnsupported {
					sb.WriteString(dimStyle.Render(line))
				} else {
					sb.WriteString(style.Render(line))
				}
				sb.WriteByte('\n')
			}
		}

		// Back option
		cursor := "  "
		style := normalStyle
		if m.cursor == len(m.options) {
			cursor = "> "
			style = selectedStyle
		}
		sb.WriteString(style.Render(fmt.Sprintf("%s Back", cursor)))
		sb.WriteString("\n\n")
		sb.WriteString(helpStyle.Render(helpKeys))
	}
	return sb.String()
}

func (m *backupMenu) viewMediaSelection() string {
	var sb strings.Builder

	sb.WriteString(titleStyle.Render("Select Backup Destination"))
	sb.WriteString("\n\n")

	if m.selectedPart != nil {
		sb.WriteString(dimStyle.Render(fmt.Sprintf("Partition: %s", m.selectedPart.DisplayLabel)))
		sb.WriteString("\n\n")
	}

	for i, option := range m.mediaOptions {
		cursor := "  "
		style := normalStyle
		if m.mediaCursor == i {
			cursor = "> "
			style = selectedStyle
		}
		line := fmt.Sprintf("%s %s", cursor, option)
		sb.WriteString(style.Render(line))
		sb.WriteByte('\n')
	}

	// Back option
	cursor := "  "
	style := normalStyle
	if m.mediaCursor == len(m.mediaOptions) {
		cursor = "> "
		style = selectedStyle
	}
	sb.WriteString(style.Render(fmt.Sprintf("%s Back", cursor)))
	sb.WriteString("\n\n")
	sb.WriteString(helpStyle.Render("←/h: back • ↑/k,↓/j: navigate • →/l/enter: select"))

	return sb.String()
}
