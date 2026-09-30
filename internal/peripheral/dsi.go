/*
 * SPDX-FileCopyrightText: 2026 Luckfox Team
 *
 * SPDX-License-Identifier: Apache-2.0
 */

package peripheral

import (
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"strings"

	"luckfox-config/internal/config"
)

// DSIPanel holds timing and init data for a single MIPI DSI panel.
type DSIPanel struct {
	ID             string      `json:"id"`
	DisplayName    string      `json:"display_name"`
	Lanes          int         `json:"lanes"`
	Format         *int        `json:"format,omitempty"`
	ModeFlags      *int        `json:"mode_flags,omitempty"`
	ClockFrequency int         `json:"clock_frequency"`
	HActive        int         `json:"hactive"`
	VActive        int         `json:"vactive"`
	VSyncLen       int         `json:"vsync_len"`
	VBackPorch     int         `json:"vback_porch"`
	VFrontPorch    int         `json:"vfront_porch"`
	HSyncLen       int         `json:"hsync_len"`
	HBackPorch     int         `json:"hback_porch"`
	HFrontPorch    int         `json:"hfront_porch"`
	VSyncActive    int         `json:"vsync_active"`
	HSyncActive    int         `json:"hsync_active"`
	DEActive       int         `json:"de_active"`
	PixelClkActive int         `json:"pixelclk_active"`
	WidthMM        int         `json:"width_mm,omitempty"`
	HeightMM       int         `json:"height_mm,omitempty"`
	PanelInitSeq   string      `json:"panel_init_sequence,omitempty"`
	PanelExitSeq   string      `json:"panel_exit_sequence,omitempty"`
	Touch          *PanelTouch `json:"touch,omitempty"`
}

// PanelTouch carries the touch controller geometry of a panel. Selecting the
// panel writes these properties onto the touch node of the board; properties
// that are not set are left out (the driver then uses its defaults).
type PanelTouch struct {
	SizeX     int  `json:"size_x,omitempty"`
	SizeY     int  `json:"size_y,omitempty"`
	InvertedX bool `json:"inverted_x,omitempty"`
	InvertedY bool `json:"inverted_y,omitempty"`
	SwappedXY bool `json:"swapped_x_y,omitempty"`
}

// DSIPanelFamily groups panels of the same connector type.
type DSIPanelFamily struct {
	Family      string     `json:"family"`
	DisplayName string     `json:"display_name"`
	Panels      []DSIPanel `json:"panels"`
}

// LoadPanelFamilies loads all panel JSON files from the embedded FS.
func LoadPanelFamilies(embedFS embed.FS, dir string) ([]DSIPanelFamily, error) {
	entries, err := fs.ReadDir(embedFS, dir)
	if err != nil {
		return nil, fmt.Errorf("reading panels dir: %w", err)
	}
	var families []DSIPanelFamily
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		data, err := embedFS.ReadFile(dir + "/" + e.Name())
		if err != nil {
			return nil, err
		}
		var fam DSIPanelFamily
		if err := json.Unmarshal(data, &fam); err != nil {
			return nil, fmt.Errorf("parsing %s: %w", e.Name(), err)
		}
		families = append(families, fam)
	}
	return families, nil
}

// FindPanel searches all families for a panel by family name and panel ID.
func FindPanel(families []DSIPanelFamily, family, id string) (*DSIPanel, error) {
	for i := range families {
		if families[i].Family != family {
			continue
		}
		for j := range families[i].Panels {
			if families[i].Panels[j].ID == id {
				return &families[i].Panels[j], nil
			}
		}
	}
	return nil, fmt.Errorf("panel %s/%s not found", family, id)
}

// defaultDSIModeFlags is the default MIPI DSI mode flag bitmask (VSYNC/HSYNC active-high, DE active-high, burst mode).
const defaultDSIModeFlags = 0x4b

// touchProps are the device tree properties that describe the touch geometry of
// a panel. They are cleared before a newly selected panel's values are written,
// because fdtoverlay cannot remove the values of the previous panel.
var touchProps = []string{
	"touchscreen-size-x",
	"touchscreen-size-y",
	"touchscreen-inverted-x",
	"touchscreen-inverted-y",
	"touchscreen-swapped-x-y",
}

// DSI manages MIPI DSI display configuration via static FDT overlay.
type DSI struct {
	deps      Deps
	fdtNode   string // e.g. "/dsi@ff640000"
	touchNode string // touch controller node, empty when the board has none
	families  []DSIPanelFamily
}

func NewDSI(deps Deps, fdtNode string, families []DSIPanelFamily) *DSI {
	return &DSI{deps: deps, fdtNode: fdtNode, touchNode: deps.Touch.Node, families: families}
}

func (d *DSI) ID() string { return "dsi" }
func (d *DSI) IsActive() bool {
	return IsNodeEnabled(d.fdtNode)
}
func (d *DSI) IsConfigEnabled(cfg *config.Store) bool {
	return cfg.Get("DSI", "TYPE") != ""
}

func (d *DSI) PinsUsed() []int { return nil } // DSI uses dedicated lanes, not RM_IO
func (d *DSI) ConfigKeys() []string {
	return []string{"DSI_TYPE", "DSI_SIZE", "DSI_LOGO_ROTATE"}
}

func (d *DSI) Enable(ctx context.Context) error {
	family := d.deps.Cfg.Get("DSI", "TYPE")
	id := d.deps.Cfg.Get("DSI", "SIZE")
	if family == "" || id == "" {
		return fmt.Errorf("DSI TYPE and SIZE must be set in config")
	}
	return d.Apply(ctx, family, id)
}

func (d *DSI) Disable(ctx context.Context) error {
	return d.deps.Cfg.SetMulti("DSI", map[string]string{
		"TYPE": "",
		"SIZE": "",
	})
}

func (d *DSI) LoadFromConfig(ctx context.Context, cfg *config.Store, apply bool) error {
	d.deps.Cfg = cfg
	if cfg.Get("DSI", "TYPE") == "" {
		return nil
	}
	if apply {
		return d.Enable(ctx)
	}
	return nil
}

// Apply configures a specific DSI panel by family and ID.
func (d *DSI) Apply(ctx context.Context, family, id string) error {
	if d.deps.Static == nil {
		return fmt.Errorf("static overlay not available")
	}

	panel, err := FindPanel(d.families, family, id)
	if err != nil {
		return err
	}

	// Build DTS overlay for panel timing and init sequences
	dts := d.buildTimingDTS(panel)
	if err := d.deps.Static.Apply(dts); err != nil {
		return err
	}

	// fdtoverlay can only add or override properties, so a sequence left behind
	// by a previously selected panel survives the overlay and keeps running on
	// the new panel. Drop it explicitly when the panel defines none.
	var drop []string
	if panel.PanelInitSeq == "" {
		drop = append(drop, "panel-init-sequence")
	}
	if panel.PanelExitSeq == "" {
		drop = append(drop, "panel-exit-sequence")
	}
	if len(drop) > 0 {
		if err := d.deps.Static.DeleteProperties(drop...); err != nil {
			return err
		}
	}

	if err := d.applyTouch(panel); err != nil {
		return err
	}

	return d.deps.Cfg.SetMulti("DSI", map[string]string{
		"TYPE": family,
		"SIZE": id,
	})
}

// applyTouch writes the touch geometry of the selected panel onto the touch
// node of the board. The properties of the previously selected panel are
// dropped first, so switching to a panel without geometry does not leave stale
// values behind.
func (d *DSI) applyTouch(p *DSIPanel) error {
	if d.touchNode == "" {
		return nil
	}
	if err := d.deps.Static.DeleteProperties(touchProps...); err != nil {
		return err
	}
	if p.Touch == nil || !p.Touch.hasValues() {
		return nil
	}

	dts := fmt.Sprintf("/dts-v1/;\n/plugin/;\n\n&{%s} {\n", d.touchNode)
	if p.Touch.SizeX > 0 {
		dts += fmt.Sprintf("\ttouchscreen-size-x = <%d>;\n", p.Touch.SizeX)
	}
	if p.Touch.SizeY > 0 {
		dts += fmt.Sprintf("\ttouchscreen-size-y = <%d>;\n", p.Touch.SizeY)
	}
	if p.Touch.InvertedX {
		dts += "\ttouchscreen-inverted-x;\n"
	}
	if p.Touch.InvertedY {
		dts += "\ttouchscreen-inverted-y;\n"
	}
	if p.Touch.SwappedXY {
		dts += "\ttouchscreen-swapped-x-y;\n"
	}
	dts += "};\n"

	return d.deps.Static.Apply(dts)
}

// hasValues reports whether the panel carries any touch geometry at all.
func (t PanelTouch) hasValues() bool {
	return t.SizeX > 0 || t.SizeY > 0 || t.InvertedX || t.InvertedY || t.SwappedXY
}

// GetResolution returns the configured panel resolution from the device tree.
func (d *DSI) GetResolution() (string, string) {
	if d.deps.Static == nil {
		return "", ""
	}
	node := d.fdtNode + "/panel@0/display-timings/timing0"
	h, _ := d.deps.Static.GetProperty(node, "hactive")
	v, _ := d.deps.Static.GetProperty(node, "vactive")
	return h, v
}

// SetLogoRotate sets the display logo rotation (angle: 0, 90, 180, 270).
func (d *DSI) SetLogoRotate(ctx context.Context, angle int) error {
	if d.deps.Static == nil {
		return fmt.Errorf("static overlay not available")
	}

	dts := fmt.Sprintf(`/dts-v1/;
/plugin/;

&{/display-subsystem/route/route-dsi} {
	logo,rotate = <%d>;
};
`, angle)

	if err := d.deps.Static.Apply(dts); err != nil {
		return err
	}
	return d.deps.Cfg.Set("DSI", "LOGO_ROTATE", fmt.Sprintf("%d", angle))
}

func (d *DSI) buildTimingDTS(p *DSIPanel) string {
	format := 0
	if p.Format != nil {
		format = *p.Format
	}
	modeFlags := defaultDSIModeFlags
	if p.ModeFlags != nil {
		modeFlags = *p.ModeFlags
	}

	dts := fmt.Sprintf(`/dts-v1/;
/plugin/;

&{%s/panel@0} {
	status = "okay";
	dsi,lanes = <%d>;
	dsi,format = <%d>;
	dsi,flags = <0x%x>;
`, d.fdtNode, p.Lanes, format, modeFlags)

	if p.WidthMM > 0 {
		dts += fmt.Sprintf("\twidth-mm = <%d>;\n", p.WidthMM)
	}
	if p.HeightMM > 0 {
		dts += fmt.Sprintf("\theight-mm = <%d>;\n", p.HeightMM)
	}

	if p.PanelInitSeq != "" {
		dts += fmt.Sprintf("\tpanel-init-sequence = [%s];\n", p.PanelInitSeq)
	}
	if p.PanelExitSeq != "" {
		dts += fmt.Sprintf("\tpanel-exit-sequence = [%s];\n", p.PanelExitSeq)
	}

	dts += fmt.Sprintf(`	display-timings {
		timing0: timing0 {
			clock-frequency = <%d>;
			hactive = <%d>;
			vactive = <%d>;
			vsync-len = <%d>;
			vback-porch = <%d>;
			vfront-porch = <%d>;
			hsync-len = <%d>;
			hback-porch = <%d>;
			hfront-porch = <%d>;
			vsync-active = <%d>;
			hsync-active = <%d>;
			de-active = <%d>;
			pixelclk-active = <%d>;
		};
	};
};
`,
		p.ClockFrequency,
		p.HActive, p.VActive,
		p.VSyncLen, p.VBackPorch, p.VFrontPorch,
		p.HSyncLen, p.HBackPorch, p.HFrontPorch,
		p.VSyncActive, p.HSyncActive, p.DEActive, p.PixelClkActive,
	)

	return dts
}

var _ Peripheral = (*DSI)(nil)
