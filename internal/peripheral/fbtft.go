/*
 * SPDX-FileCopyrightText: 2026 Luckfox Team
 *
 * SPDX-License-Identifier: Apache-2.0
 */

package peripheral

import (
	"context"
	"fmt"
	"os"
	"strings"

	"luckfox-config/internal/board"
	"luckfox-config/internal/config"
	"luckfox-config/internal/logger"
)

const defaultFBTFTCompatible = "sitronix,st7789v"

// FBTFT manages an FBTFT SPI screen driver via dynamic DTS overlay.
type FBTFT struct {
	deps       Deps
	gpioPins   []string
	spiPins    []string
	compatible string
}

func NewFBTFT(deps Deps, gpioPins, spiPins []string) *FBTFT {
	return &FBTFT{deps: deps, gpioPins: gpioPins, spiPins: spiPins}
}

func (f *FBTFT) ID() string { return "fbtft" }
func (f *FBTFT) IsActive() bool {
	overlayPath := "/sys/kernel/config/device-tree/overlays/FBTFT"
	if _, err := os.Stat(overlayPath); err == nil {
		return true
	}
	return false
}

func (f *FBTFT) IsConfigEnabled(cfg *config.Store) bool {
	return cfg.Get("FBTFT", "STATUS") == "1"
}

func (f *FBTFT) PinsUsed() []int { return nil }

func (f *FBTFT) ConfigKeys() []string {
	return []string{"FBTFT_STATUS", "FBTFT_COMPATIBLE"}
}

func (f *FBTFT) Enable(ctx context.Context) error {
	allPins := make([]string, 0, len(f.gpioPins)+len(f.spiPins))
	allPins = append(allPins, f.gpioPins...)
	allPins = append(allPins, f.spiPins...)

	if len(allPins) == 0 {
		return fmt.Errorf("fbtft: no pins configured for this board")
	}
	if f.compatible == "" {
		f.compatible = defaultFBTFTCompatible
	}

	if err := f.deps.Diagram.CheckConflictErr(allPins); err != nil {
		return err
	}

	spiPath, err := f.deps.ResolveAlias("spi0")
	if err != nil {
		return fmt.Errorf("fbtft: resolve spi0: %w", err)
	}

	dts := f.buildEnableDTS(spiPath)
	if err := f.deps.Dynamic.Apply("FBTFT", dts); err != nil {
		return err
	}

	for _, p := range f.gpioPins {
		if pin, err := board.ParseGPIO(p); err == nil {
			_ = SetPinMode(f.deps.Chip, pin.Raw(), 0)
		}
		f.deps.Diagram.MarkPin(p, true)
	}

	for _, p := range f.spiPins {
		f.deps.Diagram.MarkPin(p, true)
	}

	logger.Info("FBTFT enabled", "compatible", f.compatible, "gpio_pins", f.gpioPins, "spi_pins", f.spiPins)

	return f.deps.Cfg.SetMulti("FBTFT", map[string]string{
		"STATUS":     "1",
		"COMPATIBLE": f.compatible,
	})
}

func (f *FBTFT) buildEnableDTS(spiPath string) string {
	pinctrlLine := ""
	if f.deps.Static != nil {
		phandles := make([]string, 0, 3)
		for _, node := range []string{"spi0m0-clk", "spi0m0-mosi", "spi0m0-cs0"} {
			ph, err := f.deps.Static.GetPhandle(node)
			if err != nil {
				phandles = nil
				break
			}
			phandles = append(phandles, ph)
		}
		if len(phandles) == 3 {
			pinctrlLine = fmt.Sprintf("\tpinctrl-0 = <%s>;\n", strings.Join(phandles, " "))
		}
	}

	return fmt.Sprintf(`/dts-v1/;
/plugin/;

&{%s} {
%s	status = "okay";
};

&{%s/fbtft@0} {
	status = "okay";
	compatible = "%s";
};

&{%s/spidev@0} {
	status = "disabled";
};
`, spiPath, pinctrlLine, spiPath, f.compatible, spiPath)
}

func (f *FBTFT) Disable(ctx context.Context) error {
	spiPath, err := f.deps.ResolveAlias("spi0")
	if err != nil {
		return fmt.Errorf("fbtft: resolve spi0: %w", err)
	}

	dts := fmt.Sprintf(`/dts-v1/;
/plugin/;

&{%s} {
	status = "disabled";
};

&{%s/fbtft@0} {
	status = "disabled";
};
`, spiPath, spiPath)

	if err := f.deps.Dynamic.Apply("FBTFT", dts); err != nil {
		return err
	}

	for _, p := range f.gpioPins {
		f.deps.Diagram.MarkPin(p, false)
	}
	for _, p := range f.spiPins {
		f.deps.Diagram.MarkPin(p, false)
	}

	logger.Info("FBTFT disabled")

	return f.deps.Cfg.Set("FBTFT", "STATUS", "0")
}

func (f *FBTFT) LoadFromConfig(ctx context.Context, cfg *config.Store, apply bool) error {
	f.deps.Cfg = cfg
	if cfg.Get("FBTFT", "STATUS") != "1" {
		return nil
	}
	if c := cfg.Get("FBTFT", "COMPATIBLE"); c != "" {
		f.compatible = c
	}
	if apply {
		return f.Enable(ctx)
	}
	for _, p := range f.gpioPins {
		f.deps.Diagram.MarkPin(p, true)
	}
	for _, p := range f.spiPins {
		f.deps.Diagram.MarkPin(p, true)
	}
	return nil
}

// SetCompatible sets the panel driver compatible string before calling Enable.
func (f *FBTFT) SetCompatible(compatible string) {
	f.compatible = compatible
}

var _ Peripheral = (*FBTFT)(nil)
