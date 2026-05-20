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

const defaultCANSpeed = 300000000

// canPinModes maps CAN number → {TX mode, RX mode}.
var canPinModes = map[int][2]int{
	0: {43, 44},
	1: {41, 42},
}

// CAN manages a CAN bus peripheral via dynamic DTS overlay.
type CAN struct {
	num   int
	mux   int // Only used in non-RMIO mode
	deps  Deps
	txPin int // RM_IO index, only for RMIO mode
	rxPin int
	speed int // assigned-clock-rates
}

func NewCAN(num int, deps Deps) *CAN {
	return &CAN{num: num, mux: 0, deps: deps, txPin: -1, rxPin: -1, speed: defaultCANSpeed}
}

func (c *CAN) ID() string {
	if strings.ToLower(c.deps.ConfigMode) == "rmio" {
		return fmt.Sprintf("can%d", c.num)
	}
	return fmt.Sprintf("can%dm%d", c.num, c.mux)
}

func (c *CAN) PinsUsed() []int {
	if strings.ToLower(c.deps.ConfigMode) == "rmio" {
		if c.txPin < 0 || c.rxPin < 0 {
			return nil
		}
		return []int{c.txPin, c.rxPin}
	}
	return nil
}

func (c *CAN) ConfigKeys() []string {
	if strings.ToLower(c.deps.ConfigMode) == "rmio" {
		p := fmt.Sprintf("CAN%d_", c.num)
		return []string{p + "STATUS", p + "TX_RM_IO", p + "RX_RM_IO", p + "SPEED"}
	}
	p := fmt.Sprintf("CAN%d_M%d_", c.num, c.mux)
	return []string{p + "STATUS", p + "SPEED"}
}

func (c *CAN) Enable(ctx context.Context) error {
	if strings.ToLower(c.deps.ConfigMode) == "rmio" {
		if c.txPin < 0 || c.rxPin < 0 {
			return fmt.Errorf("can%d: TX and RX pins must be set before enabling", c.num)
		}

		txName := board.RMIOName(c.txPin)
		rxName := board.RMIOName(c.rxPin)

		if err := c.deps.Diagram.CheckConflictErr([]string{txName, rxName}); err != nil {
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

		modes := canPinModes[c.num]
		_ = SetPinMode(c.deps.Chip, c.deps.MapRMIOToGPIO(c.txPin), modes[0])
		_ = SetPinMode(c.deps.Chip, c.deps.MapRMIOToGPIO(c.rxPin), modes[1])
		_ = SetPull(c.deps.Chip, c.deps.MapRMIOToGPIO(c.txPin), PullUp)
		_ = SetPull(c.deps.Chip, c.deps.MapRMIOToGPIO(c.rxPin), PullUp)

		c.deps.Diagram.MarkPin(txName, true)
		c.deps.Diagram.MarkPin(rxName, true)

		logger.Info("CAN enabled", "num", c.num, "tx", txName, "rx", rxName, "speed", c.speed)

		return c.deps.Cfg.SetMulti(fmt.Sprintf("CAN%d", c.num), map[string]string{
			"STATUS":   "1",
			"TX_RM_IO": strconv.Itoa(c.txPin),
			"RX_RM_IO": strconv.Itoa(c.rxPin),
			"SPEED":    strconv.Itoa(c.speed),
		})
	}

	// Non-RMIO mode
	txName := fmt.Sprintf("CAN%d_M%d_TX", c.num, c.mux)
	rxName := fmt.Sprintf("CAN%d_M%d_RX", c.num, c.mux)

	if err := c.deps.Diagram.CheckConflictErr([]string{txName, rxName}); err != nil {
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

	c.deps.Diagram.MarkPin(txName, true)
	c.deps.Diagram.MarkPin(rxName, true)

	logger.Info("CAN enabled", "num", c.num, "mux", c.mux, "speed", c.speed)

	return c.deps.Cfg.SetMulti(fmt.Sprintf("CAN%d_M%d", c.num, c.mux), map[string]string{
		"STATUS": "1",
		"SPEED":  strconv.Itoa(c.speed),
	})
}

func (c *CAN) Disable(ctx context.Context) error {
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
		if c.txPin >= 0 {
			gpio := c.deps.MapRMIOToGPIO(c.txPin)
			_ = ResetPinMode(c.deps.Chip, gpio)
			_ = SetPull(c.deps.Chip, gpio, PullNone)
			c.deps.Diagram.MarkPin(board.RMIOName(c.txPin), false)
		}
		if c.rxPin >= 0 {
			gpio := c.deps.MapRMIOToGPIO(c.rxPin)
			_ = ResetPinMode(c.deps.Chip, gpio)
			_ = SetPull(c.deps.Chip, gpio, PullNone)
			c.deps.Diagram.MarkPin(board.RMIOName(c.rxPin), false)
		}
		c.txPin, c.rxPin = -1, -1

		logger.Info("CAN disabled", "num", c.num)
		return c.deps.Cfg.Set(fmt.Sprintf("CAN%d", c.num), "STATUS", "0")
	}

	// Non-RMIO mode
	txName := fmt.Sprintf("CAN%d_M%d_TX", c.num, c.mux)
	rxName := fmt.Sprintf("CAN%d_M%d_RX", c.num, c.mux)
	c.deps.Diagram.MarkPin(txName, false)
	c.deps.Diagram.MarkPin(rxName, false)

	logger.Info("CAN disabled", "num", c.num, "mux", c.mux)
	return c.deps.Cfg.Set(fmt.Sprintf("CAN%d_M%d", c.num, c.mux), "STATUS", "0")
}

func (c *CAN) LoadFromConfig(ctx context.Context, cfg *config.Store, apply bool) error {
	c.deps.Cfg = cfg
	if strings.ToLower(c.deps.ConfigMode) == "rmio" {
		mod := fmt.Sprintf("CAN%d", c.num)
		if cfg.Get(mod, "STATUS") != "1" {
			if !apply && c.IsActive() {
				c.deps.Diagram.MarkPin(board.RMIOName(c.txPin), true)
				c.deps.Diagram.MarkPin(board.RMIOName(c.rxPin), true)
			}
			return nil
		}
		txStr := cfg.Get(mod, "TX_RM_IO")
		rxStr := cfg.Get(mod, "RX_RM_IO")
		if txStr == "" || rxStr == "" {
			return nil
		}
		tx, _ := strconv.Atoi(txStr)
		rx, _ := strconv.Atoi(rxStr)
		c.txPin, c.rxPin = tx, rx
		if sp := cfg.Get(mod, "SPEED"); sp != "" {
			c.speed, _ = strconv.Atoi(sp)
		}
		if apply {
			return c.Enable(ctx)
		}
		// Just mark in diagram if not applying
		c.deps.Diagram.MarkPin(board.RMIOName(c.txPin), true)
		c.deps.Diagram.MarkPin(board.RMIOName(c.rxPin), true)
		return nil
	}

	// Non-RMIO mode
	mod := fmt.Sprintf("CAN%d_M%d", c.num, c.mux)
	if cfg.Get(mod, "STATUS") != "1" {
		if !apply && c.IsActive() {
			txName := fmt.Sprintf("CAN%d_M%d_TX", c.num, c.mux)
			rxName := fmt.Sprintf("CAN%d_M%d_RX", c.num, c.mux)
			c.deps.Diagram.MarkPin(txName, true)
			c.deps.Diagram.MarkPin(rxName, true)
		}
		return nil
	}
	if sp := cfg.Get(mod, "SPEED"); sp != "" {
		c.speed, _ = strconv.Atoi(sp)
	}
	if apply {
		return c.Enable(ctx)
	}
	txName := fmt.Sprintf("CAN%d_M%d_TX", c.num, c.mux)
	rxName := fmt.Sprintf("CAN%d_M%d_RX", c.num, c.mux)
	c.deps.Diagram.MarkPin(txName, true)
	c.deps.Diagram.MarkPin(rxName, true)
	return nil
}

func (c *CAN) IsActive() bool {
	// Check if the ConfigFS overlay exists for this peripheral
	overlayPath := "/sys/kernel/config/device-tree/overlays/" + c.ID()
	if _, err := os.Stat(overlayPath); err != nil {
		return false
	}

	// Also verify the hardware device exists
	return Exists(fmt.Sprintf("/sys/class/net/can%d", c.num))
}

func (c *CAN) IsConfigEnabled(cfg *config.Store) bool {
	mod := fmt.Sprintf("CAN%d", c.num)
	if strings.ToLower(c.deps.ConfigMode) != "rmio" {
		mod = fmt.Sprintf("CAN%d_M%d", c.num, c.mux)
	}
	return cfg.Get(mod, "STATUS") == "1"
}

func (c *CAN) SetPins(tx, rx int) { c.txPin, c.rxPin = tx, rx }
func (c *CAN) SetMux(mux int)     { c.mux = mux }
func (c *CAN) SetSpeed(hz int)    { c.speed = hz }

func (c *CAN) buildDTS(status string) string {
	alias := fmt.Sprintf("can%d", c.num)
	fullPath, err := c.deps.ResolveAlias(alias)
	if err != nil {
		fullPath = alias // Fallback
	}

	pinctrl := ""
	if strings.ToLower(c.deps.ConfigMode) != "rmio" && status == "okay" {
		name := fmt.Sprintf("can%dm%d_xfer", c.num, c.mux)
		if c.deps.PinctrlNaming == "v2" {
			name = fmt.Sprintf("can%dm%d_pins", c.num, c.mux)
		}
		pinctrl = fmt.Sprintf("pinctrl-0 = <&%s>;", name)
	}

	return fmt.Sprintf(`/dts-v1/;
/plugin/;

&{%s} {
	%s
	status = "%s";
	assigned-clock-rates = <%d>;
};
`, fullPath, pinctrl, status, c.speed)
}

var _ Peripheral = (*CAN)(nil)
