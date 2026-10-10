/*
 * SPDX-FileCopyrightText: 2026 Luckfox Team
 *
 * SPDX-License-Identifier: Apache-2.0
 */

// Package peripheral defines the Peripheral interface and a Registry for all
// enabled peripherals on the current board.
package peripheral

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"luckfox-config/internal/board"
	"luckfox-config/internal/chip"
	"luckfox-config/internal/config"
	"luckfox-config/internal/overlay"
	"luckfox-config/internal/pindiagram"
)

// Peripheral is the common interface for all configurable hardware peripherals.
type Peripheral interface {
	// ID returns a stable unique identifier, e.g. "uart1", "i2c2", "pwm0_ch3".
	ID() string
	// PinsUsed returns the RM_IO indices this peripheral claims when enabled.
	PinsUsed() []int
	// Enable applies overlay/iomux config and updates the config store.
	Enable(ctx context.Context) error
	// Disable removes the overlay and updates the config store.
	Disable(ctx context.Context) error
	// LoadFromConfig reads the saved config and optionally re-applies if status==1.
	LoadFromConfig(ctx context.Context, cfg *config.Store, apply bool) error
	// ConfigKeys returns all /etc/luckfox-config.json keys this peripheral owns.
	ConfigKeys() []string
	// IsActive returns true if the peripheral is currently active on the system
	// (e.g. /dev node exists).
	IsActive() bool
	// IsConfigEnabled returns true if the peripheral is marked as enabled in the config store.
	IsConfigEnabled(cfg *config.Store) bool
}

// Deps bundles common dependencies injected into every peripheral.
type Deps struct {
	Cfg           *config.Store
	Diagram       *pindiagram.PinDiagram
	Dynamic       *overlay.DynamicOverlay
	Static        *overlay.StaticOverlay // may be nil if not needed / not yet opened
	Chip          chip.Chip
	ConfigMode    string // "rmio" or "normal"
	PinctrlNaming string // "v1" or "v2"
	SplitSPI      bool
	Touch         board.TouchConfig // touch controller of the board, zero value when there is none
}

// MapRMIOToGPIO converts an RM_IO index to a raw GPIO number if the chip uses RM_IO.
func (d Deps) MapRMIOToGPIO(pin int) int {
	if strings.ToLower(d.ConfigMode) == "rmio" {
		if raw, err := d.Chip.RMIOToGPIO(pin); err == nil {
			return raw
		}
	}
	return pin
}

// ResolveAlias tries to find the full path for a given alias, first checking
// /proc/device-tree (running system) and then the static DTB if available.
func (d Deps) ResolveAlias(alias string) (string, error) {
	// Try /proc first (running system)
	path, err := board.ResolveAlias(alias)
	if err == nil {
		return path, nil
	}

	// If static overlay is available, try reading from the DTB on partition
	if d.Static != nil {
		if path, err := d.Static.ResolveAlias(alias); err == nil {
			return path, nil
		}
	}

	return "", fmt.Errorf("alias %s not found: %w", alias, err)
}

// Exists returns true if any file matching the pattern exists.
func Exists(pattern string) bool {
	matches, _ := filepath.Glob(pattern)
	return len(matches) > 0
}

// IsNodeEnabled checks if a device tree node is enabled (status="okay").
func IsNodeEnabled(path string) bool {
	if !strings.HasPrefix(path, "/proc/device-tree") {
		path = "/proc/device-tree" + path
	}
	data, err := os.ReadFile(filepath.Join(path, "status"))
	if err != nil {
		return false
	}
	s := string(data)
	return s == "okay" || s == "okay\x00"
}

// GetNodePhandle returns the phandle of a device tree node.
func GetNodePhandle(path string) (uint32, error) {
	if !strings.HasPrefix(path, "/proc/device-tree") {
		path = "/proc/device-tree" + path
	}
	data, err := os.ReadFile(filepath.Join(path, "phandle"))
	if err != nil {
		return 0, err
	}
	if len(data) < 4 {
		return 0, fmt.Errorf("invalid phandle data")
	}
	return uint32(data[0])<<24 | uint32(data[1])<<16 | uint32(data[2])<<8 | uint32(data[3]), nil
}

// FindNodePhandle searches /proc/device-tree for a node with the given name
// (e.g. "rm-io0-pwm0-ch0") and returns its phandle.
func FindNodePhandle(name string) (uint32, error) {
	var found *uint32
	var walk func(dir string) error
	walk = func(dir string) error {
		if ph, err := GetNodePhandle(dir); err == nil {
			if filepath.Base(dir) == name {
				found = &ph
				return nil
			}
		}
		entries, err := os.ReadDir(dir)
		if err != nil {
			return nil
		}
		for _, e := range entries {
			if !e.IsDir() {
				continue
			}
			if err := walk(filepath.Join(dir, e.Name())); err != nil {
				return err
			}
		}
		return nil
	}
	if err := walk("/proc/device-tree"); err != nil {
		return 0, err
	}
	if found == nil {
		return 0, fmt.Errorf("node %s not found in /proc/device-tree", name)
	}
	return *found, nil
}

// Registry holds all Peripheral instances for the current board.
type Registry struct {
	items []Peripheral
	byID  map[string]Peripheral
}

// NewRegistry creates an empty Registry.
func NewRegistry() *Registry {
	return &Registry{byID: make(map[string]Peripheral)}
}

// Register adds a peripheral to the registry.
func (r *Registry) Register(p Peripheral) {
	r.items = append(r.items, p)
	r.byID[p.ID()] = p
}

// Get returns the peripheral with the given ID, or an error if not found.
func (r *Registry) Get(id string) (Peripheral, error) {
	p, ok := r.byID[id]
	if !ok {
		return nil, fmt.Errorf("unknown peripheral %q", id)
	}
	return p, nil
}

// All returns all registered peripherals.
func (r *Registry) All() []Peripheral {
	return r.items
}

// LoadAll calls LoadFromConfig on every registered peripheral.
func (r *Registry) LoadAll(ctx context.Context, cfg *config.Store, apply bool) error {
	for _, p := range r.items {
		if err := p.LoadFromConfig(ctx, cfg, apply); err != nil {
			return fmt.Errorf("load %s: %w", p.ID(), err)
		}
	}
	return nil
}

// Verify checks if any peripheral enabled in the config is not actually active in the system.
// Returns a list of warning messages.
func (r *Registry) Verify(cfg *config.Store) []string {
	var warnings []string
	for _, p := range r.items {
		if p.IsConfigEnabled(cfg) {
			// Config says enabled. Now check system state.
			if !p.IsActive() {
				warnings = append(warnings, fmt.Sprintf("%s enabled in config but not active in system", p.ID()))
			}
		}
	}
	return warnings
}
