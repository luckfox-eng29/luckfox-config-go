/*
 * SPDX-FileCopyrightText: 2026 Luckfox Team
 *
 * SPDX-License-Identifier: Apache-2.0
 */

package board

import (
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"strings"

	"luckfox-config/internal/chip"
)

// StringList is a custom type that can be unmarshaled from either a single string or a slice of strings.
type StringList []string

func (s *StringList) UnmarshalJSON(data []byte) error {
	var single string
	if err := json.Unmarshal(data, &single); err == nil {
		*s = []string{single}
		return nil
	}

	var slice []string
	if err := json.Unmarshal(data, &slice); err == nil {
		*s = slice
		return nil
	}

	return fmt.Errorf("invalid StringList: %s", string(data))
}

// BoardConfig represents a single board variant loaded from JSON.
type BoardConfig struct {
	ID            string                `json:"id"`
	Chip          string                `json:"chip"`
	ConfigMode    string                `json:"config_mode"`
	PinctrlNaming string                `json:"pinctrl_naming"` // "v1" (RV1106) or "v2" (RV1126B)
	SplitSPI      bool                  `json:"split_spi"`      // RV1106 (true), RV1126B (false)
	ModelStrings  []string              `json:"model_strings"`
	BootMedia     map[string]StringList `json:"boot_media"`
	Features      BoardFeatures         `json:"features"`
	ReservedPins  []ReservedPin         `json:"reserved_pins"`
	PinDiagram    []string              `json:"pin_diagram"`
	Peripherals   PeripheralSet         `json:"peripherals"`

	ChipInstance chip.Chip `json:"-"`
}

type BoardFeatures struct {
	Has4GModule bool        `json:"has_4g_module"`
	DSIFDTNode  string      `json:"dsi_fdt_node"`
	RGBPins     []string    `json:"rgb_pins"`
	HasFBTFT    bool        `json:"has_fbtft"`
	HasUSB      bool        `json:"has_usb"`
	HasCSI      bool        `json:"has_csi"`
	HasRGB      bool        `json:"has_rgb"`
	HasSDMMC    bool        `json:"has_sdmmc"`
	Touch       TouchConfig `json:"touch"`
}

// TouchConfig describes the touch controller of a board.
//
// Node is the touch controller node. When a DSI panel carrying touch geometry
// is selected, its size/inversion properties are written onto Node.
//
// Bus, Pinctrl and Addresses drive the standalone touchscreen switch used by
// boards whose controller is not tied to a DSI panel (RV1106 Pico Ultra): Bus
// is the I2C controller node, Pinctrl lists the pinctrl node names that have
// to be referenced by phandle, and Addresses the selectable I2C addresses.
type TouchConfig struct {
	Node      string   `json:"node"`
	Bus       string   `json:"bus"`
	BusNumber int      `json:"bus_number"`
	Pinctrl   []string `json:"pinctrl"`
	Addresses []string `json:"addresses"`
}

type ReservedPin struct {
	GPIO   string `json:"gpio"`
	Reason string `json:"reason"`
}

type FBTFTPins struct {
	GPIO []string `json:"gpio"`
	SPI  []string `json:"spi"`
}

type PeripheralSet struct {
	UART           []string            `json:"uart"`
	I2C            []string            `json:"i2c"`
	SPI            []string            `json:"spi"`
	CAN            []string            `json:"can"`
	PWM            []string            `json:"pwm"`
	CompatibleApps []CompatibleAppSpec `json:"compatible_apps"`
	FBTFT          FBTFTPins           `json:"fbtft"`
}

// CompatibleAppSpec describes a preset hardware combination (e.g. DHT11,
// Pico_LCD) on a specific board.
//
//   - Check lists peripheral IDs whose physical pins must be free (occupancy
//     check only; no hardware action is performed at enable time).
//   - Enable lists real peripherals to turn on through the regular registry
//     path (conflict check + pin marking + config persistence).
//   - GPIO applies pin pulls and Nodes toggles device-tree node status.
//   - SPI narrows the SPI0 signal set on boards with split_spi; when omitted the
//     interactive default (CS0 and MISO both claimed) is kept.
type CompatibleAppSpec struct {
	Name   string       `json:"name"`
	Enable []string     `json:"enable,omitempty"`
	Check  []string     `json:"check,omitempty"`
	GPIO   []GPIOAction `json:"gpio,omitempty"`
	Nodes  []NodeAction `json:"nodes,omitempty"`
	SPI    *SPISignals  `json:"spi,omitempty"`
}

// SPISignals selects which of the splittable SPI0 signals a compatible app
// claims. It mirrors the cs/miso arguments of the shell's `luckfox_spi_app`, so
// an app that has no use for MISO does not claim that pin.
type SPISignals struct {
	CS   bool `json:"cs"`
	MISO bool `json:"miso"`
}

// GPIOAction is a single pin-level operation performed by a compatible app.
//
// Mode is the iomux function the pin has to be switched to. It is optional on
// purpose: when the JSON omits it the pin's multiplexing is left alone, which is
// what a pin that already serves as GPIO needs. Boards whose base device tree
// routes the pin to another function (SDMMC, UART, ...) declare Mode 0 to bring
// it back to GPIO.
type GPIOAction struct {
	Pin  string `json:"pin"`            // e.g. "GPIO1_C6"
	Pull string `json:"pull,omitempty"` // "up", "down" or "none"
	Mode *int   `json:"mode,omitempty"` // iomux function; 0 is GPIO, nil leaves it untouched
}

// NodeAction toggles the status of an arbitrary device-tree node.
type NodeAction struct {
	Path   string `json:"path"`   // e.g. "/dht11_sensor"
	Status string `json:"status"` // "okay" or "disabled"
}

// PWMConfig is no longer used but kept for a moment if needed for migration logic.
type PWMConfig struct {
	PWM0Channels []int `json:"pwm0_channels"`
	PWM1Channels []int `json:"pwm1_channels"`
	PWM2Channels []int `json:"pwm2_channels"`
	PWM3Channels []int `json:"pwm3_channels"`
}

// BootDevice returns the boot partition device path based on what's detected.
func (b *BoardConfig) BootDevice() (string, string, error) {
	// Check SPI NAND first
	if path, ok := b.BootMedia["nand"]; ok && len(path) > 0 {
		return path[0], "spi_nand", nil
	}
	if path, ok := b.BootMedia["mmc"]; ok && len(path) > 0 {
		return path[0], "mmc", nil
	}
	return "", "", fmt.Errorf("no boot media configured for board %s", b.ID)
}

// LoadAll loads all board configs from the embedded FS, returns them indexed by model string.
func LoadAll(embedFS embed.FS, dir string) ([]*BoardConfig, error) {
	entries, err := fs.ReadDir(embedFS, dir)
	if err != nil {
		return nil, fmt.Errorf("reading board configs dir: %w", err)
	}

	var boards []*BoardConfig
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		data, err := embedFS.ReadFile(dir + "/" + entry.Name())
		if err != nil {
			return nil, fmt.Errorf("reading %s: %w", entry.Name(), err)
		}
		var bc BoardConfig
		if err := json.Unmarshal(data, &bc); err != nil {
			return nil, fmt.Errorf("parsing %s: %w", entry.Name(), err)
		}
		boards = append(boards, &bc)
	}
	return boards, nil
}

// Match finds the board config matching the given model string.
func Match(boards []*BoardConfig, model string) (*BoardConfig, error) {
	model = strings.TrimRight(model, "\x00\n")
	for _, b := range boards {
		for _, m := range b.ModelStrings {
			if m == model {
				// Initialize ChipInstance
				c, err := chip.Get(b.Chip)
				if err != nil {
					return nil, fmt.Errorf("board %s: %w", b.ID, err)
				}
				b.ChipInstance = c
				return b, nil
			}
		}
	}
	return nil, fmt.Errorf("unsupported board model: %q", model)
}
