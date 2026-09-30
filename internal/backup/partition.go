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
	"path/filepath"
	"regexp"
	"strconv"
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

// GetPartitions returns the partition list of the system storage device.
//
// Two image layouts are supported, tried in order:
//   - legacy Luckfox SDK images that encode the layout in the kernel cmdline
//     (blkdevparts=mmcblk0:32K(env),...)
//   - GPT based images that name their partitions through GPT part labels and
//     are enumerated in /proc/partitions
func GetPartitions() ([]PartitionInfo, error) {
	// Legacy layout, kept as the primary detection path.
	partitions, cmdlineErr := partitionsFromCmdline()
	if cmdlineErr == nil {
		return partitions, nil
	}

	// Reserved fallback for images without a blkdevparts layout.
	partitions, labelsErr := partitionsFromPartLabels()
	if labelsErr == nil {
		return partitions, nil
	}

	return nil, fmt.Errorf("%v; %v", cmdlineErr, labelsErr)
}

// partitionsFromCmdline parses /proc/cmdline blkdevparts to get partition list.
func partitionsFromCmdline() ([]PartitionInfo, error) {
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

// partitionsFromPartLabels enumerates block device partitions from
// /proc/partitions and resolves their names from GPT part labels.
func partitionsFromPartLabels() ([]PartitionInfo, error) {
	data, err := os.ReadFile("/proc/partitions")
	if err != nil {
		return nil, fmt.Errorf("failed to read /proc/partitions: %w", err)
	}

	labels := partLabelMap()

	var partitions []PartitionInfo
	for _, line := range strings.Split(string(data), "\n") {
		// Format: "major minor #blocks name"
		fields := strings.Fields(line)
		if len(fields) < 4 || fields[0] == "major" {
			continue
		}

		name := fields[3]
		index, err := partitionIndex(name)
		if err != nil {
			continue // whole disk or other block device, e.g. mmcblk0boot0
		}

		var blocks int64
		fmt.Sscanf(fields[2], "%d", &blocks)

		path := "/dev/" + name
		label := labels[path]
		if label == "" {
			label = getPartitionLabel(path)
		}

		partitions = append(partitions, PartitionInfo{
			Name:    label,
			Path:    path,
			Index:   index,
			FSType:  getFilesystemType(path),
			SizeStr: formatBlocks(blocks),
		})
	}

	if len(partitions) == 0 {
		return nil, fmt.Errorf("no partitions found in /proc/partitions")
	}

	return partitions, nil
}

// partLabelMap maps a device path to its GPT part label from
// /dev/disk/by-partlabel.
func partLabelMap() map[string]string {
	labels := make(map[string]string)

	entries, err := os.ReadDir("/dev/disk/by-partlabel")
	if err != nil {
		return labels
	}

	for _, entry := range entries {
		target, err := os.Readlink(filepath.Join("/dev/disk/by-partlabel", entry.Name()))
		if err != nil {
			continue
		}
		labels["/dev/"+filepath.Base(target)] = entry.Name()
	}

	return labels
}

// partitionIndex returns the partition number of a block device, read from
// sysfs. It fails for whole disks and for devices such as mmcblk0boot0.
func partitionIndex(name string) (int, error) {
	data, err := os.ReadFile(filepath.Join("/sys/class/block", name, "partition"))
	if err != nil {
		return 0, err
	}
	return strconv.Atoi(strings.TrimSpace(string(data)))
}

// getPartitionLabel returns the GPT part label of a block device, used when
// /dev/disk/by-partlabel is not populated.
func getPartitionLabel(device string) string {
	out, err := exec.Command("blkid", "-o", "value", "-s", "PARTLABEL", device).Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// formatBlocks renders a size given in 1K blocks the way the cmdline layout
// writes sizes, e.g. "32K", "4M", "7G".
func formatBlocks(blocks int64) string {
	const (
		megabyte = 1024
		gigabyte = 1024 * 1024
	)

	switch {
	case blocks >= gigabyte:
		return fmt.Sprintf("%dG", blocks/gigabyte)
	case blocks >= megabyte:
		return fmt.Sprintf("%dM", blocks/megabyte)
	default:
		return fmt.Sprintf("%dK", blocks)
	}
}

// blkidTypeRe matches the TYPE="..." tag of blkid's default output.
var blkidTypeRe = regexp.MustCompile(`TYPE="([^"]*)"`)

// getFilesystemType returns the filesystem type of a block device, or "" when
// the device carries no filesystem.
//
// It parses the TYPE="..." tag out of blkid's default output instead of asking
// for the value with "-o value -s TYPE": BusyBox blkid — the only blkid on many
// embedded images — does not implement those util-linux options. It silently
// ignores them, prints the default "device: TAG=..." line anyway and exits 0,
// so the whole line used to be taken for a filesystem type.
func getFilesystemType(device string) string {
	// The exit status is ignored on purpose: blkid exits non-zero for devices
	// without any signature, which is exactly the "no filesystem" case.
	out, _ := exec.Command("blkid", device).Output()
	return parseBlkidType(out)
}

// parseBlkidType extracts the TYPE="..." value from blkid output, e.g.
// `/dev/mmcblk0p3: LABEL="rootfs" UUID="..." TYPE="ext4" PARTLABEL="rootfs"`.
// It returns "" for devices whose only tags are non-filesystem ones such as
// PARTLABEL="uboot", and for empty output.
func parseBlkidType(out []byte) string {
	m := blkidTypeRe.FindSubmatch(out)
	if m == nil {
		return ""
	}
	return strings.TrimSpace(string(m[1]))
}

// GetBackupOptions returns a list of backup options based on the partition table.
func GetBackupOptions() ([]BackupOption, error) {
	partitions, err := GetPartitions()
	if err != nil {
		return nil, fmt.Errorf("failed to get partitions: %w", err)
	}

	var options []BackupOption

	for _, part := range partitions {
		options = append(options, classifyPartition(part))
	}

	return options, nil
}

// classifyPartition decides how a single partition can be backed up.
// Rules:
// - ext4 partitions: BackupTypeRootFS (use rsync-based backup)
// - No filesystem with name "uboot" or "boot": BackupTypeDD (use dd backup)
// - Other filesystems or partitions: BackupTypeUnsupported
func classifyPartition(part PartitionInfo) BackupOption {
	opt := BackupOption{
		Partition:    part,
		DisplayLabel: fmt.Sprintf("%s (%s)", part.Path, part.Name),
	}

	switch {
	case part.FSType == "ext4":
		// ext4 filesystem: use rootfs backup method
		opt.BackupType = BackupTypeRootFS

	case part.FSType == "":
		// No filesystem: check if it's uboot or boot partition
		nameLower := strings.ToLower(part.Name)

		if nameLower == "uboot" || nameLower == "boot" {
			opt.BackupType = BackupTypeDD
		} else {
			opt.BackupType = BackupTypeUnsupported
			opt.Reason = "non-boot raw partition"
		}

	default:
		// Has filesystem but not ext4
		opt.BackupType = BackupTypeUnsupported
		opt.Reason = fmt.Sprintf("unsupported fs: %s", part.FSType)
	}

	return opt
}
