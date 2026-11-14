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

// RGB manages the RGB parallel display through a static device tree overlay.
//
// Node paths come from the board description: the device tree layout differs per
// SoC, so nothing here assumes a fixed address. The active timing is read back
// from the running device tree, never from the config store.
type RGB struct {
	deps                 Deps
	node                 string // RGB output node, e.g. /syscon@ff000000/rgb
	panelNode            string // RGB panel node, e.g. /panel
	cmaNode              string // CMA region the framebuffer needs
	pins                 []string
	clk                  int
	h, v                 int
	hb, hf               int
	vb, vf               int
	hLen, vLen           int
	hActive, vActive     int
	deActive, pclkActive int
}

func NewRGB(deps Deps, node, panelNode, cmaNode string, pins []string) *RGB {
	return &RGB{deps: deps, node: node, panelNode: panelNode, cmaNode: cmaNode, pins: pins}
}

func (r *RGB) ID() string      { return "rgb" }
func (r *RGB) PinsUsed() []int { return nil }

// ConfigKeys returns nil: RGB is described by the device tree, not by the
// config store.
func (r *RGB) ConfigKeys() []string { return nil }

// IsActive reports whether the RGB output is enabled in the running device tree.
func (r *RGB) IsActive() bool {
	return IsNodeEnabled(r.node)
}

// IsConfigEnabled is always false: RGB has no config store representation.
func (r *RGB) IsConfigEnabled(*config.Store) bool { return false }

func (r *RGB) Enable(ctx context.Context) error {
	if r.deps.Static == nil {
		return fmt.Errorf("static overlay not available")
	}

	if err := r.deps.Diagram.CheckConflictErrSelf(r.pins); err != nil {
		return err
	}

	if r.clk == 0 || r.h == 0 || r.v == 0 {
		return fmt.Errorf("no RGB timing configured")
	}

	for _, pin := range r.pins {
		r.deps.Diagram.MarkPin(pin, true)
	}

	dts := r.buildDTS(true)
	if err := r.deps.Static.Apply(dts); err != nil {
		return err
	}

	logger.Info("RGB enabled", "resolution", r.h, "x", r.v)
	return nil
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
	return nil
}

// LoadFromConfig syncs the in-memory timing with the running device tree. Static
// overlays are not replayed: the device tree already holds the configuration,
// so load must not rewrite the boot partition.
func (r *RGB) LoadFromConfig(ctx context.Context, cfg *config.Store, apply bool) error {
	r.deps.Cfg = cfg
	r.loadFromDT()
	return nil
}

// loadFromDT fills the timing fields from the device tree the kernel booted with.
// Values that cannot be read keep their current value, so a partially described
// panel still produces a valid overlay.
func (r *RGB) loadFromDT() {
	if r.panelNode == "" {
		return
	}
	timing := r.panelNode + "/display-timings/timing0"
	read := func(prop string, dst *int) {
		if v, err := GetNodeU32(timing, prop); err == nil {
			*dst = int(v)
		}
	}
	read("clock-frequency", &r.clk)
	read("hactive", &r.h)
	read("vactive", &r.v)
	read("hback-porch", &r.hb)
	read("hfront-porch", &r.hf)
	read("vback-porch", &r.vb)
	read("vfront-porch", &r.vf)
	read("hsync-len", &r.hLen)
	read("vsync-len", &r.vLen)
	read("hsync-active", &r.hActive)
	read("vsync-active", &r.vActive)
	read("de-active", &r.deActive)
	read("pixelclk-active", &r.pclkActive)
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

&{%s} {
	status = "%s";
};

&{%s} {
	status = "%s";
};

&{%s} {
	status = "%s";
};

&{%s/display-timings/timing0} {
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
`, r.node, status, r.panelNode, status, r.cmaNode, cmaStatus,
		r.panelNode, r.clk, r.h, r.v, r.hb, r.hf, r.vb, r.vf,
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
}

func (r *RGB) GetPins() []string {
	return r.pins
}

var _ Peripheral = (*RGB)(nil)
