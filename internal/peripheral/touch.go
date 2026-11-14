/*
 * SPDX-FileCopyrightText: 2026 Luckfox Team
 *
 * SPDX-License-Identifier: Apache-2.0
 */

package peripheral

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"luckfox-config/internal/board"
	"luckfox-config/internal/config"
	"luckfox-config/internal/logger"
)

// Touchscreen controls the standalone GT911 touch controller used on boards
// where the touchscreen is not tied to a DSI panel (RV1106 Pico Ultra).
//
// The controller sits on the I2C bus that is also offered as a normal I2C
// peripheral, so enabling the touchscreen also enables that bus and routes its
// pinctrl pins; the two cannot be configured at the same time.
type Touchscreen struct {
	deps Deps
	cfg  board.TouchConfig
	addr string
}

// touchAddrRe matches the I2C addresses accepted from the config store before
// they are embedded into a device tree overlay.
var touchAddrRe = regexp.MustCompile(`^(0x[0-9a-fA-F]+|[0-9]+)$`)

// NewTouchscreen creates the touchscreen switch for a board describing one.
func NewTouchscreen(deps Deps) *Touchscreen {
	t := &Touchscreen{deps: deps, cfg: deps.Touch}
	t.addr = defaultTouchAddress(deps.Touch)
	return t
}

// defaultTouchAddress returns the board default I2C address of the controller.
func defaultTouchAddress(cfg board.TouchConfig) string {
	if len(cfg.Addresses) > 0 {
		return cfg.Addresses[0]
	}
	return "0x14"
}

func (t *Touchscreen) ID() string { return "touchscreen" }

// PinsUsed returns no RM_IO indices: the I2C pins are owned by the I2C
// peripheral, the interrupt/reset pins are routed by pinctrl in the overlay.
func (t *Touchscreen) PinsUsed() []int { return nil }

func (t *Touchscreen) ConfigKeys() []string { return nil }

func (t *Touchscreen) IsActive() bool {
	return IsNodeEnabled(t.cfg.Node)
}

// IsConfigEnabled is always false: the touchscreen state lives in the device
// tree, not in the config store.
func (t *Touchscreen) IsConfigEnabled(*config.Store) bool { return false }

// Addresses returns the selectable I2C addresses of the controller.
func (t *Touchscreen) Addresses() []string { return t.cfg.Addresses }

// Address returns the I2C address recorded in the running device tree, falling
// back to the pending selection and then to the board default.
func (t *Touchscreen) Address() string {
	if v, err := GetNodeU32(t.cfg.Node, "reg"); err == nil {
		return fmt.Sprintf("0x%x", v)
	}
	if t.addr != "" {
		return t.addr
	}
	return defaultTouchAddress(t.cfg)
}

// SetAddress selects the I2C address the controller is probed at.
func (t *Touchscreen) SetAddress(addr string) error {
	if !touchAddrRe.MatchString(addr) {
		return fmt.Errorf("invalid touchscreen I2C address %q", addr)
	}
	t.addr = addr
	return nil
}

func (t *Touchscreen) Enable(ctx context.Context) error {
	if t.deps.Static == nil {
		return fmt.Errorf("static overlay not available")
	}
	if err := t.checkBusConflict(); err != nil {
		return err
	}

	dts, err := t.buildDTS(true)
	if err != nil {
		return err
	}
	if err := t.deps.Static.Apply(dts); err != nil {
		return err
	}

	logger.Info("Touchscreen enabled", "node", t.cfg.Node, "address", t.addr)
	return nil
}

func (t *Touchscreen) Disable(ctx context.Context) error {
	if t.deps.Static == nil {
		return fmt.Errorf("static overlay not available")
	}

	dts, err := t.buildDTS(false)
	if err != nil {
		return err
	}
	if err := t.deps.Static.Apply(dts); err != nil {
		return err
	}

	logger.Info("Touchscreen disabled", "node", t.cfg.Node)
	return nil
}

// LoadFromConfig syncs the pending address with the running device tree. Static
// overlays are not replayed: the device tree already holds the configuration, so
// load must not rewrite the boot partition.
func (t *Touchscreen) LoadFromConfig(ctx context.Context, cfg *config.Store, apply bool) error {
	t.deps.Cfg = cfg
	t.addr = t.Address()
	return nil
}

// checkBusConflict rejects enabling the touchscreen while the I2C controller it
// is attached to is already enabled as a plain I2C bus. The bus node is enabled
// by the touchscreen overlay itself, so an enabled bus next to a disabled touch
// node means the bus is in use elsewhere.
func (t *Touchscreen) checkBusConflict() error {
	if t.cfg.Bus == "" {
		return nil
	}
	if IsNodeEnabled(t.cfg.Bus) && !IsNodeEnabled(t.cfg.Node) {
		return fmt.Errorf("I2C%d is enabled, disable it before enabling the touchscreen", t.cfg.BusNumber)
	}
	return nil
}

// buildDTS returns the overlay that enables or disables the touch controller.
// Enabling it also turns the I2C bus on and routes the bus plus the interrupt
// and reset pins, mirroring the shell script's fdt overlay.
func (t *Touchscreen) buildDTS(enable bool) (string, error) {
	if !enable {
		return fmt.Sprintf(`/dts-v1/;
/plugin/;

&{%s} {
	status = "disabled";
};

&{%s} {
	status = "disabled";
};
`, t.cfg.Bus, t.cfg.Node), nil
	}

	pinctrl, err := t.pinctrlProperty()
	if err != nil {
		return "", err
	}

	return fmt.Sprintf(`/dts-v1/;
/plugin/;

&{%s} {
	status = "okay";
%s};

&{%s} {
	status = "okay";
	reg = <%s>;
};
`, t.cfg.Bus, pinctrl, t.cfg.Node, t.addr), nil
}

// pinctrlProperty resolves the pinctrl node names of the board into a pinctrl-0
// property. Node names that the board does not describe are skipped.
func (t *Touchscreen) pinctrlProperty() (string, error) {
	if len(t.cfg.Pinctrl) == 0 {
		return "", nil
	}
	if t.deps.Static == nil {
		return "", fmt.Errorf("static overlay not available")
	}

	var refs []string
	for _, name := range t.cfg.Pinctrl {
		phandle, err := t.deps.Static.GetPhandle(name)
		if err != nil {
			logger.Debug("Pinctrl node not found, skipping", "node", name)
			continue
		}
		refs = append(refs, phandle)
	}
	if len(refs) == 0 {
		return "", nil
	}
	return fmt.Sprintf("\tpinctrl-0 = <%s>;\n", strings.Join(refs, " ")), nil
}

var _ Peripheral = (*Touchscreen)(nil)
