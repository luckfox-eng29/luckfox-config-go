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

	"luckfox-config/internal/config"
	"luckfox-config/internal/logger"
)

type RGB struct {
	deps                 Deps
	pins                 []string
	clk                  int
	h, v                 int
	hb, hf               int
	vb, vf               int
	hLen, vLen           int
	hActive, vActive     int
	deActive, pclkActive int
}

func NewRGB(deps Deps, pins []string) *RGB {
	return &RGB{deps: deps, pins: pins}
}

func (r *RGB) ID() string      { return "rgb" }
func (r *RGB) PinsUsed() []int { return nil }
func (r *RGB) ConfigKeys() []string {
	return []string{
		"RGB_ENABLE", "RGB_MODE", "RGB_CLK", "RGB_HACTIVE", "RGB_VACTIVE",
		"RGB_HBACKPORCH", "RGB_HFRONTPORCH", "RGB_VBACKPORCH", "RGB_VFRONTPORCH",
		"RGB_HSYNC_LEN", "RGB_VSYNC_LEN", "RGB_HSYNC_ACTIVE", "RGB_VSYNC_ACTIVE",
		"RGB_DE_ACTIVE", "RGB_PCLK_ACTIVE",
	}
}

func (r *RGB) IsActive() bool {
	return IsNodeEnabled("/syscon@ff000000/rgb")
}

func (r *RGB) IsConfigEnabled(cfg *config.Store) bool {
	return cfg.Get("RGB", "ENABLE") == "1"
}

func (r *RGB) Enable(ctx context.Context) error {
	if r.deps.Static == nil {
		return fmt.Errorf("static overlay not available")
	}

	if err := r.loadFromConfig(); err != nil {
		return err
	}

	if err := r.deps.Diagram.CheckConflictErr(r.pins); err != nil {
		return err
	}

	for _, pin := range r.pins {
		r.deps.Diagram.MarkPin(pin, true)
	}

	dts := r.buildDTS(true)
	if err := r.deps.Static.Apply(dts); err != nil {
		return err
	}

	logger.Info("RGB enabled", "resolution", r.h, "x", r.v)
	return r.deps.Cfg.SetMulti("RGB", map[string]string{
		"ENABLE": "1",
	})
}

func (r *RGB) Disable(ctx context.Context) error {
	if r.deps.Static == nil {
		return fmt.Errorf("static overlay not available")
	}

	dts := r.buildDTS(false)
	if err := r.deps.Static.Apply(dts); err != nil {
		return err
	}

	for _, pin := range r.pins {
		r.deps.Diagram.MarkPin(pin, false)
	}

	logger.Info("RGB disabled")
	return r.deps.Cfg.Set("RGB", "ENABLE", "0")
}

func (r *RGB) LoadFromConfig(ctx context.Context, cfg *config.Store, apply bool) error {
	r.deps.Cfg = cfg
	if cfg.Get("RGB", "ENABLE") != "1" {
		return nil
	}
	if apply {
		return r.Enable(ctx)
	}
	for _, pin := range r.pins {
		r.deps.Diagram.MarkPin(pin, true)
	}
	return nil
}

func (r *RGB) loadFromConfig() error {
	cfg := r.deps.Cfg
	if v := cfg.Get("RGB", "CLK"); v != "" {
		r.clk, _ = strconv.Atoi(v)
	}
	if v := cfg.Get("RGB", "HACTIVE"); v != "" {
		r.h, _ = strconv.Atoi(v)
	}
	if v := cfg.Get("RGB", "VACTIVE"); v != "" {
		r.v, _ = strconv.Atoi(v)
	}
	if v := cfg.Get("RGB", "HBACKPORCH"); v != "" {
		r.hb, _ = strconv.Atoi(v)
	}
	if v := cfg.Get("RGB", "HFRONTPORCH"); v != "" {
		r.hf, _ = strconv.Atoi(v)
	}
	if v := cfg.Get("RGB", "VBACKPORCH"); v != "" {
		r.vb, _ = strconv.Atoi(v)
	}
	if v := cfg.Get("RGB", "VFRONTPORCH"); v != "" {
		r.vf, _ = strconv.Atoi(v)
	}
	if v := cfg.Get("RGB", "HSYNC_LEN"); v != "" {
		r.hLen, _ = strconv.Atoi(v)
	}
	if v := cfg.Get("RGB", "VSYNC_LEN"); v != "" {
		r.vLen, _ = strconv.Atoi(v)
	}
	if v := cfg.Get("RGB", "HSYNC_ACTIVE"); v != "" {
		r.hActive, _ = strconv.Atoi(v)
	}
	if v := cfg.Get("RGB", "VSYNC_ACTIVE"); v != "" {
		r.vActive, _ = strconv.Atoi(v)
	}
	if v := cfg.Get("RGB", "DE_ACTIVE"); v != "" {
		r.deActive, _ = strconv.Atoi(v)
	}
	if v := cfg.Get("RGB", "PCLK_ACTIVE"); v != "" {
		r.pclkActive, _ = strconv.Atoi(v)
	}
	return nil
}

func (r *RGB) buildDTS(enable bool) string {
	status := "okay"
	cmaStatus := "disabled"
	if !enable {
		status = "disabled"
	} else if r.h > 640 || r.v > 640 {
		cmaStatus = "okay"
	}

	return fmt.Sprintf(`/dts-v1/;
/plugin/;

&{/syscon@ff000000/rgb} {
	status = "%s";
};

&{/panel} {
	status = "%s";
};

&{/reserved-memory/linux,cma} {
	status = "%s";
};

&{/panel/display-timings/timing0} {
	clock-frequency = <0x%x>;
	hactive = <0x%x>;
	vactive = <0x%x>;
	hback-porch = <0x%x>;
	hfront-porch = <0x%x>;
	vback-porch = <0x%x>;
	vfront-porch = <0x%x>;
	hsync-len = <0x%x>;
	vsync-len = <0x%x>;
	hsync-active = <0x%x>;
	vsync-active = <0x%x>;
	de-active = <0x%x>;
	pixelclk-active = <0x%x>;
};
`, status, status, cmaStatus,
		r.clk, r.h, r.v, r.hb, r.hf, r.vb, r.vf,
		r.hLen, r.vLen, r.hActive, r.vActive, r.deActive, r.pclkActive)
}

func (r *RGB) SetParams(clk, h, v, hb, hf, vb, vf, hLen, vLen, hAct, vAct, deAct, pclkAct int) {
	r.clk = clk
	r.h = h
	r.v = v
	r.hb = hb
	r.hf = hf
	r.vb = vb
	r.vf = vf
	r.hLen = hLen
	r.vLen = vLen
	r.hActive = hAct
	r.vActive = vAct
	r.deActive = deAct
	r.pclkActive = pclkAct

	kv := map[string]string{
		"CLK":          strconv.Itoa(clk),
		"HACTIVE":      strconv.Itoa(h),
		"VACTIVE":      strconv.Itoa(v),
		"HBACKPORCH":   strconv.Itoa(hb),
		"HFRONTPORCH":  strconv.Itoa(hf),
		"VBACKPORCH":   strconv.Itoa(vb),
		"VFRONTPORCH":  strconv.Itoa(vf),
		"HSYNC_LEN":    strconv.Itoa(hLen),
		"VSYNC_LEN":    strconv.Itoa(vLen),
		"HSYNC_ACTIVE": strconv.Itoa(hAct),
		"VSYNC_ACTIVE": strconv.Itoa(vAct),
		"DE_ACTIVE":    strconv.Itoa(deAct),
		"PCLK_ACTIVE":  strconv.Itoa(pclkAct),
	}
	r.deps.Cfg.SetMulti("RGB", kv)
}

func (r *RGB) GetPins() []string {
	return r.pins
}

var _ Peripheral = (*RGB)(nil)
