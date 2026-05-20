/*
 * SPDX-FileCopyrightText: 2026 Luckfox Team
 *
 * SPDX-License-Identifier: Apache-2.0
 */

package overlay

import (
	"crypto/sha256"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
)

// RV1106Resource handles the RV1106 boot format:
// Offset 0: FDT Header (2048 bytes, itself a DTB)
// Offset 2048: Actual DTB data
type RV1106Resource struct {
	device string
	header []byte // 2048 bytes
	dtb    []byte
}

// OpenRV1106Resource opens the boot partition and reads the FDT structure for RV1106.
func OpenRV1106Resource(device string) (*RV1106Resource, error) {
	r := &RV1106Resource{device: device}

	f, err := os.Open(device)
	if err != nil {
		return nil, fmt.Errorf("open device: %w", err)
	}
	defer f.Close()

	// 1. Read FDT Header
	r.header = make([]byte, 2048)
	if _, err := f.ReadAt(r.header, 0); err != nil {
		return nil, fmt.Errorf("read FDT header: %w", err)
	}

	// 2. Extract DTB size from header using fdtdump
	tmpHdr := "/tmp/.rv1106_hdr.dtb"
	if err := os.WriteFile(tmpHdr, r.header, 0o644); err != nil {
		return nil, fmt.Errorf("write tmp header: %w", err)
	}
	out, err := exec.Command("fdtget", tmpHdr, "/images/fdt", "data-size").CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("fdtget data-size: %w\n%s", err, out)
	}
	v, err := strconv.ParseUint(strings.TrimSpace(string(out)), 0, 32)
	if err != nil {
		return nil, fmt.Errorf("parse DTB size %q: %w", string(out), err)
	}
	size := uint32(v)

	if size == 0 || size > 10*1024*1024 {
		return nil, fmt.Errorf("implausible DTB size: %d", size)
	}

	// 3. Read DTB data
	r.dtb = make([]byte, size)
	if _, err := f.ReadAt(r.dtb, 2048); err != nil {
		return nil, fmt.Errorf("read DTB data: %w", err)
	}

	return r, nil
}

func (r *RV1106Resource) DTB() []byte {
	return r.dtb
}

func (r *RV1106Resource) SetDTB(dtb []byte) {
	r.dtb = dtb
}

// Flush updates the header with new size/hash and writes both to disk.
func (r *RV1106Resource) Flush() error {
	// 1. Calculate SHA256 of new DTB
	hash := sha256.Sum256(r.dtb)
	var sb strings.Builder
	for i := 0; i < len(hash); i += 4 {
		if i > 0 {
			sb.WriteByte(' ')
		}
		fmt.Fprintf(&sb, "0x%02x%02x%02x%02x", hash[i], hash[i+1], hash[i+2], hash[i+3])
	}
	hashStr := sb.String()

	// 2. Prepare header overlay (DTS)
	headerDts := fmt.Sprintf(`
/dts-v1/;
/plugin/;
&{/images/fdt}{
    data-size=<0x%x>;
    hash{
        value=<%s>;
    };
};
`, len(r.dtb), hashStr)

	tmpDts := "/tmp/.rv1106_hdr_overlay.dts"
	tmpDtbo := "/tmp/.rv1106_hdr_overlay.dtbo"
	tmpHdr := "/tmp/.rv1106_hdr.dtb"

	if err := os.WriteFile(tmpDts, []byte(headerDts), 0o644); err != nil {
		return err
	}
	if err := os.WriteFile(tmpHdr, r.header, 0o644); err != nil {
		return err
	}

	// 3. Compile and apply header overlay
	if out, err := exec.Command("dtc", "-I", "dts", "-O", "dtb", tmpDts, "-o", tmpDtbo).CombinedOutput(); err != nil {
		return fmt.Errorf("dtc header: %w\n%s", err, out)
	}
	if out, err := exec.Command("fdtoverlay", "-i", tmpHdr, "-o", tmpHdr, tmpDtbo).CombinedOutput(); err != nil {
		return fmt.Errorf("fdtoverlay header: %w\n%s", err, out)
	}

	newHeader, err := os.ReadFile(tmpHdr)
	if err != nil {
		return err
	}
	r.header = newHeader

	// 4. Write to disk
	f, err := os.OpenFile(r.device, os.O_RDWR, 0)
	if err != nil {
		return err
	}
	defer f.Close()

	if _, err := f.WriteAt(r.header, 0); err != nil {
		return fmt.Errorf("write header: %w", err)
	}
	if _, err := f.WriteAt(r.dtb, 2048); err != nil {
		return fmt.Errorf("write DTB: %w", err)
	}

	if err := f.Sync(); err != nil {
		return err
	}
	syscall.Sync()
	return nil
}

var _ StaticResource = (*RV1106Resource)(nil)
