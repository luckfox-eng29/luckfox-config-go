/*
 * SPDX-FileCopyrightText: 2026 Luckfox Team
 *
 * SPDX-License-Identifier: Apache-2.0
 */

// Package overlay manages device tree overlays, both dynamic (configfs) and static (partition).
package overlay

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

const (
	overlaysBase = "/sys/kernel/config/device-tree/overlays"
	tempDir      = "/tmp"
)

// DynamicOverlay applies device tree overlays via the kernel's configfs interface.
// Changes take effect immediately without a reboot.
type DynamicOverlay struct {
	TempDir     string
	OverlaysDir string
}

// NewDynamic creates a DynamicOverlay with default paths.
func NewDynamic() *DynamicOverlay {
	return &DynamicOverlay{TempDir: tempDir, OverlaysDir: overlaysBase}
}

// Apply compiles the DTS content and loads it as a named overlay.
// Equivalent to luckfox_dtbo_overlay().
func (o *DynamicOverlay) Apply(nodeName, dtsContent string) error {
	dtsFile := filepath.Join(o.TempDir, ".overlay_"+nodeName+".dts")
	dtboFile := filepath.Join(o.TempDir, ".overlay_"+nodeName+".dtbo")

	if err := os.WriteFile(dtsFile, []byte(dtsContent), 0o644); err != nil {
		return fmt.Errorf("write DTS: %w", err)
	}

	// Compile DTS → DTBO
	out, err := exec.Command("dtc", "-I", "dts", "-O", "dtb", dtsFile, "-o", dtboFile).CombinedOutput()
	if err != nil {
		return fmt.Errorf("dtc compile: %w\n%s", err, out)
	}

	overlayDir := filepath.Join(o.OverlaysDir, nodeName)

	// If overlay exists, disable it first
	statusPath := filepath.Join(overlayDir, "status")
	if _, err := os.Stat(statusPath); err == nil {
		_ = os.WriteFile(statusPath, []byte("0"), 0o644)
	} else {
		if err := os.MkdirAll(overlayDir, 0o755); err != nil {
			return fmt.Errorf("mkdir overlay %s: %w", overlayDir, err)
		}
	}

	// Write DTBO
	dtboData, err := os.ReadFile(dtboFile)
	if err != nil {
		return fmt.Errorf("read DTBO: %w", err)
	}
	if err := os.WriteFile(filepath.Join(overlayDir, "dtbo"), dtboData, 0o644); err != nil {
		return fmt.Errorf("write DTBO to configfs: %w", err)
	}

	// Enable
	if err := os.WriteFile(statusPath, []byte("1"), 0o644); err != nil {
		return fmt.Errorf("enable overlay %s: %w", nodeName, err)
	}

	// Cleanup temp files
	_ = os.Remove(dtsFile)
	_ = os.Remove(dtboFile)
	return nil
}

// Remove disables and removes a named overlay from configfs.
func (o *DynamicOverlay) Remove(nodeName string) error {
	overlayDir := filepath.Join(o.OverlaysDir, nodeName)
	statusPath := filepath.Join(overlayDir, "status")

	if _, err := os.Stat(overlayDir); os.IsNotExist(err) {
		return nil // already gone
	}

	if err := os.WriteFile(statusPath, []byte("0"), 0o644); err != nil {
		return fmt.Errorf("disable overlay %s: %w", nodeName, err)
	}
	return os.RemoveAll(overlayDir)
}
