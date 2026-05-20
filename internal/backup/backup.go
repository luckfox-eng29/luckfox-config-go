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

// RootfsBackup performs a backup of the root filesystem to an ext4 image.
func RootfsBackup(media MediaClass, progressChan chan<- Progress) error {
	defer close(progressChan)

	progressChan <- Progress{Percentage: 1, Message: "Checking root filesystem..."}
	// 1. Check rootfs type by reading /proc/mounts (most universal way)
	var fsType string
	if data, err := os.ReadFile("/proc/mounts"); err == nil {
		lines := strings.Split(string(data), "\n")
		for _, line := range lines {
			fields := strings.Fields(line)
			if len(fields) >= 3 && fields[1] == "/" {
				fsType = fields[2]
				break
			}
		}
	}

	// Fallback to stat if /proc/mounts failed
	if fsType == "" {
		if out, err := exec.Command("stat", "-f", "-c", "%T", "/").Output(); err == nil {
			fsType = strings.TrimSpace(string(out))
			if fsType == "ext2/ext3" {
				fsType = "ext4"
			}
		}
	}

	if fsType == "" {
		return fmt.Errorf("could not determine root filesystem type")
	}

	if fsType != "ext4" {
		return fmt.Errorf("root filesystem is not ext4 (found: %s)", fsType)
	}

	progressChan <- Progress{Percentage: 5, Message: "Checking dependencies..."}
	// 2. Check rsync
	if _, err := exec.LookPath("rsync"); err != nil {
		return fmt.Errorf("rsync is not installed")
	}

	var imageName, mountPoint string
	var availableSpaceKB int64

	switch media {
	case Local:
		imageName = "/mnt/backup_rootfs.img"
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
		imageName = "/mnt/udisk/backup_rootfs.img"
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
		imageName = "/mnt/sdcard/backup_rootfs.img"
		mountPoint = "/mnt/sdcard/backup_img"
		var err error
		availableSpaceKB, err = getAvailableSpace("/mnt/sdcard")
		if err != nil {
			return fmt.Errorf("get available space: %w", err)
		}
	default:
		return fmt.Errorf("invalid media class")
	}

	progressChan <- Progress{Percentage: 8, Message: "Calculating required space..."}
	// 3. Calculate required space
	usedKB, err := getUsedSpace("/")
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

	progressChan <- Progress{Percentage: 60, Message: "Copying rootfs with rsync..."}
	excludes := []string{"/sys", "/mnt", "/tmp", "/proc"}
	if isMounted("/oem") {
		excludes = append(excludes, "/oem")
	}
	if isMounted("/userdata") {
		excludes = append(excludes, "/userdata")
	}

	args := []string{"-aX", "--info=progress2"}
	for _, e := range excludes {
		args = append(args, "--exclude="+e)
	}
	args = append(args, "/", mountPoint)

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

	progressChan <- Progress{Percentage: 90, Message: "Creating excluded directories..."}
	// Create excluded directories in the backup
	for _, dir := range excludes {
		os.MkdirAll(filepath.Join(mountPoint, dir), 0755)
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

func getAvailableSpace(path string) (int64, error) {
	out, err := exec.Command("sh", "-c", fmt.Sprintf("df -k %s | tail -1 | awk '{print $4}'", path)).Output()
	if err != nil {
		return 0, err
	}
	return strconv.ParseInt(strings.TrimSpace(string(out)), 10, 64)
}

func getUsedSpace(path string) (int64, error) {
	out, err := exec.Command("sh", "-c", fmt.Sprintf("df -k %s | tail -1 | awk '{print $3}'", path)).Output()
	if err != nil {
		return 0, err
	}
	return strconv.ParseInt(strings.TrimSpace(string(out)), 10, 64)
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
