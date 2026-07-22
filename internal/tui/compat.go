/*
 * SPDX-FileCopyrightText: 2026 Luckfox Team
 *
 * SPDX-License-Identifier: Apache-2.0
 */

// Package tui terminal compatibility layer for ADB and serial terminals.
//
// ADB and serial terminals may split CSI escape sequences across
// multiple read() calls.  Bubbletea treats a lone ESC byte as the
// Escape key, which triggers NavigatePop → program exit.
//
// This wrapper detects ESC and ESC[ at the end of a read and uses
// select() to wait for the remaining bytes before returning.
package tui

import (
	"os"
	"strings"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"github.com/charmbracelet/bubbletea"
)

// ─── Input wrapper ────────────────────────────────────────────────────────────

type ttyESCTimeout struct {
	f   *os.File
	mu  sync.Mutex
	buf []byte
}

func newTTYESCTimeout(f *os.File) *ttyESCTimeout {
	return &ttyESCTimeout{f: f}
}

func (t *ttyESCTimeout) Fd() uintptr                 { return t.f.Fd() }
func (t *ttyESCTimeout) Write(p []byte) (int, error) { return t.f.Write(p) }
func (t *ttyESCTimeout) Close() error                { return t.f.Close() }

func (t *ttyESCTimeout) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}

	t.mu.Lock()
	if len(t.buf) > 0 {
		n := copy(p, t.buf)
		t.buf = t.buf[n:]
		if len(t.buf) == 0 {
			t.buf = nil
		}
		t.mu.Unlock()
		return n, nil
	}
	t.mu.Unlock()

	n, err := t.f.Read(p)
	if n == 0 {
		return 0, err
	}

	// Case 1: ends with ESC[ (incomplete CSI) — wait for final byte.
	if n >= 2 && p[n-2] == 0x1b && p[n-1] == '[' {
		stashed := make([]byte, n)
		copy(stashed, p[:n])
		for {
			more := t.readOneByte(1 * time.Second)
			if len(more) == 0 {
				break
			}
			stashed = append(stashed, more...)
			if stashed[len(stashed)-1] >= 0x40 && stashed[len(stashed)-1] <= 0x7e {
				break
			}
		}
		return t.returnStashed(p, stashed), nil
	}

	// Case 2: ends with bare ESC — wait for possible CSI start.
	if p[n-1] == 0x1b && (n == 1 || p[n-2] != 0x1b) {
		stashed := make([]byte, n)
		copy(stashed, p[:n])
		next := t.readOneByte(200 * time.Millisecond)
		if len(next) == 0 {
			return t.returnStashed(p, stashed), nil
		}
		stashed = append(stashed, next...)
		if len(stashed) >= 2 && stashed[1] == '[' {
			for {
				more := t.readOneByte(1 * time.Second)
				if len(more) == 0 {
					break
				}
				stashed = append(stashed, more...)
				if stashed[len(stashed)-1] >= 0x40 && stashed[len(stashed)-1] <= 0x7e {
					break
				}
			}
		}
		return t.returnStashed(p, stashed), nil
	}

	return n, err
}

func (t *ttyESCTimeout) returnStashed(p []byte, stashed []byte) int {
	out := copy(p, stashed)
	if out < len(stashed) {
		t.mu.Lock()
		t.buf = stashed[out:]
		t.mu.Unlock()
	}
	return out
}

func (t *ttyESCTimeout) readOneByte(timeout time.Duration) []byte {
	fd := int(t.f.Fd())
	fdSet := [1024 / 64]uint64{}
	fdSet[fd/64] |= 1 << (uint(fd) % 64)
	tv := syscall.Timeval{
		Sec:  int32(timeout / time.Second),
		Usec: int32((timeout % time.Second) / time.Microsecond),
	}
	n, _, _ := syscall.Syscall6(
		syscall.SYS_SELECT,
		uintptr(fd+1),
		uintptr(unsafe.Pointer(&fdSet)),
		0, 0,
		uintptr(unsafe.Pointer(&tv)),
		0,
	)
	if n <= 0 {
		return nil
	}
	var buf [1]byte
	if nn, err := t.f.Read(buf[:]); nn > 0 && err == nil {
		return buf[:1]
	}
	return nil
}

// ─── Terminal detection ───────────────────────────────────────────────────────

// SlowTerminal returns true when the terminal is likely limited.
func SlowTerminal() bool {
	term := os.Getenv("TERM")
	if term == "" || term == "dumb" || term == "vt100" || term == "vt102" || term == "linux" {
		return true
	}
	if os.Getenv("ANDROID_SOCKET_adbd") != "" {
		return true
	}
	if tty := os.Getenv("SSH_TTY"); tty != "" {
		if strings.Contains(tty, "ttyS") || strings.Contains(tty, "ttyAMA") || strings.Contains(tty, "ttyMSM") {
			return true
		}
	}
	return false
}

// asciiHelp replaces non-ASCII characters with ASCII equivalents.
func asciiHelp(s string) string {
	return strings.NewReplacer(
		"↑", "^", "↓", "v", "←", "<", "→", ">",
		"•", "*", "✗", "x", "✓", "+",
	).Replace(s)
}

// ─── Public API ───────────────────────────────────────────────────────────────

// CompatOptions returns tea.ProgramOption values for ADB/serial terminals.
func CompatOptions() []tea.ProgramOption {
	if !SlowTerminal() {
		return nil
	}
	return []tea.ProgramOption{
		tea.WithInput(newTTYESCTimeout(os.Stdin)),
	}
}
