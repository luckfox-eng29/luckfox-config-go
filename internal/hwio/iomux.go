//go:build linux && (arm || arm64)

/*
 * SPDX-FileCopyrightText: 2026 Luckfox Team
 *
 * SPDX-License-Identifier: Apache-2.0
 */

package hwio

import (
	"encoding/binary"
	"fmt"
	"os"
	"syscall"
	"unsafe"
)

// ioctl constants derived from rk-iomux.h:
//
//	sizeof(iomux_ioctl_data{u32,u32,u32}) = 12, magic = 'P' = 0x50
//	_IOWR('P', 0, 12) = (3<<30)|(12<<16)|(0x50<<8)|0 = 0xC00C5000
//	_IOWR('P', 1, 12) = 0xC00C5001
const (
	iomuxIocMuxSet uintptr = 0xC00C5000
	iomuxIocMuxGet uintptr = 0xC00C5001
)

// IomuxSet sets the pin multiplexing function via /dev/iomux ioctl. Requires root.
func IomuxSet(bank, pin, mux int) error {
	f, err := os.OpenFile("/dev/iomux", os.O_RDWR, 0)
	if err != nil {
		return fmt.Errorf("open /dev/iomux: %w", err)
	}
	defer f.Close()

	var buf [12]byte
	binary.NativeEndian.PutUint32(buf[0:4], uint32(bank))
	binary.NativeEndian.PutUint32(buf[4:8], uint32(pin))
	binary.NativeEndian.PutUint32(buf[8:12], uint32(mux))

	_, _, errno := syscall.Syscall(syscall.SYS_IOCTL,
		uintptr(f.Fd()), iomuxIocMuxSet, uintptr(unsafe.Pointer(&buf[0])))
	if errno != 0 {
		return fmt.Errorf("iomux set bank=%d pin=%d mux=%d: %w", bank, pin, mux, errno)
	}
	return nil
}

// IomuxGet reads the current pin multiplexing function via /dev/iomux ioctl. Requires root.
func IomuxGet(bank, pin int) (int, error) {
	f, err := os.OpenFile("/dev/iomux", os.O_RDWR, 0)
	if err != nil {
		return 0, fmt.Errorf("open /dev/iomux: %w", err)
	}
	defer f.Close()

	var buf [12]byte
	binary.NativeEndian.PutUint32(buf[0:4], uint32(bank))
	binary.NativeEndian.PutUint32(buf[4:8], uint32(pin))

	_, _, errno := syscall.Syscall(syscall.SYS_IOCTL,
		uintptr(f.Fd()), iomuxIocMuxGet, uintptr(unsafe.Pointer(&buf[0])))
	if errno != 0 {
		return 0, fmt.Errorf("iomux get bank=%d pin=%d: %w", bank, pin, errno)
	}
	return int(binary.NativeEndian.Uint32(buf[8:12])), nil
}
