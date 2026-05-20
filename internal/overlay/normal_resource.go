/*
 * SPDX-FileCopyrightText: 2026 Luckfox Team
 *
 * SPDX-License-Identifier: Apache-2.0
 */

package overlay

import (
	"crypto/sha1"
	"encoding/binary"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
)

// resourceImg offsets within the 512-byte content block at data-position+512.
const (
	sha1Offset       = 0xe0  // 20-byte SHA1 of rk-kernel.dtb
	sizeOffset       = 0x108 // 4-byte little-endian size of rk-kernel.dtb
	contentBlockSize = 512
	dtbDataOffset    = 2048 // DTB starts at data-position+2048
)

// NormalResource handles reading and writing the Rockchip resource.img format.
type NormalResource struct {
	device       string
	dataPosition int64 // byte offset; obtained from fdtget
	contentBlock [contentBlockSize]byte
	dtb          []byte
}

// OpenNormalResource opens the boot partition and reads the FDT.
func OpenNormalResource(device string) (*NormalResource, error) {
	r := &NormalResource{device: device}
	if err := r.readDataPosition(); err != nil {
		return nil, err
	}
	if err := r.readContentBlock(); err != nil {
		return nil, err
	}
	if err := r.readDTB(); err != nil {
		return nil, err
	}
	return r, nil
}

// DTB returns the raw DTB bytes (for use with dtc/fdtoverlay).
func (r *NormalResource) DTB() []byte {
	return r.dtb
}

// SetDTB replaces the in-memory DTB. Call Flush() to write back.
func (r *NormalResource) SetDTB(dtb []byte) {
	r.dtb = dtb
}

// Flush writes the DTB back to the boot partition and updates the content block
// (SHA1 at 0xe0, little-endian size at 0x108).
func (r *NormalResource) Flush() error {
	// Update SHA1
	hash := sha1.Sum(r.dtb)
	copy(r.contentBlock[sha1Offset:sha1Offset+20], hash[:])

	// Update size
	binary.LittleEndian.PutUint32(r.contentBlock[sizeOffset:], uint32(len(r.dtb)))

	f, err := os.OpenFile(r.device, os.O_RDWR, 0)
	if err != nil {
		return fmt.Errorf("open device %s: %w", r.device, err)
	}
	defer f.Close()

	// Write content block
	if _, err := f.WriteAt(r.contentBlock[:], r.dataPosition+512); err != nil {
		return fmt.Errorf("write content block: %w", err)
	}

	// Write DTB
	if _, err := f.WriteAt(r.dtb, r.dataPosition+dtbDataOffset); err != nil {
		return fmt.Errorf("write DTB: %w", err)
	}

	if err := f.Sync(); err != nil {
		return err
	}
	syscall.Sync()
	return nil
}

func (r *NormalResource) readDataPosition() error {
	// Use fdtget to read data-position from the FDT header in the first 2048 bytes.
	// First extract the header to a temp file.
	hdrFile := "/tmp/.fdt_header.dtb"
	f, err := os.Open(r.device)
	if err != nil {
		return fmt.Errorf("open device: %w", err)
	}
	defer f.Close()

	hdr := make([]byte, 2048)
	if _, err := f.ReadAt(hdr, 0); err != nil {
		return fmt.Errorf("read header: %w", err)
	}
	if err := os.WriteFile(hdrFile, hdr, 0o644); err != nil {
		return fmt.Errorf("write header tmp: %w", err)
	}

	out, err := exec.Command("fdtget", hdrFile, "/images/resource", "data-position").Output()
	if err != nil {
		return fmt.Errorf("fdtget data-position: %w", err)
	}
	pos, err := strconv.ParseInt(strings.TrimSpace(string(out)), 10, 64)
	if err != nil {
		return fmt.Errorf("parse data-position: %w", err)
	}
	r.dataPosition = pos
	return nil
}

func (r *NormalResource) readContentBlock() error {
	f, err := os.Open(r.device)
	if err != nil {
		return fmt.Errorf("open device: %w", err)
	}
	defer f.Close()
	_, err = f.ReadAt(r.contentBlock[:], r.dataPosition+512)
	return err
}

func (r *NormalResource) readDTB() error {
	size := int64(binary.LittleEndian.Uint32(r.contentBlock[sizeOffset:]))
	if size == 0 || size > 10*1024*1024 {
		return fmt.Errorf("implausible DTB size: %d", size)
	}
	r.dtb = make([]byte, size)
	f, err := os.Open(r.device)
	if err != nil {
		return fmt.Errorf("open device: %w", err)
	}
	defer f.Close()
	_, err = f.ReadAt(r.dtb, r.dataPosition+dtbDataOffset)
	return err
}

var _ StaticResource = (*NormalResource)(nil)
