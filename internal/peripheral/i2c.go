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
	"strconv"
	"strings"

	"luckfox-config/internal/board"
	"luckfox-config/internal/config"
	"luckfox-config/internal/logger"
)

const defaultI2CSpeed = 100000

// i2cPinModes maps I2C number → {SDA mode, SCL mode}.
var i2cPinModes = map[int][2]int{
	0: {31, 30},
	1: {33, 32},
	2: {35, 34},
}

// I2C manages an I2C peripheral via dynamic DTS overlay.
type I2C struct {
	num    int
	mux    int // Only used in non-RMIO mode
	deps   Deps
	sdaPin int // RM_IO index, only for RMIO mode
	sclPin int
	speed  int
}

func NewI2C(num int, deps Deps) *I2C {
	return &I2C{num: num, mux: 0, deps: deps, sdaPin: -1, sclPin: -1, speed: defaultI2CSpeed}
}

func (c *I2C) ID() string {
	if strings.ToLower(c.deps.ConfigMode) == "rmio" {
		return fmt.Sprintf("i2c%d", c.num)
	}
	return fmt.Sprintf("i2c%dm%d", c.num, c.mux)
}

func (c *I2C) PinsUsed() []int {
	if strings.ToLower(c.deps.ConfigMode) == "rmio" {
		if c.sdaPin < 0 || c.sclPin < 0 {
			return nil
		}
		return []int{c.sdaPin, c.sclPin}
	}
	return nil
}

func (c *I2C) ConfigKeys() []string {
	if strings.ToLower(c.deps.ConfigMode) == "rmio" {
		p := fmt.Sprintf("I2C%d_", c.num)
		return []string{p + "STATUS", p + "SDA_RM_IO", p + "SCL_RM_IO", p + "SPEED"}
	}
	p := fmt.Sprintf("I2C%d_M%d_", c.num, c.mux)
	return []string{p + "STATUS", p + "SPEED"}
}

func (c *I2C) Enable(ctx context.Context) error {
	if strings.ToLower(c.deps.ConfigMode) == "rmio" {
		if c.sdaPin < 0 || c.sclPin < 0 {
			return fmt.Errorf("i2c%d: SDA and SCL pins must be set before enabling", c.num)
		}

		sdaName := board.RMIOName(c.sdaPin)
		sclName := board.RMIOName(c.sclPin)

		if err := c.deps.Diagram.CheckConflictErr([]string{sdaName, sclName}); err != nil {
			return err
		}

		dts := c.buildDTS("okay")
		if err := c.deps.Dynamic.Apply(c.ID(), dts); err != nil {
			return err
		}

		/*
			if c.deps.Static != nil {
				if err := c.deps.Static.Apply(dts); err != nil {
					logger.Error("Static overlay apply failed", "peripheral", c.ID(), "error", err)
				}
			}
		*/

		modes := i2cPinModes[c.num]
		if err := SetPinMode(c.deps.Chip, c.deps.MapRMIOToGPIO(c.sdaPin), modes[0]); err != nil {
			return err
		}
		if err := SetPinMode(c.deps.Chip, c.deps.MapRMIOToGPIO(c.sclPin), modes[1]); err != nil {
			return err
		}

		// Enable pull-ups on I2C pins
		_ = SetPull(c.deps.Chip, c.deps.MapRMIOToGPIO(c.sdaPin), PullUp)
		_ = SetPull(c.deps.Chip, c.deps.MapRMIOToGPIO(c.sclPin), PullUp)

		c.deps.Diagram.MarkPin(sdaName, true)
		c.deps.Diagram.MarkPin(sclName, true)

		logger.Info("I2C enabled", "num", c.num, "sda", sdaName, "scl", sclName, "speed", c.speed)

		return c.deps.Cfg.SetMulti(fmt.Sprintf("I2C%d", c.num), map[string]string{
			"STATUS":    "1",
			"SDA_RM_IO": strconv.Itoa(c.sdaPin),
			"SCL_RM_IO": strconv.Itoa(c.sclPin),
			"SPEED":     strconv.Itoa(c.speed),
		})
	}

	// Non-RMIO mode
	sdaName := fmt.Sprintf("I2C%d_M%d_SDA", c.num, c.mux)
	sclName := fmt.Sprintf("I2C%d_M%d_SCL", c.num, c.mux)

	if err := c.deps.Diagram.CheckConflictErr([]string{sdaName, sclName}); err != nil {
		return err
	}

	dts := c.buildDTS("okay")
	if err := c.deps.Dynamic.Apply(c.ID(), dts); err != nil {
		return err
	}

	/*
		if c.deps.Static != nil {
			if err := c.deps.Static.Apply(dts); err != nil {
				logger.Error("Static overlay apply failed", "peripheral", c.ID(), "error", err)
			}
		}
	*/

	c.deps.Diagram.MarkPin(sdaName, true)
	c.deps.Diagram.MarkPin(sclName, true)

	logger.Info("I2C enabled", "num", c.num, "mux", c.mux, "speed", c.speed)

	return c.deps.Cfg.SetMulti(fmt.Sprintf("I2C%d_M%d", c.num, c.mux), map[string]string{
		"STATUS": "1",
		"SPEED":  strconv.Itoa(c.speed),
	})
}

func (c *I2C) Disable(ctx context.Context) error {
	dts := c.buildDTS("disabled")
	if err := c.deps.Dynamic.Apply(c.ID(), dts); err != nil {
		return err
	}

	/*
		if c.deps.Static != nil {
			if err := c.deps.Static.Apply(dts); err != nil {
				logger.Error("Static overlay disable failed", "peripheral", c.ID(), "error", err)
			}
		}
	*/

	if strings.ToLower(c.deps.ConfigMode) == "rmio" {
		if c.sdaPin >= 0 {
			_ = ResetPinMode(c.deps.Chip, c.deps.MapRMIOToGPIO(c.sdaPin))
			c.deps.Diagram.MarkPin(board.RMIOName(c.sdaPin), false)
		}
		if c.sclPin >= 0 {
			_ = ResetPinMode(c.deps.Chip, c.deps.MapRMIOToGPIO(c.sclPin))
			c.deps.Diagram.MarkPin(board.RMIOName(c.sclPin), false)
		}
		c.sdaPin, c.sclPin = -1, -1

		logger.Info("I2C disabled", "num", c.num)
		return c.deps.Cfg.Set(fmt.Sprintf("I2C%d", c.num), "STATUS", "0")
	}

	// Non-RMIO mode
	sdaName := fmt.Sprintf("I2C%d_M%d_SDA", c.num, c.mux)
	sclName := fmt.Sprintf("I2C%d_M%d_SCL", c.num, c.mux)
	c.deps.Diagram.MarkPin(sdaName, false)
	c.deps.Diagram.MarkPin(sclName, false)

	logger.Info("I2C disabled", "num", c.num, "mux", c.mux)
	return c.deps.Cfg.Set(fmt.Sprintf("I2C%d_M%d", c.num, c.mux), "STATUS", "0")
}

func (c *I2C) LoadFromConfig(ctx context.Context, cfg *config.Store, apply bool) error {
	c.deps.Cfg = cfg
	if strings.ToLower(c.deps.ConfigMode) == "rmio" {
		mod := fmt.Sprintf("I2C%d", c.num)
		if cfg.Get(mod, "STATUS") != "1" {
			if !apply && c.IsActive() {
				c.deps.Diagram.MarkPin(board.RMIOName(c.sdaPin), true)
				c.deps.Diagram.MarkPin(board.RMIOName(c.sclPin), true)
			}
			return nil
		}
		sdaStr := cfg.Get(mod, "SDA_RM_IO")
		sclStr := cfg.Get(mod, "SCL_RM_IO")
		if sdaStr == "" || sclStr == "" {
			return nil
		}
		sda, _ := strconv.Atoi(sdaStr)
		scl, _ := strconv.Atoi(sclStr)
		c.sdaPin, c.sclPin = sda, scl

		if sp := cfg.Get(mod, "SPEED"); sp != "" {
			if v, err := strconv.Atoi(sp); err == nil {
				c.speed = v
			}
		}
		if apply {
			return c.Enable(ctx)
		}
		// Just mark in diagram if not applying
		c.deps.Diagram.MarkPin(board.RMIOName(c.sdaPin), true)
		c.deps.Diagram.MarkPin(board.RMIOName(c.sclPin), true)
		return nil
	}

	// Non-RMIO mode
	mod := fmt.Sprintf("I2C%d_M%d", c.num, c.mux)
	if cfg.Get(mod, "STATUS") != "1" {
		if !apply && c.IsActive() {
			sdaName := fmt.Sprintf("I2C%d_M%d_SDA", c.num, c.mux)
			sclName := fmt.Sprintf("I2C%d_M%d_SCL", c.num, c.mux)
			c.deps.Diagram.MarkPin(sdaName, true)
			c.deps.Diagram.MarkPin(sclName, true)
		}
		return nil
	}
	if sp := cfg.Get(mod, "SPEED"); sp != "" {
		if v, err := strconv.Atoi(sp); err == nil {
			c.speed = v
		}
	}
	if apply {
		return c.Enable(ctx)
	}
	sdaName := fmt.Sprintf("I2C%d_M%d_SDA", c.num, c.mux)
	sclName := fmt.Sprintf("I2C%d_M%d_SCL", c.num, c.mux)
	c.deps.Diagram.MarkPin(sdaName, true)
	c.deps.Diagram.MarkPin(sclName, true)
	return nil
}

func (c *I2C) IsActive() bool {
	// Check if the ConfigFS overlay exists for this peripheral
	overlayPath := "/sys/kernel/config/device-tree/overlays/" + c.ID()
	if _, err := os.Stat(overlayPath); err != nil {
		return false
	}

	// Also verify the hardware device exists
	return Exists(fmt.Sprintf("/dev/i2c-%d", c.num))
}

func (c *I2C) IsConfigEnabled(cfg *config.Store) bool {
	mod := fmt.Sprintf("I2C%d", c.num)
	if strings.ToLower(c.deps.ConfigMode) != "rmio" {
		mod = fmt.Sprintf("I2C%d_M%d", c.num, c.mux)
	}
	return cfg.Get(mod, "STATUS") == "1"
}

func (c *I2C) SetPins(sda, scl int) { c.sdaPin, c.sclPin = sda, scl }
func (c *I2C) SetMux(mux int)       { c.mux = mux }
func (c *I2C) SetSpeed(hz int)      { c.speed = hz }

func (c *I2C) buildDTS(status string) string {
	alias := fmt.Sprintf("i2c%d", c.num)
	fullPath, err := c.deps.ResolveAlias(alias)
	if err != nil {
		fullPath = alias // Fallback
	}

	pinctrl := ""
	if strings.ToLower(c.deps.ConfigMode) != "rmio" && status == "okay" {
		name := fmt.Sprintf("i2c%dm%d_xfer", c.num, c.mux)
		if c.deps.PinctrlNaming == "v2" {
			name = fmt.Sprintf("i2c%dm%d_pins", c.num, c.mux)
		}
		pinctrl = fmt.Sprintf("pinctrl-0 = <&%s>;", name)
	}

	return fmt.Sprintf(`/dts-v1/;
/plugin/;

&{%s} {
	%s
	status = "%s";
	clock-frequency = <%d>;
};
`, fullPath, pinctrl, status, c.speed)
}

var _ Peripheral = (*I2C)(nil)
