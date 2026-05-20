//go:build linux && (arm || arm64)

/*
 * SPDX-FileCopyrightText: 2026 Luckfox Team
 *
 * SPDX-License-Identifier: Apache-2.0
 */

package hwio

import (
	"fmt"
	"os"
	"sync/atomic"
	"syscall"
	"unsafe"
)

const devMemPageSize = 4096

// WriteReg32 writes a 32-bit value to a physical hardware register via /dev/mem.
// Requires CAP_SYS_RAWIO (root). addr must be 4-byte aligned.
func WriteReg32(addr uint32, value uint32) error {
	pageBase := addr &^ uint32(devMemPageSize-1)
	pageOff := addr & uint32(devMemPageSize-1)

	f, err := os.OpenFile("/dev/mem", os.O_RDWR|os.O_SYNC, 0)
	if err != nil {
		return fmt.Errorf("open /dev/mem: %w", err)
	}
	defer f.Close()

	data, err := syscall.Mmap(
		int(f.Fd()),
		int64(pageBase),
		devMemPageSize,
		syscall.PROT_READ|syscall.PROT_WRITE,
		syscall.MAP_SHARED,
	)
	if err != nil {
		return fmt.Errorf("mmap 0x%x: %w", pageBase, err)
	}
	defer syscall.Munmap(data) //nolint:errcheck

	// MAP_SHARED + O_SYNC ensures the write reaches the physical address.
	// atomic.StoreUint32 prevents compiler reordering on ARM.
	p := (*uint32)(unsafe.Pointer(&data[pageOff]))
	atomic.StoreUint32(p, value)
	return nil
}
