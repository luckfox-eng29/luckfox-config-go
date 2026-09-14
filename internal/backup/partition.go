/*
 * SPDX-FileCopyrightText: 2026 Luckfox Team
 *
 * SPDX-License-Identifier: Apache-2.0
 */

package backup

import (
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strings"
)

// PartitionInfo represents information about a disk partition.
type PartitionInfo struct {
	Name    string // e.g., "env", "uboot", "boot", "rootfs"
	Path    string // e.g., "/dev/mmcblk0p1"
	Index   int    // partition index (1-based)
	FSType  string // e.g., "ext4", "" (no filesystem)
	SizeStr string // human-readable size, e.g., "32K", "4M", "1G"
}

// BackupType defines how a partition should be backed up.
type BackupType int

const (
	BackupTypeUnsupported BackupType = iota // Not supported for backup
	BackupTypeRootFS                        // ext4 filesystem, use rsync-based backup
	BackupTypeDD                            // No filesystem (raw), use dd backup (uboot/boot only)
)

// BackupOption represents a partition that can be backed up.
type BackupOption struct {
	Partition    PartitionInfo
	BackupType   BackupType
	DisplayLabel string // Human-readable label for TUI display
	Reason       string // Why this partition is unsupported (if BackupTypeUnsupported)
}

// GetPartitions parses /proc/cmdline blkdevparts to get partition list.
func GetPartitions() ([]PartitionInfo, error) {
	// Read /proc/cmdline
	data, err := os.ReadFile("/proc/cmdline")
	if err != nil {
		return nil, fmt.Errorf("failed to read /proc/cmdline: %w", err)
	}

	cmdline := string(data)

	// Extract blkdevparts=mmcblk0:...
	re := regexp.MustCompile(`blkdevparts=(mmcblk\d+):([^\s]+)`)
	matches := re.FindStringSubmatch(cmdline)
	if len(matches) < 3 {
		return nil, fmt.Errorf("blkdevparts not found in /proc/cmdline")
	}

	device := matches[1]   // e.g., "mmcblk0"
	partsStr := matches[2] // e.g., "32K(env),512K@32K(idblock),..."

	// Parse each partition: "32K(env)", "512K@32K(idblock)", "4M(uboot)", "-(rootfs)"
	partRe := regexp.MustCompile(`([^,(]+)\((\w+)\)`)

	var partitions []PartitionInfo
	index := 1

	for _, match := range partRe.FindAllStringSubmatch(partsStr, -1) {
		sizeStr := match[1] // e.g., "32K", "4M", "-"
		name := match[2]    // e.g., "env", "uboot"

		part := PartitionInfo{
			Name:    name,
			Path:    fmt.Sprintf("/dev/%sp%d", device, index),
			Index:   index,
			SizeStr: sizeStr,
		}

		// Get filesystem type from blkid
		part.FSType = getFilesystemType(part.Path)

		partitions = append(partitions, part)
		index++
	}

	return partitions, nil
}

// getFilesystemType returns the filesystem type of a block device.
func getFilesystemType(device string) string {
	out, err := exec.Command("blkid", "-o", "value", "-s", "TYPE", device).Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// GetBackupOptions returns a list of backup options based on the partition table.
// Rules:
// - ext4 partitions: BackupTypeRootFS (use rsync-based backup)
// - No filesystem with name "uboot" or "boot": BackupTypeDD (use dd backup)
// - Other filesystems or partitions: BackupTypeUnsupported
func GetBackupOptions() ([]BackupOption, error) {
	partitions, err := GetPartitions()
	if err != nil {
		return nil, fmt.Errorf("failed to get partitions: %w", err)
	}

	var options []BackupOption

	for _, part := range partitions {
		opt := BackupOption{
			Partition: part,
		}

		switch {
		case part.FSType == "ext4":
			// ext4 filesystem: use rootfs backup method
			opt.BackupType = BackupTypeRootFS
			opt.DisplayLabel = fmt.Sprintf("%s (%s)", part.Path, part.Name)

		case part.FSType == "":
			// No filesystem: check if it's uboot or boot partition
			nameLower := strings.ToLower(part.Name)

			if nameLower == "uboot" || nameLower == "boot" {
				opt.BackupType = BackupTypeDD
				opt.DisplayLabel = fmt.Sprintf("%s (%s)", part.Path, part.Name)
			} else {
				opt.BackupType = BackupTypeUnsupported
				opt.Reason = "non-boot raw partition"
				opt.DisplayLabel = fmt.Sprintf("%s (%s)", part.Path, part.Name)
			}

		default:
			// Has filesystem but not ext4
			opt.BackupType = BackupTypeUnsupported
			opt.Reason = fmt.Sprintf("unsupported fs: %s", part.FSType)
			opt.DisplayLabel = fmt.Sprintf("%s (%s)", part.Path, part.Name)
		}

		options = append(options, opt)
	}

	return options, nil
}
