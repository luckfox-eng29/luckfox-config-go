/*
 * SPDX-FileCopyrightText: 2026 Luckfox Team
 *
 * SPDX-License-Identifier: Apache-2.0
 */

package backup

import "testing"

// TestParseBlkidType pins the parsing against real blkid output. The BusyBox
// variants matter most: BusyBox v1.36 does not implement "-o value -s TYPE",
// it prints the default line below and exits 0.
func TestParseBlkidType(t *testing.T) {
	tests := []struct {
		name string
		out  string
		want string
	}{
		{
			name: "busybox blkid: raw uboot partition, part label only",
			out:  "/dev/mmcblk0p1: PARTLABEL=\"uboot\"\n",
			want: "",
		},
		{
			name: "busybox blkid: raw boot partition, part label only",
			out:  "/dev/mmcblk0p2: PARTLABEL=\"boot\"\n",
			want: "",
		},
		{
			name: "busybox blkid: ext4 rootfs",
			out:  "/dev/mmcblk0p3: LABEL=\"rootfs\" UUID=\"e6d2cad0-cdb1-4dfd-9d6b-ad5747fbfae9\" TYPE=\"ext4\" PARTLABEL=\"rootfs\"\n",
			want: "ext4",
		},
		{
			name: "util-linux blkid default output",
			out:  "/dev/sda1: UUID=\"1c2f8b7a-0000-4444-9999-abcdefabcdef\" TYPE=\"ext4\" PARTUUID=\"00000001-01\"\n",
			want: "ext4",
		},
		{
			name: "device without any signature",
			out:  "",
			want: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := parseBlkidType([]byte(tt.out)); got != tt.want {
				t.Errorf("parseBlkidType(%q) = %q, want %q", tt.out, got, tt.want)
			}
		})
	}
}

// TestClassifyPartition covers the partition table of a GPT based rk3506 image
// (uboot: 4M raw, boot: 12M raw, rootfs: 7.2G ext4) plus the unsupported cases.
func TestClassifyPartition(t *testing.T) {
	tests := []struct {
		name         string
		part         PartitionInfo
		wantType     BackupType
		wantLabel    string
		wantNoReason bool
	}{
		{
			name:         "raw uboot partition",
			part:         PartitionInfo{Name: "uboot", Path: "/dev/mmcblk0p1", Index: 1, SizeStr: "4M"},
			wantType:     BackupTypeDD,
			wantLabel:    "/dev/mmcblk0p1 (uboot)",
			wantNoReason: true,
		},
		{
			name:         "raw boot partition",
			part:         PartitionInfo{Name: "boot", Path: "/dev/mmcblk0p2", Index: 2, SizeStr: "12M"},
			wantType:     BackupTypeDD,
			wantLabel:    "/dev/mmcblk0p2 (boot)",
			wantNoReason: true,
		},
		{
			name:         "ext4 rootfs partition",
			part:         PartitionInfo{Name: "rootfs", Path: "/dev/mmcblk0p3", Index: 3, FSType: "ext4", SizeStr: "7G"},
			wantType:     BackupTypeRootFS,
			wantLabel:    "/dev/mmcblk0p3 (rootfs)",
			wantNoReason: true,
		},
		{
			name:      "raw partition which is neither uboot nor boot",
			part:      PartitionInfo{Name: "userdata", Path: "/dev/mmcblk0p4", Index: 4},
			wantType:  BackupTypeUnsupported,
			wantLabel: "/dev/mmcblk0p4 (userdata)",
		},
		{
			name:      "filesystem which cannot be backed up",
			part:      PartitionInfo{Name: "userdata", Path: "/dev/mmcblk0p4", Index: 4, FSType: "squashfs"},
			wantType:  BackupTypeUnsupported,
			wantLabel: "/dev/mmcblk0p4 (userdata)",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			opt := classifyPartition(tt.part)

			if opt.BackupType != tt.wantType {
				t.Errorf("BackupType = %d, want %d", opt.BackupType, tt.wantType)
			}
			if opt.DisplayLabel != tt.wantLabel {
				t.Errorf("DisplayLabel = %q, want %q", opt.DisplayLabel, tt.wantLabel)
			}
			if tt.wantNoReason && opt.Reason != "" {
				t.Errorf("Reason = %q, want empty", opt.Reason)
			}
			if !tt.wantNoReason && opt.Reason == "" {
				t.Error("Reason is empty for an unsupported partition")
			}
		})
	}
}
