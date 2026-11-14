/*
 * SPDX-FileCopyrightText: 2026 Luckfox Team
 *
 * SPDX-License-Identifier: Apache-2.0
 */

package peripheral

import (
	"context"
	"fmt"

	"luckfox-config/internal/config"
	"luckfox-config/internal/logger"
)

// csiUniteProp is the property of the ISP node that selects the unite mode.
const csiUniteProp = "rockchip,unite"

// CSI manages the camera serial interface through a static device tree overlay.
//
// The node paths come from the board description: they differ between SoCs and
// boards, so nothing here assumes a fixed address. CSI state lives in the boot
// partition device tree only — it is never mirrored into the config store.
type CSI struct {
	deps      Deps
	uniteNode string // ISP node carrying rockchip,unite
	busNode   string // I2C bus the camera sensor sits on
	unite     bool   // unite selection to be written by the next Enable
}

func NewCSI(deps Deps, uniteNode, busNode string) *CSI {
	return &CSI{deps: deps, uniteNode: uniteNode, busNode: busNode}
}

func (c *CSI) ID() string      { return "csi" }
func (c *CSI) PinsUsed() []int { return nil }

// ConfigKeys returns nil: CSI is described by the device tree, not by the
// config store.
func (c *CSI) ConfigKeys() []string { return nil }

// IsActive reports whether the ISP node is enabled in the running device tree.
func (c *CSI) IsActive() bool {
	return IsNodeEnabled(c.uniteNode)
}

// IsConfigEnabled is always false: CSI has no config store representation.
func (c *CSI) IsConfigEnabled(*config.Store) bool { return false }

func (c *CSI) Enable(ctx context.Context) error {
	return c.apply(true, c.unite)
}

func (c *CSI) Disable(ctx context.Context) error {
	return c.apply(false, false)
}

func (c *CSI) apply(enable bool, unite bool) error {
	if c.deps.Static == nil {
		return fmt.Errorf("static overlay not available")
	}

	status := "okay"
	if !enable {
		status = "disabled"
	}

	uniteVal := 0
	if unite {
		uniteVal = 1
	}

	dts := fmt.Sprintf(`/dts-v1/;
/plugin/;

&{%s} {
	status = "%s";
};

&{%s} {
	%s = <%d>;
};
`, c.busNode, status, c.uniteNode, csiUniteProp, uniteVal)

	if err := c.deps.Static.Apply(dts); err != nil {
		return err
	}

	c.unite = unite
	logger.Info("CSI configured", "enable", enable, "unite", unite)
	return nil
}

// LoadFromConfig initialises the in-memory unite selection from the boot
// partition device tree. Static overlays are not replayed: the device tree
// already holds the configuration, so load must not rewrite the boot partition.
func (c *CSI) LoadFromConfig(ctx context.Context, cfg *config.Store, apply bool) error {
	c.deps.Cfg = cfg
	c.unite = c.UniteEnabled() == 1
	return nil
}

// SetUnite selects the unite mode for the next Enable call. It only touches
// memory; the device tree is written by Enable.
func (c *CSI) SetUnite(unite bool) {
	c.unite = unite
}

// UniteEnabled reads rockchip,unite from the running device tree. It returns 1
// when unite is enabled and 0 when the property is absent, matching the legacy
// get_csi_unite subcommand of the shell implementation.
func (c *CSI) UniteEnabled() int {
	v, err := GetNodeU32(c.uniteNode, csiUniteProp)
	if err != nil {
		// Missing property means unite is disabled.
		logger.Debug("rockchip,unite not found, assuming disabled",
			"node", c.uniteNode, "error", err)
		return 0
	}
	return int(v)
}

var _ Peripheral = (*CSI)(nil)
