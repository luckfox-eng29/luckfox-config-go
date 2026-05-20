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

var _ Peripheral = (*CSI)(nil)
