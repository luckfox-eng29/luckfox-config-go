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
	"luckfox-config/internal/pindiagram"
)

// uartPinModes maps UART number → {TX mode, RX mode} for iomux.
var uartPinModes = map[int][2]int{
	1: {16, 17},
	2: {18, 19},
	3: {20, 21},
	4: {24, 25},
}

// UART manages a UART peripheral via dynamic DTS overlay.
type UART struct {
	num   int
	mux   int // Only used in non-RMIO mode
	deps  Deps
	txPin int // RM_IO index, -1 if not configured (only for RMIO mode)
	rxPin int
}

// NewUART constructs a UART peripheral for the given controller number (1-4).
func NewUART(num int, deps Deps) *UART {
	return &UART{num: num, mux: 0, deps: deps, txPin: -1, rxPin: -1}
}

func (u *UART) ID() string {
	if strings.ToLower(u.deps.ConfigMode) == "rmio" {
		return fmt.Sprintf("uart%d", u.num)
	}
	return fmt.Sprintf("uart%dm%d", u.num, u.mux)
}

func (u *UART) PinsUsed() []int {
	if strings.ToLower(u.deps.ConfigMode) == "rmio" {
		if u.txPin < 0 || u.rxPin < 0 {
			return nil
		}
		return []int{u.txPin, u.rxPin}
	}
	return nil
}

func (u *UART) ConfigKeys() []string {
	if strings.ToLower(u.deps.ConfigMode) == "rmio" {
		p := fmt.Sprintf("UART%d_", u.num)
		return []string{p + "STATUS", p + "TX_RM_IO", p + "RX_RM_IO"}
	}
	p := fmt.Sprintf("UART%d_M%d_", u.num, u.mux)
	return []string{p + "STATUS"}
}

func (u *UART) Enable(ctx context.Context) error {
	if strings.ToLower(u.deps.ConfigMode) == "rmio" {
		if u.txPin < 0 || u.rxPin < 0 {
			return fmt.Errorf("uart%d: TX and RX pins must be set before enabling", u.num)
		}

		txName := board.RMIOName(u.txPin)
		rxName := board.RMIOName(u.rxPin)

		if err := u.deps.Diagram.CheckConflictErr([]string{txName, rxName}); err != nil {
			return err
		}

		dts := u.buildDTS("okay")
		if err := u.deps.Dynamic.Apply(u.ID(), dts); err != nil {
			return err
		}

		/*
			if u.deps.Static != nil {
				if err := u.deps.Static.Apply(dts); err != nil {
					logger.Error("Static overlay apply failed", "peripheral", u.ID(), "error", err)
				}
			}
		*/

		modes := uartPinModes[u.num]
		if err := SetPinMode(u.deps.Chip, u.deps.MapRMIOToGPIO(u.txPin), modes[0]); err != nil {
			return err
		}
		if err := SetPinMode(u.deps.Chip, u.deps.MapRMIOToGPIO(u.rxPin), modes[1]); err != nil {
			return err
		}

		u.deps.Diagram.MarkPin(txName, true)
		u.deps.Diagram.MarkPin(rxName, true)

		logger.Info("UART enabled", "num", u.num, "tx", txName, "rx", rxName)

		return u.deps.Cfg.SetMulti(fmt.Sprintf("UART%d", u.num), map[string]string{
			"STATUS":   "1",
			"TX_RM_IO": strconv.Itoa(u.txPin),
			"RX_RM_IO": strconv.Itoa(u.rxPin),
		})
	}

	// Non-RMIO mode
	txName := fmt.Sprintf("UART%d_M%d_TX", u.num, u.mux)
	rxName := fmt.Sprintf("UART%d_M%d_RX", u.num, u.mux)

	if err := u.deps.Diagram.CheckConflictErr([]string{txName, rxName}); err != nil {
		return err
	}

	dts := u.buildDTS("okay")
	if err := u.deps.Dynamic.Apply(u.ID(), dts); err != nil {
		return err
	}

	/*
		if u.deps.Static != nil {
			if err := u.deps.Static.Apply(dts); err != nil {
				logger.Error("Static overlay apply failed", "peripheral", u.ID(), "error", err)
				// Don't return error; dynamic overlay worked.
			}
		}
	*/

	u.deps.Diagram.MarkPin(txName, true)
	u.deps.Diagram.MarkPin(rxName, true)

	logger.Info("UART enabled", "num", u.num, "mux", u.mux)

	return u.deps.Cfg.Set(fmt.Sprintf("UART%d_M%d", u.num, u.mux), "STATUS", "1")
}

func (u *UART) Disable(ctx context.Context) error {
	dts := u.buildDTS("disabled")
	if err := u.deps.Dynamic.Apply(u.ID(), dts); err != nil {
		return err
	}

	/*
		if u.deps.Static != nil {
			if err := u.deps.Static.Apply(dts); err != nil {
				logger.Error("Static overlay disable failed", "peripheral", u.ID(), "error", err)
			}
		}
	*/

	if strings.ToLower(u.deps.ConfigMode) == "rmio" {
		if u.txPin >= 0 {
			_ = ResetPinMode(u.deps.Chip, u.deps.MapRMIOToGPIO(u.txPin))
			u.deps.Diagram.MarkPin(board.RMIOName(u.txPin), false)
		}
		if u.rxPin >= 0 {
			_ = ResetPinMode(u.deps.Chip, u.deps.MapRMIOToGPIO(u.rxPin))
			u.deps.Diagram.MarkPin(board.RMIOName(u.rxPin), false)
		}
		u.txPin, u.rxPin = -1, -1

		logger.Info("UART disabled", "num", u.num)
		return u.deps.Cfg.Set(fmt.Sprintf("UART%d", u.num), "STATUS", "0")
	}

	// Non-RMIO mode
	txName := fmt.Sprintf("UART%d_M%d_TX", u.num, u.mux)
	rxName := fmt.Sprintf("UART%d_M%d_RX", u.num, u.mux)
	u.deps.Diagram.MarkPin(txName, false)
	u.deps.Diagram.MarkPin(rxName, false)

	logger.Info("UART disabled", "num", u.num, "mux", u.mux)
	return u.deps.Cfg.Set(fmt.Sprintf("UART%d_M%d", u.num, u.mux), "STATUS", "0")
}

func (u *UART) LoadFromConfig(ctx context.Context, cfg *config.Store, apply bool) error {
	u.deps.Cfg = cfg
	if strings.ToLower(u.deps.ConfigMode) == "rmio" {
		mod := fmt.Sprintf("UART%d", u.num)
		status := cfg.Get(mod, "STATUS")
		if status != "1" {
			if !apply && u.IsActive() {
				u.deps.Diagram.MarkPin(board.RMIOName(u.txPin), true)
				u.deps.Diagram.MarkPin(board.RMIOName(u.rxPin), true)
			}
			return nil
		}
		txStr := cfg.Get(mod, "TX_RM_IO")
		rxStr := cfg.Get(mod, "RX_RM_IO")
		if txStr == "" || rxStr == "" {
			return nil
		}
		tx, err := strconv.Atoi(txStr)
		if err != nil {
			return err
		}
		rx, err := strconv.Atoi(rxStr)
		if err != nil {
			return err
		}
		u.txPin, u.rxPin = tx, rx
		if apply {
			return u.Enable(ctx)
		}
		// Just mark in diagram if not applying
		u.deps.Diagram.MarkPin(board.RMIOName(u.txPin), true)
		u.deps.Diagram.MarkPin(board.RMIOName(u.rxPin), true)
		return nil
	}

	// Non-RMIO mode
	mod := fmt.Sprintf("UART%d_M%d", u.num, u.mux)
	status := cfg.Get(mod, "STATUS")
	if status != "1" {
		if !apply && u.IsActive() {
			txName := fmt.Sprintf("UART%d_M%d_TX", u.num, u.mux)
			rxName := fmt.Sprintf("UART%d_M%d_RX", u.num, u.mux)
			u.deps.Diagram.MarkPin(txName, true)
			u.deps.Diagram.MarkPin(rxName, true)
		}
		return nil
	}
	if apply {
		return u.Enable(ctx)
	}
	txName := fmt.Sprintf("UART%d_M%d_TX", u.num, u.mux)
	rxName := fmt.Sprintf("UART%d_M%d_RX", u.num, u.mux)
	u.deps.Diagram.MarkPin(txName, true)
	u.deps.Diagram.MarkPin(rxName, true)
	return nil
}

func (u *UART) IsActive() bool {
	// Check if the ConfigFS overlay exists for this peripheral
	overlayPath := "/sys/kernel/config/device-tree/overlays/" + u.ID()
	if _, err := os.Stat(overlayPath); err != nil {
		return false
	}

	// Also verify the hardware device exists
	return Exists(fmt.Sprintf("/dev/ttyS%d", u.num))
}

// SetPins configures the TX and RX RM_IO pins before calling Enable.
func (u *UART) IsConfigEnabled(cfg *config.Store) bool {
	mod := fmt.Sprintf("UART%d", u.num)
	if strings.ToLower(u.deps.ConfigMode) != "rmio" {
		mod = fmt.Sprintf("UART%d_M%d", u.num, u.mux)
	}
	return cfg.Get(mod, "STATUS") == "1"
}

func (u *UART) SetPins(tx, rx int) {
	u.txPin, u.rxPin = tx, rx
}

// SetMux configures the mux mode (Mx) before calling Enable.
func (u *UART) SetMux(mux int) {
	u.mux = mux
}

func (u *UART) buildDTS(status string) string {
	alias := fmt.Sprintf("serial%d", u.num)
	fullPath, err := u.deps.ResolveAlias(alias)
	if err != nil {
		fullPath = alias // Fallback
	}

	if strings.ToLower(u.deps.ConfigMode) == "rmio" {
		return fmt.Sprintf(`/dts-v1/;
/plugin/;

&{%s} {
	status = "%s";
};
`, fullPath, status)
	}

	// Non-RMIO mode needs pinctrl
	pinctrl := ""
	if status == "okay" {
		name := fmt.Sprintf("uart%dm%d_xfer", u.num, u.mux)
		if u.deps.PinctrlNaming == "v2" {
			name += "_pins"
		}
		pinctrl = fmt.Sprintf("pinctrl-0 = <&%s>;", name)
	}

	return fmt.Sprintf(`/dts-v1/;
/plugin/;

&{%s} {
	%s
	status = "%s";
};
`, fullPath, pinctrl, status)
}

// UARTDeps is satisfied by Deps — kept for type alias clarity in tests.
type UARTDeps = Deps

// ensure UART implements Peripheral at compile time
var _ Peripheral = (*UART)(nil)

// PinDiagramHelper to simplify diagram access in tests.
func newDiagramForTest(rows []string) *pindiagram.PinDiagram {
	return pindiagram.New(rows)
}
