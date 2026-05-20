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

const defaultSPISpeed = 10000000

// spiPinModes maps SPI number → {SCLK, MOSI, MISO, CS} iomux modes.
var spiPinModes = map[int][4]int{
	0: {82, 83, 84, 85},
	1: {87, 88, 89, 90},
}

// SPI manages a SPI peripheral via dynamic DTS overlay.
type SPI struct {
	num     int
	mux     int // Only used in non-RMIO mode
	deps    Deps
	sclkPin int // RM_IO index, only for RMIO mode
	mosiPin int
	misoPin int // -1 = disabled ("*")
	csPin   int // -1 = disabled ("*")
	speed   int
}

func NewSPI(num int, deps Deps) *SPI {
	return &SPI{num: num, mux: 0, deps: deps, sclkPin: -1, mosiPin: -1, misoPin: -1, csPin: -1, speed: defaultSPISpeed}
}

func (s *SPI) ID() string {
	if strings.ToLower(s.deps.ConfigMode) == "rmio" {
		return fmt.Sprintf("spi%d", s.num)
	}
	return fmt.Sprintf("spi%dm%d", s.num, s.mux)
}

func (s *SPI) PinsUsed() []int {
	if strings.ToLower(s.deps.ConfigMode) == "rmio" {
		if s.sclkPin < 0 || s.mosiPin < 0 {
			return nil
		}
		pins := []int{s.sclkPin, s.mosiPin}
		if s.misoPin >= 0 {
			pins = append(pins, s.misoPin)
		}
		if s.csPin >= 0 {
			pins = append(pins, s.csPin)
		}
		return pins
	}
	return nil
}

func (s *SPI) ConfigKeys() []string {
	if strings.ToLower(s.deps.ConfigMode) == "rmio" {
		p := fmt.Sprintf("SPI%d_", s.num)
		return []string{p + "STATUS", p + "SCLK_RM_IO", p + "MOSI_RM_IO", p + "MISO_RM_IO", p + "CS_RM_IO", p + "SPEED"}
	}
	p := fmt.Sprintf("SPI%d_M%d_", s.num, s.mux)
	return []string{p + "STATUS", p + "SPEED"}
}

func (s *SPI) Enable(ctx context.Context) error {
	if strings.ToLower(s.deps.ConfigMode) == "rmio" {
		if s.sclkPin < 0 || s.mosiPin < 0 {
			return fmt.Errorf("spi%d: SCLK and MOSI pins must be set before enabling", s.num)
		}

		candidates := []string{board.RMIOName(s.sclkPin), board.RMIOName(s.mosiPin)}
		if s.misoPin >= 0 {
			candidates = append(candidates, board.RMIOName(s.misoPin))
		}
		if s.csPin >= 0 {
			candidates = append(candidates, board.RMIOName(s.csPin))
		}

		if err := s.deps.Diagram.CheckConflictErr(candidates); err != nil {
			return err
		}

		dts := s.buildDTS("okay")
		if err := s.deps.Dynamic.Apply(s.ID(), dts); err != nil {
			return err
		}

		/*
			if s.deps.Static != nil {
				if err := s.deps.Static.Apply(dts); err != nil {
					logger.Error("Static overlay apply failed", "peripheral", s.ID(), "error", err)
				}
			}
		*/

		modes := spiPinModes[s.num]
		_ = SetPinMode(s.deps.Chip, s.deps.MapRMIOToGPIO(s.sclkPin), modes[0])
		_ = SetPinMode(s.deps.Chip, s.deps.MapRMIOToGPIO(s.mosiPin), modes[1])
		if s.misoPin >= 0 {
			_ = SetPinMode(s.deps.Chip, s.deps.MapRMIOToGPIO(s.misoPin), modes[2])
		}
		if s.csPin >= 0 {
			_ = SetPinMode(s.deps.Chip, s.deps.MapRMIOToGPIO(s.csPin), modes[3])
		}

		// SPI needs drive strength level 3
		for _, pin := range s.PinsUsed() {
			_ = SetDriveStrength(s.deps.Chip, s.deps.MapRMIOToGPIO(pin), 3)
		}

		for _, name := range candidates {
			s.deps.Diagram.MarkPin(name, true)
		}

		logger.Info("SPI enabled", "num", s.num, "pins", candidates, "speed", s.speed)

		cfgMap := map[string]string{
			"STATUS":     "1",
			"SCLK_RM_IO": strconv.Itoa(s.sclkPin),
			"MOSI_RM_IO": strconv.Itoa(s.mosiPin),
			"SPEED":      strconv.Itoa(s.speed),
		}
		if s.misoPin >= 0 {
			cfgMap["MISO_RM_IO"] = strconv.Itoa(s.misoPin)
		}
		if s.csPin >= 0 {
			cfgMap["CS_RM_IO"] = strconv.Itoa(s.csPin)
		}
		return s.deps.Cfg.SetMulti(fmt.Sprintf("SPI%d", s.num), cfgMap)
	}

	// Non-RMIO mode
	pins := []string{
		fmt.Sprintf("SPI%d_M%d_CLK", s.num, s.mux),
		fmt.Sprintf("SPI%d_M%d_MOSI", s.num, s.mux),
		fmt.Sprintf("SPI%d_M%d_MISO", s.num, s.mux),
		fmt.Sprintf("SPI%d_M%d_CS0", s.num, s.mux),
	}

	if err := s.deps.Diagram.CheckConflictErr(pins); err != nil {
		return err
	}

	dts := s.buildDTS("okay")
	if err := s.deps.Dynamic.Apply(s.ID(), dts); err != nil {
		return err
	}

	/*
		if s.deps.Static != nil {
			if err := s.deps.Static.Apply(dts); err != nil {
				logger.Error("Static overlay apply failed", "peripheral", s.ID(), "error", err)
			}
		}
	*/

	s.markNonRMIOPins(true)

	logger.Info("SPI enabled", "num", s.num, "mux", s.mux, "speed", s.speed)

	return s.deps.Cfg.SetMulti(fmt.Sprintf("SPI%d_M%d", s.num, s.mux), map[string]string{
		"STATUS": "1",
		"SPEED":  strconv.Itoa(s.speed),
	})
}

func (s *SPI) Disable(ctx context.Context) error {
	dts := s.buildDTS("disabled")
	if err := s.deps.Dynamic.Apply(s.ID(), dts); err != nil {
		return err
	}

	/*
		if s.deps.Static != nil {
			if err := s.deps.Static.Apply(dts); err != nil {
				logger.Error("Static overlay disable failed", "peripheral", s.ID(), "error", err)
			}
		}
	*/

	if strings.ToLower(s.deps.ConfigMode) == "rmio" {
		for _, pin := range s.PinsUsed() {
			gpio := s.deps.MapRMIOToGPIO(pin)
			_ = ResetPinMode(s.deps.Chip, gpio)
			_ = SetDriveStrength(s.deps.Chip, gpio, 2)
			s.deps.Diagram.MarkPin(board.RMIOName(pin), false)
		}
		s.sclkPin, s.mosiPin, s.misoPin, s.csPin = -1, -1, -1, -1

		logger.Info("SPI disabled", "num", s.num)
		return s.deps.Cfg.Set(fmt.Sprintf("SPI%d", s.num), "STATUS", "0")
	}

	// Non-RMIO mode
	s.markNonRMIOPins(false)

	logger.Info("SPI disabled", "num", s.num, "mux", s.mux)
	return s.deps.Cfg.Set(fmt.Sprintf("SPI%d_M%d", s.num, s.mux), "STATUS", "0")
}

func (s *SPI) LoadFromConfig(ctx context.Context, cfg *config.Store, apply bool) error {
	s.deps.Cfg = cfg
	if strings.ToLower(s.deps.ConfigMode) == "rmio" {
		mod := fmt.Sprintf("SPI%d", s.num)
		if cfg.Get(mod, "STATUS") != "1" {
			// If not in config, check hardware status to mark diagram
			if !apply && s.IsActive() {
				s.deps.Diagram.MarkPin(board.RMIOName(s.sclkPin), true)
				s.deps.Diagram.MarkPin(board.RMIOName(s.mosiPin), true)
				if s.misoPin >= 0 {
					s.deps.Diagram.MarkPin(board.RMIOName(s.misoPin), true)
				}
				if s.csPin >= 0 {
					s.deps.Diagram.MarkPin(board.RMIOName(s.csPin), true)
				}
			}
			return nil
		}
		atoi := func(key string) int {
			v, _ := strconv.Atoi(cfg.Get(mod, key))
			return v
		}
		sclk := cfg.Get(mod, "SCLK_RM_IO")
		mosi := cfg.Get(mod, "MOSI_RM_IO")
		if sclk == "" || mosi == "" {
			return nil
		}
		s.sclkPin = atoi("SCLK_RM_IO")
		s.mosiPin = atoi("MOSI_RM_IO")
		if v := cfg.Get(mod, "MISO_RM_IO"); v != "" {
			s.misoPin, _ = strconv.Atoi(v)
		}
		if v := cfg.Get(mod, "CS_RM_IO"); v != "" {
			s.csPin, _ = strconv.Atoi(v)
		}
		if v := cfg.Get(mod, "SPEED"); v != "" {
			s.speed, _ = strconv.Atoi(v)
		}
		if apply {
			return s.Enable(ctx)
		}
		// Just mark in diagram if not applying
		s.deps.Diagram.MarkPin(board.RMIOName(s.sclkPin), true)
		s.deps.Diagram.MarkPin(board.RMIOName(s.mosiPin), true)
		if s.misoPin >= 0 {
			s.deps.Diagram.MarkPin(board.RMIOName(s.misoPin), true)
		}
		if s.csPin >= 0 {
			s.deps.Diagram.MarkPin(board.RMIOName(s.csPin), true)
		}
		return nil
	}

	// Non-RMIO mode
	mod := fmt.Sprintf("SPI%d_M%d", s.num, s.mux)
	if cfg.Get(mod, "STATUS") != "1" {
		if !apply && s.IsActive() {
			s.markNonRMIOPins(true)
		}
		return nil
	}
	if v := cfg.Get(mod, "SPEED"); v != "" {
		s.speed, _ = strconv.Atoi(v)
	}
	if apply {
		return s.Enable(ctx)
	}
	s.markNonRMIOPins(true)
	return nil
}

func (s *SPI) markNonRMIOPins(inUse bool) {
	pins := []string{
		fmt.Sprintf("SPI%d_M%d_CLK", s.num, s.mux),
		fmt.Sprintf("SPI%d_M%d_MOSI", s.num, s.mux),
		fmt.Sprintf("SPI%d_M%d_MISO", s.num, s.mux),
		fmt.Sprintf("SPI%d_M%d_CS0", s.num, s.mux),
	}
	for _, name := range pins {
		s.deps.Diagram.MarkPin(name, inUse)
	}
}

func (s *SPI) IsActive() bool {
	// Check if the ConfigFS overlay exists for this peripheral
	overlayPath := "/sys/kernel/config/device-tree/overlays/" + s.ID()
	if _, err := os.Stat(overlayPath); err != nil {
		return false
	}

	// Also verify the hardware device exists
	found := false
	if entries, err := os.ReadDir("/dev"); err == nil {
		prefix := fmt.Sprintf("spidev%d.", s.num)
		for _, e := range entries {
			if strings.HasPrefix(e.Name(), prefix) {
				found = true
				break
			}
		}
	}
	return found
}

func (s *SPI) IsConfigEnabled(cfg *config.Store) bool {
	mod := fmt.Sprintf("SPI%d", s.num)
	if strings.ToLower(s.deps.ConfigMode) != "rmio" {
		mod = fmt.Sprintf("SPI%d_M%d", s.num, s.mux)
	}
	return cfg.Get(mod, "STATUS") == "1"
}

func (s *SPI) SetPins(sclk, mosi, miso, cs int) {
	s.sclkPin, s.mosiPin, s.misoPin, s.csPin = sclk, mosi, miso, cs
}
func (s *SPI) SetMux(mux int)  { s.mux = mux }
func (s *SPI) SetSpeed(hz int) { s.speed = hz }

func (s *SPI) buildDTS(status string) string {
	alias := fmt.Sprintf("spi%d", s.num)
	fullPath, err := s.deps.ResolveAlias(alias)
	if err != nil {
		fullPath = alias // Fallback
	}

	pinctrl := ""
	if strings.ToLower(s.deps.ConfigMode) != "rmio" && status == "okay" {
		clkName := fmt.Sprintf("spi%dm%d_pins", s.num, s.mux)
		csName := fmt.Sprintf("spi%dm%d_cs0", s.num, s.mux)
		if s.deps.PinctrlNaming == "v2" {
			clkName = fmt.Sprintf("spi%dm%d_clk_pins", s.num, s.mux)
			csName = fmt.Sprintf("spi%dm%d_csn0_pins", s.num, s.mux)
		}
		pinctrl = fmt.Sprintf("pinctrl-0 = <&%s &%s>;", clkName, csName)
	}

	return fmt.Sprintf(`/dts-v1/;
/plugin/;

&{%s} {
	%s
	status = "%s";
	#address-cells = <1>;
	#size-cells = <0>;
	spidev@%d {
		compatible = "rockchip,spidev";
		spi-max-frequency = <%d>;
		reg = <%d>;
	};
};
`, fullPath, pinctrl, status, s.num, s.speed, s.num)
}

var _ Peripheral = (*SPI)(nil)
