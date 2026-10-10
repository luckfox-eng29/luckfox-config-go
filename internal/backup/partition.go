/*
 * SPDX-FileCopyrightText: 2026 Luckfox Team
 *
 * SPDX-License-Identifier: Apache-2.0
 */

package backup

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
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
	BackupTypeExt4                          // ext4 filesystem, use rsync-based backup
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
// Three image layouts are supported, tried in order:
//   - legacy Luckfox SDK images that encode the layout in the kernel cmdline
//     (blkdevparts=mmcblk0:32K(env),...)
//   - GPT based images that name their partitions through GPT part labels and
//     are enumerated in /proc/partitions
//   - SPI-NAND images, whose layout lives in the device tree and is published
//     by the kernel in /proc/mtd
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

	partitions, mtdErr := partitionsFromMtd()
	if mtdErr == nil {
		return partitions, nil
	}

	return nil, fmt.Errorf("%v; %v; %v", cmdlineErr, labelsErr, mtdErr)
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

// mtdLineRe matches a partition line of /proc/mtd, e.g.
// `mtd3: 00400000 00020000 "boot"` (size and erase size in hexadecimal).
var mtdLineRe = regexp.MustCompile(`^(mtd\d+):\s+([0-9a-fA-F]+)\s+([0-9a-fA-F]+)\s+"([^"]*)"`)

// partitionsFromMtd enumerates the MTD partitions of a SPI-NAND system.
//
// Such images carry no block device partition table at all: the layout is part
// of the device tree and the kernel publishes it in /proc/mtd, exposing one
// /dev/mtdblockN block device per partition.
func partitionsFromMtd() ([]PartitionInfo, error) {
	data, err := os.ReadFile("/proc/mtd")
	if err != nil {
		return nil, fmt.Errorf("failed to read /proc/mtd: %w", err)
	}

	partitions := parseProcMtd(data)
	if len(partitions) == 0 {
		return nil, fmt.Errorf("no partitions found in /proc/mtd")
	}

	for i := range partitions {
		partitions[i].FSType = getFilesystemType(partitions[i].Path)
	}

	return partitions, nil
}

// parseProcMtd parses /proc/mtd, e.g.
//
//	dev:    size   erasesize  name
//	mtd0: 00040000 00020000 "uboot"
//	mtd3: 00400000 00020000 "boot"
//
// Index is the MTD device number, so it matches the /dev/mtdblockN path. The
// filesystem type is left empty for the caller to fill in.
func parseProcMtd(data []byte) []PartitionInfo {
	var partitions []PartitionInfo

	for _, line := range strings.Split(string(data), "\n") {
		m := mtdLineRe.FindStringSubmatch(strings.TrimSpace(line))
		if m == nil {
			continue // header and empty lines
		}

		index, err := strconv.Atoi(strings.TrimPrefix(m[1], "mtd"))
		if err != nil {
			continue
		}

		size, err := strconv.ParseInt(m[2], 16, 64)
		if err != nil {
			continue
		}

		partitions = append(partitions, PartitionInfo{
			Name:    m[4],
			Path:    fmt.Sprintf("/dev/mtdblock%d", index),
			Index:   index,
			SizeStr: formatBlocks(size / 1024),
		})
	}

	return partitions
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
// no layer can identify it.
//
// Detection is layered because no single source is reliable on all supported
// images:
//
//  1. blkid's TYPE="..." tag. util-linux implements it, BusyBox does not: the
//     BusyBox blkid applet only ever prints UUID/LABEL, so on RV1106 an ext4
//     rootfs looks exactly like a raw partition.
//  2. the on-disk superblock magic, which does not depend on any userspace tool
//     and works on the BusyBox images.
//  3. /proc/self/mountinfo for partitions that are already mounted (the running
//     root, for example).
//
// The first layer that yields a type wins. "" means the filesystem could not be
// identified at all; classifyPartition then treats the partition as unsupported
// so the UI refuses the backup instead of starting a doomed rsync.
func getFilesystemType(device string) string {
	if fsType := blkidFilesystemType(device); fsType != "" {
		return fsType
	}
	if fsType := superblockFilesystemType(device); fsType != "" {
		return fsType
	}
	return mountinfoFilesystemType(device)
}

// blkidFilesystemType parses the TYPE="..." tag out of blkid's default output,
// or returns "".
//
// It parses the default output instead of asking for the value with
// "-o value -s TYPE": BusyBox blkid — the only blkid on many embedded images —
// does not implement those util-linux options. It silently ignores them, prints
// the default "device: TAG=..." line anyway and exits 0, so the whole line used
// to be taken for a filesystem type — and the missing TYPE tag for a real type.
func blkidFilesystemType(device string) string {
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

// fsSuperblockMagic describes a filesystem signature found at a fixed offset of
// a partition.
type fsSuperblockMagic struct {
	offset int64
	magic  []byte
	fsType string
}

// superblockMagics lists the signatures the backup code needs to recognise:
// ext4 (the only filesystem it can back up through rsync) plus a few common
// ones that must be rejected with a proper reason rather than mistaken for a
// raw partition. Offsets are absolute within the partition and magics are
// compared as raw bytes, so multi-byte values are written in little-endian
// order as they appear on disk.
var superblockMagics = []fsSuperblockMagic{
	{0, []byte("hsqs"), "squashfs"},                // squashfs, little endian
	{0, []byte("sqsh"), "squashfs"},                // squashfs, big endian
	{0, []byte("XFSB"), "xfs"},                     // XFS
	{3, []byte("EXFAT   "), "exfat"},               // exFAT
	{54, []byte("FAT12   "), "vfat"},               // FAT12
	{54, []byte("FAT16   "), "vfat"},               // FAT16
	{82, []byte("FAT32   "), "vfat"},               // FAT32
	{1024, []byte{0x10, 0x20, 0xF5, 0xF2}, "f2fs"}, // F2FS
	{1080, []byte{0x53, 0xef}, "ext4"},             // ext2/3/4 superblock magic
}

// superblockFilesystemType reads the signatures of superblockMagics from the
// device and returns the matching filesystem type, or "".
func superblockFilesystemType(device string) string {
	f, err := os.Open(device)
	if err != nil {
		return "" // missing device or no read permission: cannot probe
	}
	defer f.Close()

	return matchSuperblockMagic(f)
}

// matchSuperblockMagic probes r for the signatures of superblockMagics. It is
// separate from superblockFilesystemType so the matching can be tested without
// a real block device.
func matchSuperblockMagic(r io.ReaderAt) string {
	for _, sb := range superblockMagics {
		buf := make([]byte, len(sb.magic))
		if _, err := r.ReadAt(buf, sb.offset); err != nil {
			continue // partition shorter than this signature
		}
		if bytes.Equal(buf, sb.magic) {
			return sb.fsType
		}
	}
	return ""
}

// mountinfoFilesystemType resolves the filesystem type of a mounted block
// device through /proc/self/mountinfo, matching on the device's major:minor
// numbers. It is the last resort: unmounted partitions are invisible to it, but
// on BusyBox images it is the only kernel-provided source that names the
// filesystem of, for example, the running ext4 root.
func mountinfoFilesystemType(device string) string {
	major, minor, err := deviceNumbers(device)
	if err != nil {
		return ""
	}

	data, err := os.ReadFile("/proc/self/mountinfo")
	if err != nil {
		return ""
	}

	return parseMountinfo(string(data), major, minor)
}

// parseMountinfo returns the filesystem type of the mount whose device numbers
// are major:minor, or "". Each line has the shape
//
//	36 35 179:7 / / rw,relatime - ext4 /dev/root rw
//
// where the fields after "-" are "<fstype> <source> <super options>".
func parseMountinfo(data string, major, minor uint32) string {
	want := fmt.Sprintf("%d:%d", major, minor)

	for _, line := range strings.Split(data, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 4 || fields[2] != want {
			continue
		}
		for i, field := range fields {
			if field == "-" && i+1 < len(fields) {
				return fields[i+1]
			}
		}
	}
	return ""
}

// deviceNumbers returns the major and minor numbers of a device node.
//
// st_rdev is not reported in the kernel-internal dev_t layout (minor in bits
// 0..19, major above): the kernel stores it through new_encode_dev(), the 32-bit
// layout glibc's makedev()/major()/minor() use, where the major sits in bits
// 8..19 and the minor in bits 0..7 plus 12..31. Decoding it as MKDEV
// (major = rdev >> 20) silently returns 0 for every real device, so every
// major:minor lookup against the mount table misses and an already mounted
// partition is reported as unmounted.
func deviceNumbers(device string) (uint32, uint32, error) {
	var st syscall.Stat_t
	if err := syscall.Stat(device, &st); err != nil {
		return 0, 0, err
	}

	rdev := uint64(st.Rdev)
	major := uint32((rdev & 0x00000000000fff00) >> 8)
	major |= uint32((rdev & 0xfffff00000000000) >> 32)
	minor := uint32(rdev & 0x00000000000000ff)
	minor |= uint32((rdev & 0x00000ffffff00000) >> 12)
	return major, minor, nil
}

// RootPartition returns the partition that is currently mounted as the root
// filesystem. It is resolved through the mount table so the kernel's own device
// aliases (for example /dev/root) do not matter.
func RootPartition() (PartitionInfo, error) {
	partitions, err := GetPartitions()
	if err != nil {
		return PartitionInfo{}, err
	}

	for _, part := range partitions {
		if mountPointOf(part.Path) == "/" {
			return part, nil
		}
	}

	return PartitionInfo{}, fmt.Errorf("no partition is mounted as the root filesystem")
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
// - ext4 partitions: BackupTypeExt4 (use rsync-based backup)
// - No filesystem with name "uboot" or "boot": BackupTypeDD (use dd backup)
// - Other filesystems or partitions: BackupTypeUnsupported
func classifyPartition(part PartitionInfo) BackupOption {
	opt := BackupOption{
		Partition:    part,
		DisplayLabel: fmt.Sprintf("%s (%s)", part.Path, part.Name),
	}

	switch {
	case part.FSType == "ext4":
		// ext4 filesystem: use the rsync-based filesystem backup
		opt.BackupType = BackupTypeExt4

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
