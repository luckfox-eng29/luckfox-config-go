/*
 * SPDX-FileCopyrightText: 2026 Luckfox Team
 *
 * SPDX-License-Identifier: Apache-2.0
 */

package peripheral

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"luckfox-config/internal/config"
	"luckfox-config/internal/logger"
)

// csiUniteNode is the device tree node holding the CSI unite property.
const csiUniteNode = "/rkisp@ffa00000"

func boolToStr(b bool) string {
	if b {
		return "1"
	}
	return "0"
}

type CSI struct {
	deps Deps
}

func NewCSI(deps Deps) *CSI {
	return &CSI{deps: deps}
}

func (c *CSI) ID() string      { return "csi" }
func (c *CSI) PinsUsed() []int { return nil }
func (c *CSI) ConfigKeys() []string {
	return []string{"CSI_ENABLE", "CSI_UNITE_ENABLE"}
}

func (c *CSI) IsActive() bool {
	return IsNodeEnabled("/rkisp@ffa00000")
}

func (c *CSI) IsConfigEnabled(cfg *config.Store) bool {
	return cfg.Get("CSI", "ENABLE") == "1"
}

func (c *CSI) Enable(ctx context.Context) error {
	return c.apply(true, c.deps.Cfg.Get("CSI", "UNITE") == "1")
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

&{/i2c@ff470000} {
	status = "%s";
};

&{/rkisp@ffa00000} {
	rockchip,unite = <%d>;
};
`, status, uniteVal)

	if err := c.deps.Static.Apply(dts); err != nil {
		return err
	}

	logger.Info("CSI configured", "enable", enable, "unite", unite)
	return c.deps.Cfg.SetMulti("CSI", map[string]string{
		"ENABLE": boolToStr(enable),
		"UNITE":  boolToStr(unite),
	})
}

func (c *CSI) LoadFromConfig(ctx context.Context, cfg *config.Store, apply bool) error {
	c.deps.Cfg = cfg
	if cfg.Get("CSI", "ENABLE") != "1" {
		return nil
	}
	if apply {
		return c.Enable(ctx)
	}
	return nil
}

func (c *CSI) SetUnite(unite bool) {
	c.deps.Cfg.Set("CSI", "UNITE", boolToStr(unite))
}

// UniteEnabled reads rockchip,unite from the boot partition device tree.
// It returns 1 when unite is enabled, 0 when the property is absent, matching
// the legacy get_csi_unite subcommand of the shell implementation.
func (c *CSI) UniteEnabled() (int, error) {
	if c.deps.Static == nil {
		return 0, fmt.Errorf("static overlay not available")
	}

	raw, err := c.deps.Static.GetProperty(csiUniteNode, "rockchip,unite")
	if err != nil {
		// Missing property means unite is disabled.
		logger.Debug("rockchip,unite not found, assuming disabled", "node", csiUniteNode, "error", err)
		return 0, nil
	}

	v, err := strconv.ParseInt(strings.TrimSpace(raw), 0, 32)
	if err != nil {
		return 0, fmt.Errorf("parse rockchip,unite %q: %w", raw, err)
	}
	return int(v), nil
}

var _ Peripheral = (*CSI)(nil)
