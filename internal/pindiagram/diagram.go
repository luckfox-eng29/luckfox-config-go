/*
 * SPDX-FileCopyrightText: 2026 Luckfox Team
 *
 * SPDX-License-Identifier: Apache-2.0
 */

// Package pindiagram tracks pin usage and renders the board's ASCII pin diagram.
package pindiagram

import (
	"fmt"
	"strings"
	"sync"
)

const separator = "|       |"

// PinDiagram holds the board's ASCII art pinout and tracks which RM_IO pins are in use.
type PinDiagram struct {
	rows   []string
	marked map[string]bool // "RM_IO5" -> true means pin is in use
	mu     sync.RWMutex
}

// New creates a PinDiagram from the board's pin_diagram rows.
func New(rows []string) *PinDiagram {
	return &PinDiagram{
		rows:   rows,
		marked: make(map[string]bool),
	}
}

// MarkPin marks or unmarks an RM_IO pin (e.g. "RM_IO5" or 5 via RMIOName).
func (d *PinDiagram) MarkPin(rmio string, inUse bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if inUse {
		d.marked[rmio] = true
	} else {
		delete(d.marked, rmio)
	}
}

// IsMarked reports whether the given RM_IO name is currently marked.
func (d *PinDiagram) IsMarked(rmio string) bool {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.marked[rmio]
}

// CheckConflict checks whether any of the candidate pins share a physical
// connector row with an already-marked pin. Returns the conflicting pin name,
// or "" if no conflict.
//
// Mirrors luckfox_check_pin_diagram: any two pins on the same |       | row
// are physically shared and cannot be used simultaneously.
func (d *PinDiagram) CheckConflict(candidates []string) (string, error) {
	d.mu.RLock()
	defer d.mu.RUnlock()

	for _, row := range d.rows {
		if !strings.Contains(row, separator) {
			continue
		}
		idx := strings.Index(row, separator)
		left := row[:idx]
		right := row[idx+len(separator):]

		for _, candidate := range candidates {
			var side string
			// Check if any token on the left or right side matches the candidate (exactly or as prefix)
			leftFields := strings.Fields(left)
			rightFields := strings.Fields(right)

			match := func(field, pin string) bool {
				return field == pin || strings.HasPrefix(field, pin+"_")
			}

			foundSide := false
			for _, field := range leftFields {
				if match(field, candidate) {
					side = left
					foundSide = true
					break
				}
			}
			if !foundSide {
				for _, field := range rightFields {
					if match(field, candidate) {
						side = right
						foundSide = true
						break
					}
				}
			}

			if !foundSide {
				continue
			}

			// Check if any marked pin shares this side
			sideFields := strings.Fields(side)
			for pin := range d.marked {
				for _, field := range sideFields {
					if match(field, pin) {
						return pin, nil
					}
				}
			}
		}
	}
	return "", nil
}

// CheckConflictErr is like CheckConflict but returns an error if conflicting.
func (d *PinDiagram) CheckConflictErr(candidates []string) error {
	conflict, err := d.CheckConflict(candidates)
	if err != nil {
		return err
	}
	if conflict != "" {
		return fmt.Errorf("pin conflict: %s is already in use on the same physical row", conflict)
	}
	return nil
}

// Render returns the ASCII art diagram with * markers on in-use pins.
func (d *PinDiagram) Render() string {
	d.mu.RLock()
	defer d.mu.RUnlock()

	var sb strings.Builder
	for _, row := range d.rows {
		rendered := row
		// Find all tokens in this row
		fields := strings.Fields(row)
		for _, field := range fields {
			for pin := range d.marked {
				// Check if the token exactly matches or starts with the pin name followed by a separator
				// E.g., pin="UART4_M1" matches "UART4_M1_TX" or "UART4_M1-TX"
				isMatch := field == pin ||
					strings.HasPrefix(field, pin+"_") ||
					strings.HasPrefix(field, pin+"-") ||
					strings.HasPrefix(field, pin+".")

				if isMatch {
					// Replace the field in the original string with *field.
					// We use a simple strategy: if we find the field preceded by ' ' or '-',
					// we prepend the '*'. We prioritize ' ' because it's more common.
					if strings.Contains(rendered, " "+field) {
						rendered = strings.ReplaceAll(rendered, " "+field, "*"+field)
					} else if strings.Contains(rendered, "-"+field) {
						rendered = strings.ReplaceAll(rendered, "-"+field, "*"+field)
					} else if strings.HasPrefix(rendered, field) {
						rendered = "*" + rendered
					}
				}
			}
		}
		sb.WriteString(rendered)
		sb.WriteByte('\n')
	}
	return sb.String()
}

// MarkedPins returns a snapshot of all currently marked pins.
func (d *PinDiagram) MarkedPins() []string {
	d.mu.RLock()
	defer d.mu.RUnlock()
	pins := make([]string, 0, len(d.marked))
	for p := range d.marked {
		pins = append(pins, p)
	}
	return pins
}
