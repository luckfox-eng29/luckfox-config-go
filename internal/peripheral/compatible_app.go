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

// CompatibleApp represents a preset hardware combination (e.g. DHT11, Pico_LCD).
// Each app orchestrates multiple sub-peripheral DTS overlays based on the board model.
type CompatibleApp struct {
	name    string
	boardID string
	deps    Deps
}

func NewCompatibleApp(name, boardID string, deps Deps) *CompatibleApp {
	return &CompatibleApp{name: name, boardID: boardID, deps: deps}
}

func (c *CompatibleApp) ID() string { return "app_" + c.name }
func (c *CompatibleApp) IsActive() bool {
	// Check for a representative overlay or hardware node for each app
	switch c.name {
	case "DHT11":
		return IsNodeEnabled("/dht11_sensor")
	case "Pico_ePaper", "OLED_Module", "Pico_OLED", "Pico_LCD", "Pico_ResTouch_LCD":
		// These all use SPI0. Check if SPI0 is enabled.
		return Exists("/dev/spidev0.*")
	case "Pico_UPS_B":
		// Uses I2C3.
		return Exists("/dev/i2c-3")
	}
	return false
}

func (c *CompatibleApp) IsConfigEnabled(cfg *config.Store) bool {
	return cfg.Get("APP_"+c.name, "STATUS") == "1"
}

func (c *CompatibleApp) PinsUsed() []int { return nil }

func (c *CompatibleApp) ConfigKeys() []string {
	return []string{"APP_" + c.name + "_STATUS"}
}

func (c *CompatibleApp) Enable(ctx context.Context) error {
	var err error
	switch c.name {
	case "DHT11":
		err = c.enableDHT11()
	case "Pico_ePaper":
		err = c.enablePicoEPaper()
	case "Pico_UPS_B":
		err = c.enablePicoUPSB()
	case "OLED_Module":
		err = c.enableOLEDModule()
	case "Pico_OLED":
		err = c.enablePicoOLED()
	case "Pico_LCD":
		err = c.enablePicoLCD()
	case "Pico_ResTouch_LCD":
		err = c.enablePicoResTouchLCD()
	default:
		return fmt.Errorf("compatible app %q: not implemented", c.name)
	}
	if err != nil {
		return err
	}
	logger.Info("Compatible app enabled", "name", c.name, "board", c.boardID)
	return c.deps.Cfg.Set("APP_"+c.name, "STATUS", "1")
}

func (c *CompatibleApp) Disable(ctx context.Context) error {
	if c.name == "DHT11" {
		if err := c.applyNode("dht11", "/dht11_sensor", "disabled"); err != nil {
			return err
		}
	}
	logger.Info("Compatible app disabled", "name", c.name)
	return c.deps.Cfg.Set("APP_"+c.name, "STATUS", "0")
}

func (c *CompatibleApp) LoadFromConfig(ctx context.Context, cfg *config.Store, apply bool) error {
	c.deps.Cfg = cfg
	if cfg.Get("APP_"+c.name, "STATUS") == "1" {
		if apply {
			return c.Enable(ctx)
		}
	}
	return nil
}

// ─── App implementations ──────────────────────────────────────────────────────

func (c *CompatibleApp) enableDHT11() error {
	// Free GPIO1_C7 (PWM11_M1) for DHT11 data line
	if err := c.applyPWM(11, 1, false); err != nil {
		return err
	}
	return c.applyNode("dht11", "/dht11_sensor", "okay")
}

func (c *CompatibleApp) enablePicoEPaper() error {
	for _, m := range [][2]int{{0, 0}, {2, 2}, {4, 2}, {5, 2}, {6, 2}, {1, 0}} {
		if err := c.applyPWM(m[0], m[1], false); err != nil {
			return err
		}
	}
	return c.applySPI(0, 0, true, false, true)
}

func (c *CompatibleApp) enablePicoUPSB() error {
	if err := c.applyPWM(0, 1, false); err != nil {
		return err
	}
	if err := c.applyPWM(11, 2, false); err != nil {
		return err
	}
	return c.applyI2C(3, 1, 1000000, true)
}

func (c *CompatibleApp) enableOLEDModule() error {
	for _, m := range [][2]int{{11, 1}, {10, 1}, {8, 1}, {0, 1}, {11, 2}, {4, 2}, {5, 2}} {
		if err := c.applyPWM(m[0], m[1], false); err != nil {
			return err
		}
	}
	if err := c.applyUART(4, 1, false); err != nil {
		return err
	}
	if err := c.applySPI(0, 0, false, false, true); err != nil {
		return err
	}
	return c.applyI2C(3, 1, defaultI2CSpeed, true)
}

func (c *CompatibleApp) enablePicoOLED() error {
	if err := c.applyUART(3, 1, false); err != nil {
		return err
	}
	for _, m := range [][2]int{{0, 0}, {0, 1}, {11, 2}} {
		if err := c.applyPWM(m[0], m[1], false); err != nil {
			return err
		}
	}
	if err := c.applySPI(0, 0, false, false, true); err != nil {
		return err
	}
	if err := c.applyI2C(3, 1, defaultI2CSpeed, true); err != nil {
		return err
	}
	switch c.boardID {
	case "pico_pro_max":
		if err := c.applyUART(1, 1, false); err != nil {
			return err
		}
	case "pico_plus":
		if err := c.applyUART(5, 0, false); err != nil {
			return err
		}
	}
	return nil
}

func (c *CompatibleApp) enablePicoLCD() error {
	for _, m := range [][2]int{{11, 1}, {10, 1}, {0, 0}, {2, 2}, {4, 2}, {5, 2}, {6, 2}, {1, 0}, {10, 2}} {
		if err := c.applyPWM(m[0], m[1], false); err != nil {
			return err
		}
	}
	if err := c.applyUART(3, 1, false); err != nil {
		return err
	}
	switch c.boardID {
	case "pico_pro_max":
		if err := c.applyUART(1, 1, false); err != nil {
			return err
		}
		if err := c.applyI2C(4, 0, defaultI2CSpeed, false); err != nil {
			return err
		}
	case "pico_plus":
		if err := c.applyPWM(9, 0, false); err != nil {
			return err
		}
		if err := c.applyPWM(11, 0, false); err != nil {
			return err
		}
		if err := c.applyUART(5, 0, false); err != nil {
			return err
		}
	case "pico":
		// no extra steps
	}
	return c.applySPI(0, 0, true, false, true)
}

func (c *CompatibleApp) enablePicoResTouchLCD() error {
	for _, m := range [][2]int{{8, 1}, {0, 0}, {2, 2}, {1, 0}, {10, 2}} {
		if err := c.applyPWM(m[0], m[1], false); err != nil {
			return err
		}
	}
	for _, u := range [][2]int{{3, 1}, {4, 1}} {
		if err := c.applyUART(u[0], u[1], false); err != nil {
			return err
		}
	}
	if err := c.applySPI(0, 0, false, true, true); err != nil {
		return err
	}
	switch c.boardID {
	case "pico_pro_max":
		if err := c.applyUART(1, 1, false); err != nil {
			return err
		}
		if err := c.applyI2C(4, 0, defaultI2CSpeed, false); err != nil {
			return err
		}
		if err := c.applyI2C(3, 0, defaultI2CSpeed, false); err != nil {
			return err
		}
	case "pico_plus":
		if err := c.applyUART(5, 0, false); err != nil {
			return err
		}
		if err := c.applyI2C(0, 2, defaultI2CSpeed, true); err != nil {
			return err
		}
	}
	return nil
}

// ─── Sub-peripheral helpers ────────────────────────────────────────────────────

// applyPWM enables or disables PWM{main} M{sub} via dynamic overlay.
func (c *CompatibleApp) applyPWM(main, sub int, enable bool) error {
	devPath, err := c.deps.ResolveAlias(fmt.Sprintf("pwm%d", main))
	if err != nil {
		return fmt.Errorf("pwm%d: resolve: %w", main, err)
	}
	status := "disabled"
	if enable {
		status = "okay"
	}
	dts := fmt.Sprintf("/dts-v1/;\n/plugin/;\n\n&{%s} {\n\tstatus = \"%s\";\n};\n", devPath, status)
	if err := c.deps.Dynamic.Apply(fmt.Sprintf("pwm%dm%d", main, sub), dts); err != nil {
		return err
	}
	/*
		if c.deps.Static != nil {
			_ = c.deps.Static.Apply(dts)
		}
	*/
	return nil
}

// applyUART enables or disables UART{main} M{sub} via dynamic overlay.
func (c *CompatibleApp) applyUART(main, sub int, enable bool) error {
	devPath, err := c.deps.ResolveAlias(fmt.Sprintf("serial%d", main))
	if err != nil {
		return fmt.Errorf("uart%d: resolve: %w", main, err)
	}
	status := "disabled"
	if enable {
		status = "okay"
	}
	dts := fmt.Sprintf("/dts-v1/;\n/plugin/;\n\n&{%s} {\n\tstatus = \"%s\";\n};\n", devPath, status)
	if err := c.deps.Dynamic.Apply(fmt.Sprintf("uart%dm%d", main, sub), dts); err != nil {
		return err
	}
	/*
		if c.deps.Static != nil {
			_ = c.deps.Static.Apply(dts)
		}
	*/
	return nil
}

// applyI2C enables or disables I2C{main} M{sub} via dynamic overlay.
func (c *CompatibleApp) applyI2C(main, sub, speed int, enable bool) error {
	devPath, err := c.deps.ResolveAlias(fmt.Sprintf("i2c%d", main))
	if err != nil {
		return fmt.Errorf("i2c%d: resolve: %w", main, err)
	}
	var dts string
	if enable {
		dts = fmt.Sprintf("/dts-v1/;\n/plugin/;\n\n&{%s} {\n\tclock-frequency = <%d>;\n\tstatus = \"okay\";\n};\n", devPath, speed)
	} else {
		dts = fmt.Sprintf("/dts-v1/;\n/plugin/;\n\n&{%s} {\n\tstatus = \"disabled\";\n};\n", devPath)
	}
	if err := c.deps.Dynamic.Apply(fmt.Sprintf("i2c%dm%d", main, sub), dts); err != nil {
		return err
	}
	/*
		if c.deps.Static != nil {
			_ = c.deps.Static.Apply(dts)
		}
	*/
	return nil
}

// applySPI enables or disables SPI{main} M{sub} via dynamic overlay.
// cs and miso indicate whether those SPI lines are active; passed through for
// future pinctrl-0 phandle selection when deps.Static is available.
func (c *CompatibleApp) applySPI(main, sub int, cs, miso bool, enable bool) error {
	devPath, err := c.deps.ResolveAlias(fmt.Sprintf("spi%d", main))
	if err != nil {
		return fmt.Errorf("spi%d: resolve: %w", main, err)
	}
	status := "disabled"
	if enable {
		status = "okay"
	}
	_ = cs
	_ = miso
	dts := fmt.Sprintf(`/dts-v1/;
/plugin/;

&{%s} {
	status = "%s";
};

&{%s/spidev@0} {
	status = "%s";
};
`, devPath, status, devPath, status)
	if err := c.deps.Dynamic.Apply(fmt.Sprintf("spi%dm%d", main, sub), dts); err != nil {
		return err
	}
	/*
		if c.deps.Static != nil {
			_ = c.deps.Static.Apply(dts)
		}
	*/
	return nil
}

// applyNode sets the status of an arbitrary DT node via dynamic overlay.
func (c *CompatibleApp) applyNode(overlayID, nodePath, status string) error {
	dts := fmt.Sprintf("/dts-v1/;\n/plugin/;\n\n&{%s} {\n\tstatus = \"%s\";\n};\n", nodePath, status)
	if err := c.deps.Dynamic.Apply(overlayID, dts); err != nil {
		return err
	}
	/*
		if c.deps.Static != nil {
			_ = c.deps.Static.Apply(dts)
		}
	*/
	return nil
}

var _ Peripheral = (*CompatibleApp)(nil)
