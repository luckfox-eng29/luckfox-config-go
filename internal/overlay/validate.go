/*
 * SPDX-FileCopyrightText: 2026 Luckfox Team
 *
 * SPDX-License-Identifier: Apache-2.0
 */

package overlay

import (
	"fmt"
	"strings"
)

// CheckNoSymbolRefs rejects DTS content that references device tree symbols
// (&label, which dtc turns into a __fixups__ entry). Luckfox BSP device trees
// are compiled without a __symbols__ section, so the kernel cannot resolve
// such a reference: it fails with
//
//	OF: resolver: no symbols in root of device tree
//	OF: resolver: overlay phandle fixup failed: -22
//
// and the overlay never applies. Generated overlays must target nodes by
// absolute path (&{/pwm@ff350000}) and reference pinctrl nodes by numeric
// phandle. This guard catches such a reference at the last possible moment,
// with a message that names the offending line.
func CheckNoSymbolRefs(name, dts string) error {
	i := strings.Index(dts, "<&")
	if i < 0 {
		return nil
	}
	line := dts[i:]
	if nl := strings.IndexByte(line, '\n'); nl >= 0 {
		line = line[:nl]
	}
	return fmt.Errorf("%s: DTS uses a device tree symbol reference (%s); "+
		"the base device tree has no __symbols__ section, use a numeric phandle instead",
		name, strings.TrimSpace(line))
}
