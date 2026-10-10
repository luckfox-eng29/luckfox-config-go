/*
 * SPDX-FileCopyrightText: 2026 Luckfox Team
 *
 * SPDX-License-Identifier: Apache-2.0
 */

package peripheral

import (
	"fmt"
	"strconv"
	"strings"
	"sync"

	"luckfox-config/internal/logger"
)

// Pin groups live in /pinctrl/<family><controller>/<node> in the base device
// tree, e.g. /pinctrl/pwm0/pwm0m0-pins. Generated overlays used to reference
// them by device tree label (&pwm0m0_pins), which only works when the base
// device tree carries a __symbols__ section. Luckfox BSP device trees are
// built without one, so the kernel rejects the overlay with
//
//	OF: resolver: no symbols in root of device tree
//	OF: resolver: overlay phandle fixup failed: -22
//
// Resolving the numeric phandle up front keeps the overlay free of symbols.
const procDeviceTree = "/proc/device-tree"

// phandleCache caches node name → phandle for the lifetime of the process.
// A cached zero means "known to be unresolvable", so the expensive fallbacks
// (full tree walk, fdtdump on the boot partition DTB) run at most once per
// node. Phandles of base device tree nodes never change while the system is
// running, and overlays only allocate phandles for the nodes they add, so the
// cache needs no invalidation.
var phandleCache sync.Map // map[string]uint32

// pinName returns the pin group node name of one signal group, in the naming
// scheme of this board. The two schemes differ in the node names themselves,
// not only in the labels, so callers must not hardcode either form.
func (d Deps) pinName(family string, num, mux, ch int) string {
	if d.PinctrlNaming == "v2" {
		switch family {
		case "pwm":
			return fmt.Sprintf("pwm%dm%d-ch%d-pins", num, mux, ch)
		case "uart":
			return fmt.Sprintf("uart%dm%d-xfer-pins", num, mux)
		case "i2c":
			return fmt.Sprintf("i2c%dm%d-pins", num, mux)
		case "can":
			return fmt.Sprintf("can%dm%d-pins", num, mux)
		}
		return ""
	}

	switch family {
	case "pwm":
		// v1 names the PWM pin group after the channel, not the controller.
		return fmt.Sprintf("pwm%dm%d-pins", ch, mux)
	case "uart":
		return fmt.Sprintf("uart%dm%d-xfer", num, mux)
	case "i2c":
		return fmt.Sprintf("i2c%dm%d-xfer", num, mux)
	case "can":
		return fmt.Sprintf("can%dm%d-xfer", num, mux)
	}
	return ""
}

// pinPhandle resolves the phandle of a pin group node without reading
// __symbols__. Lookup order: the process cache, the running device tree
// (direct path, then a node name walk), and finally the DTB of the boot
// partition when a static overlay handle is available.
func (d Deps) pinPhandle(node string) (uint32, bool) {
	if node == "" {
		return 0, false
	}
	if v, ok := phandleCache.Load(node); ok {
		ph := v.(uint32)
		return ph, ph != 0
	}

	ph, found := d.lookupPinPhandle(node)
	if !found {
		ph = 0
	}
	phandleCache.Store(node, ph)
	return ph, found
}

func (d Deps) lookupPinPhandle(node string) (uint32, bool) {
	if dir := pinctrlDir(node); dir != "" {
		if ph, err := GetNodePhandle(procDeviceTree + "/pinctrl/" + dir + "/" + node); err == nil {
			return ph, true
		}
	}
	if ph, err := FindNodePhandle(node); err == nil {
		return ph, true
	}
	if d.Static != nil {
		if s, err := d.Static.GetPhandle(node); err == nil {
			if ph, err := parsePhandle(s); err == nil {
				return ph, true
			}
		}
	}
	return 0, false
}

// pinSnippet renders the pinctrl-0 property from pin group node names. Every
// required node must resolve; optional nodes are added when the board provides
// them. A missing required node yields an empty string instead of an error so
// the peripheral still gets its status change applied, only without a pinmux.
func (d Deps) pinSnippet(id string, required, optional []string) string {
	phandles := make([]string, 0, len(required)+len(optional))
	for _, node := range required {
		ph, ok := d.pinPhandle(node)
		if !ok {
			logger.Warn("pinctrl node not found, overlay without pinctrl-0", "id", id, "node", node)
			return ""
		}
		phandles = append(phandles, fmt.Sprintf("0x%x", ph))
	}
	for _, node := range optional {
		ph, ok := d.pinPhandle(node)
		if !ok {
			logger.Debug("optional pinctrl node not found, skipped", "id", id, "node", node)
			continue
		}
		phandles = append(phandles, fmt.Sprintf("0x%x", ph))
	}
	return fmt.Sprintf("pinctrl-0 = <%s>;", strings.Join(phandles, " "))
}

// pinctrlDir derives the /pinctrl sub-directory of a pin group node name:
// "pwm0m0-pins" → "pwm0", "uart1m0-xfer" → "uart1", "spi2ahb-clk-pins" →
// "spi2ahb". It is only a shortcut for the direct lookup path; when the guess
// is wrong the node name walk in lookupPinPhandle still finds the node.
func pinctrlDir(node string) string {
	// Skip the family letters, then the controller digits: what still follows
	// the digits (the "ahb" of spi2ahb) is part of the group name.
	i := 0
	for i < len(node) && !isDigit(node[i]) {
		i++
	}
	for i < len(node) && isDigit(node[i]) {
		i++
	}
	for i < len(node) && node[i] != '-' && node[i] != 'm' {
		i++
	}
	if i == 0 || i >= len(node) {
		return ""
	}
	return node[:i]
}

func isDigit(c byte) bool { return c >= '0' && c <= '9' }

// parsePhandle parses the hex string form used by StaticOverlay.GetPhandle
// ("0x00000024").
func parsePhandle(s string) (uint32, error) {
	v, err := strconv.ParseUint(strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(s), "0x")), 16, 32)
	if err != nil {
		return 0, fmt.Errorf("parse phandle %q: %w", s, err)
	}
	return uint32(v), nil
}
