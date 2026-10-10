/*
 * SPDX-FileCopyrightText: 2026 Luckfox Team
 *
 * SPDX-License-Identifier: Apache-2.0
 */

package backup

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"

	"luckfox-config/internal/logger"
)

// Progress represents the backup progress state.
type Progress struct {
	Percentage int
	Message    string
}

// MediaClass defines the location where the backup image will be stored.
type MediaClass int

const (
	Local MediaClass = iota
	USBDisk
	SDCard
)

// FilesystemBackup backs up an ext4 partition into a fresh ext4 image stored on
// the selected media.
//
// The rsync source is the partition's current mount point when it is already
// mounted — that is how the running root is handled, since it cannot be mounted
// a second time — otherwise the partition is mounted read-only on a temporary
// path for the duration of the copy. The image is named
// backup_<partition name>.img, matching DDBackup.
func FilesystemBackup(partitionPath, partName string, media MediaClass, progressChan chan<- Progress) error {
	defer close(progressChan)

	progressChan <- Progress{Percentage: 1, Message: "Checking partition..."}

	if partName == "" {
		partName = filepath.Base(partitionPath)
	}

	// Resolve the rsync source: an already mounted partition contributes its
	// mount point, an unmounted one is mounted for us.
	source := mountPointOf(partitionPath)
	var tempMount string
	if source == "" {
		var err error
		tempMount, err = mountReadOnly(partitionPath)
		if err != nil {
			return err
		}
		source = tempMount
	}
	defer func() {
		if tempMount != "" {
			unmount(tempMount)
		}
	}()

	isRoot := source == "/"

	progressChan <- Progress{Percentage: 5, Message: "Checking dependencies..."}
	if _, err := exec.LookPath("rsync"); err != nil {
		return fmt.Errorf("rsync is not installed")
	}

	var imageName, mountPoint string
	var availableSpaceKB int64

	switch media {
	case Local:
		imageName = "/mnt/backup_" + partName + ".img"
		mountPoint = "/mnt/backup_img"
		var err error
		availableSpaceKB, err = getAvailableSpace("/mnt")
		if err != nil {
			return fmt.Errorf("get available space: %w", err)
		}
	case USBDisk:
		if !isMounted("/mnt/udisk") {
			return fmt.Errorf("USB disk is not mounted at /mnt/udisk")
		}
		imageName = "/mnt/udisk/backup_" + partName + ".img"
		mountPoint = "/mnt/udisk/backup_img"
		var err error
		availableSpaceKB, err = getAvailableSpace("/mnt/udisk")
		if err != nil {
			return fmt.Errorf("get available space: %w", err)
		}
	case SDCard:
		if !isMounted("/mnt/sdcard") {
			return fmt.Errorf("SD card is not mounted at /mnt/sdcard")
		}
		imageName = "/mnt/sdcard/backup_" + partName + ".img"
		mountPoint = "/mnt/sdcard/backup_img"
		var err error
		availableSpaceKB, err = getAvailableSpace("/mnt/sdcard")
		if err != nil {
			return fmt.Errorf("get available space: %w", err)
		}
	default:
		return fmt.Errorf("invalid media class")
	}

	// When the partition is not the running root, it may be a second mount of a
	// filesystem that also hosts the image (the root mounted again on a temporary
	// path, for example). Copying would then feed the image into itself, so
	// refuse before the rsync starts filling the filesystem.
	if !isRoot && sameFilesystem(source, filepath.Dir(mountPoint)) {
		return fmt.Errorf("refusing to back up %s: target %s is on the same filesystem", source, mountPoint)
	}

	progressChan <- Progress{Percentage: 8, Message: "Calculating required space..."}
	// 3. Calculate required space
	usedKB, err := getUsedSpace(source)
	if err != nil {
		return fmt.Errorf("get used space: %w", err)
	}

	// Required space in MB: (used_kb * 2.4 / 1024) + 1
	requiredSpaceMB := int64(float64(usedKB)*2.4/1024 + 1)
	if availableSpaceKB/1024 < requiredSpaceMB {
		return fmt.Errorf("not enough space (required: %d MB, available: %d MB)", requiredSpaceMB, availableSpaceKB/1024)
	}

	progressChan <- Progress{Percentage: 10, Message: "Preparing environment..."}

	// 4. Prepare image
	if _, err := os.Stat(imageName); err == nil {
		os.Remove(imageName)
	}
	if isMounted(mountPoint) {
		exec.Command("umount", mountPoint).Run()
	}
	os.RemoveAll(mountPoint)
	os.MkdirAll(mountPoint, 0755)

	imageSizeMB := int64(float64(usedKB)*1.2/1024 + 1)
	progressChan <- Progress{Percentage: 15, Message: fmt.Sprintf("Creating ext4 image (%d MB)...", imageSizeMB)}

	ddSupportsProgress := supportsDDStatusProgress()
	ddDebugInfo := fmt.Sprintf("target=%s used_kb=%d image_size_mb=%d available_kb=%d progress=%t", imageName, usedKB, imageSizeMB, availableSpaceKB, ddSupportsProgress)

	// Enable dd progress only when the local dd implementation supports it.
	ddArgs := []string{"if=/dev/zero", "of=" + imageName, "bs=1M", "count=" + strconv.FormatInt(imageSizeMB, 10)}
	if ddSupportsProgress {
		ddArgs = append(ddArgs, "status=progress")
	}
	ddCmd := exec.Command("dd", ddArgs...)
	stderr, _ := ddCmd.StderrPipe()
	if startErr := ddCmd.Start(); startErr != nil {
		return fmt.Errorf("dd start: %w", startErr)
	}

	var ddStderrMu sync.Mutex
	var ddStderr bytes.Buffer
	ddReadDone := make(chan struct{})

	// Read dd progress
	go func() {
		defer close(ddReadDone)
		buf := make([]byte, 1024)
		for {
			n, readErr := stderr.Read(buf)
			if n > 0 {
				chunk := buf[:n]
				line := string(chunk)

				ddStderrMu.Lock()
				appendTail(&ddStderr, chunk, 8192)
				ddStderrMu.Unlock()

				// Extract bytes copied if possible, but for simplicity just send the last line
				parts := strings.Split(line, "\r")
				lastPart := strings.TrimSpace(parts[len(parts)-1])
				if lastPart != "" {
					progressChan <- Progress{Percentage: 15, Message: "dd: " + lastPart}
				}
			}
			if readErr != nil {
				break
			}
		}
	}()
	err = ddCmd.Wait()
	<-ddReadDone
	if err != nil {
		ddStderrMu.Lock()
		stderrText := strings.TrimSpace(ddStderr.String())
		ddStderrMu.Unlock()

		if media == Local {
			if stderrText != "" {
				logger.Error("dd failed", "error", err, "stderr", stderrText, "debug", ddDebugInfo)
				return fmt.Errorf("dd wait: %w\n%s\ndd debug: %s", err, stderrText, ddDebugInfo)
			}
			logger.Error("dd failed", "error", err, "debug", ddDebugInfo)
			return fmt.Errorf("dd wait: %w\ndd debug: %s", err, ddDebugInfo)
		}
		if stderrText != "" {
			logger.Error("dd failed", "error", err, "stderr", stderrText)
			return fmt.Errorf("dd wait: %w\n%s", err, stderrText)
		}
		logger.Error("dd failed", "error", err)
		return fmt.Errorf("dd wait: %w", err)
	}

	progressChan <- Progress{Percentage: 40, Message: "Formatting image as ext4..."}
	if out, err := exec.Command("mkfs.ext4", "-F", imageName).CombinedOutput(); err != nil {
		return fmt.Errorf("mkfs.ext4: %w\n%s", err, out)
	}

	// 5. Mount and Copy
	progressChan <- Progress{Percentage: 50, Message: "Mounting image..."}

	// Step A: Ensure loop module is loaded and check kernel support
	exec.Command("modprobe", "loop").Run()

	// Check /proc/devices to see if 'loop' (major 7) is registered
	loopSupported := false
	if devices, err := os.ReadFile("/proc/devices"); err == nil {
		if strings.Contains(string(devices), " 7 loop") {
			loopSupported = true
		}
	}

	if !loopSupported {
		return fmt.Errorf("kernel does not support loop devices (check /proc/devices)")
	}

	// Step B: Proactively create loop nodes if missing (udev might not be running)
	if _, err := os.Stat("/dev/loop-control"); err != nil {
		exec.Command("mknod", "/dev/loop-control", "c", "10", "237").Run()
	}

	for i := 0; i < 8; i++ {
		dev := fmt.Sprintf("/dev/loop%d", i)
		if _, err := os.Stat(dev); err != nil {
			exec.Command("mknod", dev, "b", "7", strconv.Itoa(i)).Run()
		}
	}

	exec.Command("sync").Run()

	// Step C: Try mounting using losetup for better compatibility
	var loopDev string
	// Find a free loop device
	if out, err := exec.Command("losetup", "-f").CombinedOutput(); err == nil {
		loopDev = strings.TrimSpace(string(out))
	}

	if loopDev == "" {
		// Fallback: manually check /dev/loopX status
		for i := 0; i < 8; i++ {
			dev := fmt.Sprintf("/dev/loop%d", i)
			// Check if it's already in use
			if err := exec.Command("losetup", dev).Run(); err != nil {
				// losetup <dev> fails if it's NOT associated with anything
				loopDev = dev
				break
			}
		}
	}

	if loopDev != "" {
		// Associate the image with the loop device
		if err := exec.Command("losetup", loopDev, imageName).Run(); err != nil {
			// If it failed, maybe it was actually in use, try next or fallback
			loopDev = ""
		}
	}

	if loopDev != "" {
		// Mount the loop device
		if out, err := exec.Command("mount", loopDev, mountPoint).CombinedOutput(); err != nil {
			exec.Command("losetup", "-d", loopDev).Run()
			return fmt.Errorf("mount %s: %w\n%s", loopDev, err, out)
		}
	} else {
		// Final Fallback to direct mount
		if out, err := exec.Command("mount", "-o", "loop", imageName, mountPoint).CombinedOutput(); err != nil {
			return fmt.Errorf("mount (direct): %w\n%s", err, out)
		}
	}

	progressChan <- Progress{Percentage: 60, Message: "Copying filesystem with rsync..."}
	// Only the running root needs excludes: the target image lives under /mnt
	// and the other ext4 partitions are backed up on their own.
	var excludes []string
	if isRoot {
		excludes = []string{"/sys", "/mnt", "/tmp", "/proc"}
		if isMounted("/oem") {
			excludes = append(excludes, "/oem")
		}
		if isMounted("/userdata") {
			excludes = append(excludes, "/userdata")
		}
	}

	args := []string{"-aX", "--info=progress2"}
	for _, e := range excludes {
		args = append(args, "--exclude="+e)
	}
	args = append(args, source, mountPoint)

	rsyncCmd := exec.Command("rsync", args...)
	stdout, _ := rsyncCmd.StdoutPipe()
	if err := rsyncCmd.Start(); err != nil {
		return fmt.Errorf("rsync start: %w", err)
	}

	// Read rsync progress
	go func() {
		buf := make([]byte, 1024)
		for {
			n, err := stdout.Read(buf)
			if n > 0 {
				line := string(buf[:n])
				parts := strings.Split(line, "\r")
				lastPart := strings.TrimSpace(parts[len(parts)-1])
				if lastPart != "" {
					// Rsync progress2 output can be long, clean it up a bit
					progressChan <- Progress{Percentage: 60, Message: "rsync: " + lastPart}
				}
			}
			if err != nil {
				break
			}
		}
	}()
	if err := rsyncCmd.Wait(); err != nil {
		return fmt.Errorf("rsync wait: %w", err)
	}

	if isRoot {
		progressChan <- Progress{Percentage: 90, Message: "Creating excluded directories..."}
		// Create excluded directories in the backup
		for _, dir := range excludes {
			os.MkdirAll(filepath.Join(mountPoint, dir), 0755)
		}
	}

	progressChan <- Progress{Percentage: 95, Message: "Unmounting and finalizing..."}
	if err := exec.Command("umount", mountPoint).Run(); err != nil {
		// Try lazy unmount if normal fails
		exec.Command("umount", "-l", mountPoint).Run()
	}

	if loopDev != "" {
		exec.Command("losetup", "-d", loopDev).Run()
	}

	progressChan <- Progress{Percentage: 100, Message: "Backup completed successfully!"}
	return nil
}

// filesystemSpaceKB returns the used and available space of the filesystem that
// contains path, in kilobytes.
//
// The numbers come from statfs(2) instead of parsed "df" output on purpose:
// BusyBox df wraps the filesystem column onto a line of its own whenever the
// device name is too long for the column — which is the case for every
// /dev/block/by-name/<name> partition. The wrapped layout shifts every field, so
// a fixed awk column picks the wrong one: "df -k /oem | tail -1 | awk '{print
// $3}'" returns Available instead of Used (452556 vs 29892 on the RV1106
// image), which inflated an oem backup from 36 MB to 531 MB. statfs() cannot be
// reformatted by the userland and reports exactly the columns df derives.
func filesystemSpaceKB(path string) (usedKB, availableKB int64, err error) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(path, &st); err != nil {
		return 0, 0, err
	}

	blockSize := int64(st.Bsize)
	usedKB = (int64(st.Blocks) - int64(st.Bfree)) * blockSize / 1024
	availableKB = int64(st.Bavail) * blockSize / 1024
	return usedKB, availableKB, nil
}

func getAvailableSpace(path string) (int64, error) {
	_, availableKB, err := filesystemSpaceKB(path)
	if err != nil {
		return 0, err
	}
	return availableKB, nil
}

func getUsedSpace(path string) (int64, error) {
	usedKB, _, err := filesystemSpaceKB(path)
	if err != nil {
		return 0, err
	}
	return usedKB, nil
}

func isMounted(path string) bool {
	out, _ := exec.Command("mount").Output()
	lines := strings.Split(string(out), "\n")
	for _, line := range lines {
		fields := strings.Fields(line)
		if len(fields) >= 3 && fields[2] == path {
			return true
		}
	}
	return false
}

// mountPointOf returns the mount point of an already mounted block device, or
// "" when it is not mounted. Devices are matched on their major:minor numbers so
// that the kernel's own aliases — /dev/root for the running root,
// /dev/block/by-name/<name> for the others — resolve to the same partition.
//
// A device can be mounted more than once (a bind mount, or a second mount of the
// running root); "/" wins when present so the running root is always recognised.
func mountPointOf(device string) string {
	major, minor, err := deviceNumbers(device)
	if err != nil {
		return ""
	}

	data, err := os.ReadFile("/proc/self/mountinfo")
	if err != nil {
		return ""
	}

	want := fmt.Sprintf("%d:%d", major, minor)
	found := ""
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		// id parent major:minor root mountpoint ...
		if len(fields) < 5 || fields[2] != want {
			continue
		}
		if fields[4] == "/" {
			return "/"
		}
		if found == "" {
			found = fields[4]
		}
	}
	return found
}

// sameFilesystem reports whether two paths live on the same mounted filesystem.
func sameFilesystem(a, b string) bool {
	var sa, sb syscall.Stat_t
	if err := syscall.Stat(a, &sa); err != nil {
		return false
	}
	if err := syscall.Stat(b, &sb); err != nil {
		return false
	}
	return sa.Dev == sb.Dev
}

// mountReadOnly mounts a block device read-only on a temporary mount point and
// returns that path. The caller must unmount it when done.
func mountReadOnly(device string) (string, error) {
	dir := filepath.Join("/mnt", ".backup_src")
	if err := os.MkdirAll(dir, 0755); err != nil {
		return "", fmt.Errorf("create mount point: %w", err)
	}
	if out, err := exec.Command("mount", "-o", "ro", device, dir).CombinedOutput(); err != nil {
		os.RemoveAll(dir)
		return "", fmt.Errorf("mount %s: %w\n%s", device, err, out)
	}
	return dir, nil
}

// unmount unmounts path, falling back to a lazy unmount, and removes the
// temporary mount point.
func unmount(path string) {
	if err := exec.Command("umount", path).Run(); err != nil {
		exec.Command("umount", "-l", path).Run()
	}
	os.RemoveAll(path)
}

// partitionSizeBytes returns the size of a block device in bytes.
//
// The kernel already knows the size, so it is read from sysfs instead of
// shelling out to "blockdev": that tool is missing on many embedded images and
// BusyBox does not provide it at all.
func partitionSizeBytes(path string) (int64, error) {
	// Resolve symlinks so aliases such as /dev/disk/by-partlabel/rootfs end up as
	// the kernel name (mmcblk0p3) that sysfs is indexed by.
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		path = resolved
	}

	data, err := os.ReadFile(filepath.Join("/sys/class/block", filepath.Base(path), "size"))
	if err != nil {
		return 0, err
	}

	// The sysfs attribute holds the size in 512 byte sectors.
	sectors, err := strconv.ParseInt(strings.TrimSpace(string(data)), 10, 64)
	if err != nil {
		return 0, err
	}

	return sectors * 512, nil
}

func appendTail(dst *bytes.Buffer, chunk []byte, maxBytes int) {
	if len(chunk) >= maxBytes {
		dst.Reset()
		dst.Write(chunk[len(chunk)-maxBytes:])
		return
	}

	if dst.Len()+len(chunk) > maxBytes {
		trim := dst.Len() + len(chunk) - maxBytes
		current := dst.Bytes()
		if trim >= len(current) {
			dst.Reset()
		} else {
			remaining := append([]byte(nil), current[trim:]...)
			dst.Reset()
			dst.Write(remaining)
		}
	}

	dst.Write(chunk)
}

func supportsDDStatusProgress() bool {
	out, err := exec.Command("dd", "--help").CombinedOutput()
	if err != nil && len(out) == 0 {
		return false
	}
	return strings.Contains(string(out), "status=progress")
}

// DDBackup performs a raw dd backup of a partition without filesystem.
// This is typically used for uboot and boot partitions.
func DDBackup(partitionPath string, media MediaClass, progressChan chan<- Progress) error {
	defer close(progressChan)

	progressChan <- Progress{Percentage: 1, Message: "Checking partition..."}

	// Validate partition exists
	if _, err := os.Stat(partitionPath); os.IsNotExist(err) {
		return fmt.Errorf("partition not found: %s", partitionPath)
	}

	// Get partition size
	partSize, err := partitionSizeBytes(partitionPath)
	if err != nil {
		return fmt.Errorf("failed to get partition size: %w", err)
	}

	progressChan <- Progress{Percentage: 5, Message: "Checking dependencies..."}

	// Determine output path based on media
	var outputDir string
	var availableSpaceKB int64

	switch media {
	case Local:
		outputDir = "/mnt"
		availableSpaceKB, err = getAvailableSpace("/mnt")
		if err != nil {
			return fmt.Errorf("get available space: %w", err)
		}
	case USBDisk:
		if !isMounted("/mnt/udisk") {
			return fmt.Errorf("USB disk is not mounted at /mnt/udisk")
		}
		outputDir = "/mnt/udisk"
		availableSpaceKB, err = getAvailableSpace("/mnt/udisk")
		if err != nil {
			return fmt.Errorf("get available space: %w", err)
		}
	case SDCard:
		if !isMounted("/mnt/sdcard") {
			return fmt.Errorf("SD card is not mounted at /mnt/sdcard")
		}
		outputDir = "/mnt/sdcard"
		availableSpaceKB, err = getAvailableSpace("/mnt/sdcard")
		if err != nil {
			return fmt.Errorf("get available space: %w", err)
		}
	default:
		return fmt.Errorf("invalid media class")
	}

	// Extract partition name for filename
	partName := filepath.Base(partitionPath)
	outputPath := filepath.Join(outputDir, fmt.Sprintf("backup_%s.img", partName))

	// Check available space (need at least partition size + 10% margin)
	requiredKB := partSize/1024 + partSize/1024/10
	if availableSpaceKB < requiredKB {
		return fmt.Errorf("not enough space (required: %d MB, available: %d MB)", requiredKB/1024, availableSpaceKB/1024)
	}

	progressChan <- Progress{Percentage: 10, Message: fmt.Sprintf("Starting dd backup of %s...", partName)}

	// Remove existing backup file if present
	if _, err := os.Stat(outputPath); err == nil {
		os.Remove(outputPath)
	}

	// Use dd to backup
	ddSupportsProgress := supportsDDStatusProgress()
	ddArgs := []string{"if=" + partitionPath, "of=" + outputPath, "bs=1M", "conv=fsync"}
	if ddSupportsProgress {
		ddArgs = append(ddArgs, "status=progress")
	}

	ddCmd := exec.Command("dd", ddArgs...)
	stderr, _ := ddCmd.StderrPipe()
	if startErr := ddCmd.Start(); startErr != nil {
		return fmt.Errorf("dd start: %w", startErr)
	}

	var ddStderrMu sync.Mutex
	var ddStderr bytes.Buffer
	ddReadDone := make(chan struct{})

	// Read dd progress
	go func() {
		defer close(ddReadDone)
		buf := make([]byte, 1024)
		for {
			n, readErr := stderr.Read(buf)
			if n > 0 {
				chunk := buf[:n]
				line := string(chunk)

				ddStderrMu.Lock()
				appendTail(&ddStderr, chunk, 8192)
				ddStderrMu.Unlock()

				parts := strings.Split(line, "\r")
				lastPart := strings.TrimSpace(parts[len(parts)-1])
				if lastPart != "" {
					progressChan <- Progress{Percentage: 10, Message: "dd: " + lastPart}
				}
			}
			if readErr != nil {
				break
			}
		}
	}()

	err = ddCmd.Wait()
	<-ddReadDone
	if err != nil {
		ddStderrMu.Lock()
		stderrText := strings.TrimSpace(ddStderr.String())
		ddStderrMu.Unlock()

		os.Remove(outputPath) // Clean up partial file
		if stderrText != "" {
			logger.Error("dd backup failed", "error", err, "stderr", stderrText)
			return fmt.Errorf("dd backup failed: %w\n%s", err, stderrText)
		}
		logger.Error("dd backup failed", "error", err)
		return fmt.Errorf("dd backup failed: %w", err)
	}

	progressChan <- Progress{Percentage: 95, Message: "Syncing..."}
	exec.Command("sync").Run()

	progressChan <- Progress{Percentage: 100, Message: fmt.Sprintf("Backup completed: %s", outputPath)}
	return nil
}
