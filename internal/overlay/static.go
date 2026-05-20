/*
 * SPDX-FileCopyrightText: 2026 Luckfox Team
 *
 * SPDX-License-Identifier: Apache-2.0
 */

package overlay

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
)

// StaticOverlay applies overlays to the boot partition's DTB.
// Changes require a reboot.
type StaticOverlay struct {
	Resource StaticResource
	TempDir  string
}

// NewStatic opens the boot device and prepares a StaticOverlay for a specific chip.
func NewStatic(chipName, device string) (*StaticOverlay, error) {
	res, err := NewStaticResource(chipName, device)
	if err != nil {
		return nil, err
	}
	return &StaticOverlay{Resource: res, TempDir: tempDir}, nil
}

// dtbFile returns the path to the temporary DTB working file.
func (s *StaticOverlay) dtbFile() string {
	return s.TempDir + "/.resource_fdt.dtb"
}

// writeTempDTB writes the current in-memory DTB to the temp working file.
func (s *StaticOverlay) writeTempDTB() error {
	if err := os.WriteFile(s.dtbFile(), s.Resource.DTB(), 0o644); err != nil {
		return fmt.Errorf("write DTB: %w", err)
	}
	return nil
}

// Apply overlays DTS content onto the existing DTB and writes back to partition.
// Equivalent to luckfox_fdt_overlay().
func (s *StaticOverlay) Apply(dtsContent string) error {
	dtsFile := s.TempDir + "/.fdt_overlay.dts"
	dtboFile := s.TempDir + "/.fdt_overlay.dtbo"

	if err := s.writeTempDTB(); err != nil {
		return err
	}

	if dtsContent != "" {
		// Compile DTS → DTBO
		if err := os.WriteFile(dtsFile, []byte(dtsContent), 0o644); err != nil {
			return fmt.Errorf("write DTS: %w", err)
		}
		out, err := exec.Command("dtc", "-I", "dts", "-O", "dtb", dtsFile, "-o", dtboFile).CombinedOutput()
		if err != nil {
			return fmt.Errorf("dtc: %w\n%s", err, out)
		}
		// Overlay DTBO onto DTB in-place
		out, err = exec.Command("fdtoverlay", "-i", s.dtbFile(), "-o", s.dtbFile(), dtboFile).CombinedOutput()
		if err != nil {
			return fmt.Errorf("fdtoverlay: %w\n%s", err, out)
		}
	}

	// Read back modified DTB
	newDTB, err := os.ReadFile(s.dtbFile())
	if err != nil {
		return fmt.Errorf("read modified DTB: %w", err)
	}
	s.Resource.SetDTB(newDTB)
	return s.Resource.Flush()
}

// FDTPut sets a property in the DTB using fdtput, then flushes.
// Equivalent to running fdtput directly on the DTB file.
func (s *StaticOverlay) FDTPut(node, prop string, args ...string) error {
	if err := s.writeTempDTB(); err != nil {
		return err
	}

	cmdArgs := append([]string{"-t", "x", s.dtbFile(), node, prop}, args...)
	out, err := exec.Command("fdtput", cmdArgs...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("fdtput: %w\n%s", err, out)
	}

	newDTB, err := os.ReadFile(s.dtbFile())
	if err != nil {
		return fmt.Errorf("read modified DTB: %w", err)
	}
	s.Resource.SetDTB(newDTB)
	return s.Resource.Flush()
}

// Delete removes lines matching a pattern from the DTS and re-compiles.
// Equivalent to luckfox_fdt_delete().
func (s *StaticOverlay) Delete(pattern string) error {
	dtsFile := s.TempDir + "/.fdt_dump.dts"

	if err := s.writeTempDTB(); err != nil {
		return err
	}

	// DTB → DTS
	out, err := exec.Command("dtc", "-I", "dtb", "-O", "dts", s.dtbFile(), "-o", dtsFile).CombinedOutput()
	if err != nil {
		return fmt.Errorf("dtc decompile: %w\n%s", err, out)
	}

	// Remove matching lines
	if err := deleteLinesContaining(dtsFile, pattern); err != nil {
		return err
	}

	// DTS → DTB
	out, err = exec.Command("dtc", "-I", "dts", "-O", "dtb", dtsFile, "-o", s.dtbFile()).CombinedOutput()
	if err != nil {
		return fmt.Errorf("dtc recompile: %w\n%s", err, out)
	}

	newDTB, err := os.ReadFile(s.dtbFile())
	if err != nil {
		return fmt.Errorf("read DTB: %w", err)
	}
	s.Resource.SetDTB(newDTB)
	return s.Resource.Flush()
}

// DumpDTS returns the DTS text of the current DTB (for reading properties).
func (s *StaticOverlay) DumpDTS() (string, error) {
	if err := s.writeTempDTB(); err != nil {
		return "", err
	}
	out, err := exec.Command("dtc", "-I", "dtb", "-O", "dts", s.dtbFile()).CombinedOutput()
	return string(out), err
}

// ResolveAlias finds the full path for an alias in the current DTB.
func (s *StaticOverlay) ResolveAlias(alias string) (string, error) {
	if err := s.writeTempDTB(); err != nil {
		return "", err
	}

	out, err := exec.Command("fdtget", "-t", "s", s.dtbFile(), "/aliases", alias).CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("fdtget: %w\n%s", err, out)
	}
	return strings.TrimSpace(string(out)), nil
}

// GetPinctrlPins returns a list of GPIO names (e.g. "GPIO1_PB0") used by a pinctrl node.
// It parses the "rockchip,pins" property from the DTB.
func (s *StaticOverlay) GetPinctrlPins(label string) ([]string, error) {
	if err := s.writeTempDTB(); err != nil {
		return nil, err
	}

	// Find the node by label or phandle
	// This is tricky because fdtget doesn't easily find nodes by label.
	// We can use fdtdump to find the node path for a label.
	out, err := exec.Command("fdtdump", s.dtbFile()).CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("fdtdump: %w", err)
	}

	lines := strings.Split(string(out), "\n")
	for i, line := range lines {
		if strings.Contains(line, label+":") || strings.Contains(line, label+" {") {
			// Find the path. We might need to backtrack or track depth.
			// Simplified: search for the next "rockchip,pins" property in this node.
			for j := i; j < len(lines); j++ {
				if strings.Contains(lines[j], "rockchip,pins") {
					// Found the property. Now we need to parse it.
					// Format: "rockchip,pins = <0x00000001 0x0000000c 0x00000002 0x00000034 ...>"
					parts := strings.Split(lines[j], "<")
					if len(parts) > 1 {
						data := strings.Trim(parts[1], ">; ")
						return parseRockchipPins(data), nil
					}
				}
				if strings.Contains(lines[j], "};") && j > i {
					break // End of node
				}
			}
		}
	}

	return nil, fmt.Errorf("pinctrl node %s not found or has no pins", label)
}

func parseRockchipPins(data string) []string {
	fields := strings.Fields(data)
	var pins []string
	// Each pin is a tuple of 4 cells: <bank pin_num mux config_phandle>
	for i := 0; i+3 < len(fields); i += 4 {
		bank, _ := strconv.ParseUint(strings.TrimPrefix(fields[i], "0x"), 16, 32)
		pinNum, _ := strconv.ParseUint(strings.TrimPrefix(fields[i+1], "0x"), 16, 32)

		// Convert pinNum (0-31) to Group (A-D) and Index (0-7)
		group := ""
		switch {
		case pinNum < 8:
			group = "A"
		case pinNum < 16:
			group = "B"
			pinNum -= 8
		case pinNum < 24:
			group = "C"
			pinNum -= 16
		default:
			group = "D"
			pinNum -= 24
		}

		pins = append(pins, fmt.Sprintf("GPIO%d_%s%d", bank, group, pinNum))
	}
	return pins
}

// GetProperty reads a property from the DTB using fdtget.
func (s *StaticOverlay) GetProperty(node, prop string) (string, error) {
	if err := s.writeTempDTB(); err != nil {
		return "", err
	}

	out, err := exec.Command("fdtget", s.dtbFile(), node, prop).CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("fdtget %s %s: %w\n%s", node, prop, err, out)
	}
	return strings.TrimSpace(string(out)), nil
}

// GetPhandle finds the phandle for a given node name/label.
// It decompiles to DTS and searches for the node.
func (s *StaticOverlay) GetPhandle(nodeName string) (string, error) {
	if err := s.writeTempDTB(); err != nil {
		return "", err
	}

	out, err := exec.Command("fdtdump", s.dtbFile()).CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("fdtdump: %w", err)
	}

	lines := strings.Split(string(out), "\n")
	for i, line := range lines {
		if strings.Contains(line, nodeName+" {") {
			// Search next 5 lines for phandle
			for j := 1; j <= 5 && i+j < len(lines); j++ {
				if strings.Contains(lines[i+j], "phandle") {
					// Format: "phandle = <0x00000045>"
					parts := strings.Split(lines[i+j], "<")
					if len(parts) > 1 {
						ph := strings.Trim(parts[1], ">; ")
						return ph, nil
					}
				}
			}
		}
	}
	return "", fmt.Errorf("phandle for %s not found", nodeName)
}

func deleteLinesContaining(path, pattern string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	var lines []string
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.Contains(line, pattern) {
			lines = append(lines, line)
		}
	}
	f.Close()
	if err := scanner.Err(); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o644)
}
