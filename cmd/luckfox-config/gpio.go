/*
 * SPDX-FileCopyrightText: 2026 Luckfox Team
 *
 * SPDX-License-Identifier: Apache-2.0
 */

package main

import (
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"luckfox-config/internal/board"
	"luckfox-config/internal/config"
	"luckfox-config/internal/peripheral"
	"luckfox-config/internal/tui"
)

var (
	gpioListFlag    bool
	gpioPullFlag    string
	gpioDriveFlag   int
	gpioReleaseFlag bool
)

func newGPIOCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "gpio [PIN | BANK GROUP NUM]",
		Short: "Configure a GPIO pin (pull resistor, drive strength, iomux mode)",
		Long: `Configure a GPIO pin.

The pin is selected by a raw GPIO number (54), a pin diagram name (GPIO1_C6 or
RM_IO48), or by its bank/group/number triple (1 C 6). Without any pin the pins
the board exposes and their current state are listed.

By default the pin keeps the mux mode, pull resistor and drive strength already
recorded for it; --pull and --drive override the latter two. A pin the board pin
diagram does not expose is accepted as well: it has no peripheral of its own, so
its settings are saved under the pin's canonical name and replayed by the
boot-time load.

--release accepts any GPIO, including a pin the board pin diagram does not expose
and a pin no bank of this SoC covers, so a stale record can always be dropped.`,
		Example: `  luckfox-config gpio --list
  luckfox-config gpio 54 --pull up
  luckfox-config gpio GPIO1_C6 --pull down --drive 4
  luckfox-config gpio 1 C 6 --release
  luckfox-config gpio pull 41 up
  luckfox-config gpio mode 41 1
  luckfox-config gpio 41 mode 1`,
		Args: cobra.MaximumNArgs(3),
		RunE: runGPIO,
	}
	cmd.Flags().BoolVar(&gpioListFlag, "list", false, "List the GPIO pins exposed by the board")
	cmd.Flags().StringVar(&gpioPullFlag, "pull", "", "Pull resistor: none, up or down (default: keep current)")
	cmd.Flags().IntVar(&gpioDriveFlag, "drive", -1, "Drive strength level 0-5 (default: keep current)")
	cmd.Flags().BoolVar(&gpioReleaseFlag, "release", false, "Release the pin (input, no pull, default drive strength) and drop its saved settings; accepts any GPIO")
	cmd.AddCommand(cmdGPIOPull(), cmdGPIODrive(), cmdGPIOMode())
	return cmd
}

func cmdGPIOPull() *cobra.Command {
	return &cobra.Command{
		Use:   "pull <pin> <none|up|down>",
		Short: "Set pull resistor on a GPIO pin",
		Example: `  luckfox-config gpio pull 41 up
  luckfox-config gpio pull GPIO1_C6 none`,
		Args: cobra.ExactArgs(2),
		RunE: func(_ *cobra.Command, args []string) error {
			return runGPIOPull(args[:1], args[1])
		},
	}
}

func cmdGPIODrive() *cobra.Command {
	return &cobra.Command{
		Use:   "drive <pin> <level>",
		Short: "Set drive strength on a GPIO pin (level 0-5)",
		Example: `  luckfox-config gpio drive 41 2
  luckfox-config gpio drive GPIO1_C6 3`,
		Args: cobra.ExactArgs(2),
		RunE: func(_ *cobra.Command, args []string) error {
			return runGPIODrive(args[:1], args[1])
		},
	}
}

func cmdGPIOMode() *cobra.Command {
	return &cobra.Command{
		Use:   "mode <pin> <mode>",
		Short: "Set iomux mux mode on a GPIO pin",
		Example: `  luckfox-config gpio mode 41 0   # reset to GPIO
  luckfox-config gpio mode 41 16  # UART1 TX`,
		Args: cobra.ExactArgs(2),
		RunE: func(_ *cobra.Command, args []string) error {
			return runGPIOMode(args[:1], args[1])
		},
	}
}

func runGPIO(_ *cobra.Command, args []string) error {
	// "gpio 41 mode 1" is accepted alongside "gpio mode 41 1": cobra only hands
	// the arguments here when the first one is not a subcommand name.
	if len(args) == 3 && isGPIOAction(args[1]) {
		return runGPIOAction(args[1], args[0], args[2])
	}

	bc, err := initBoard()
	if err != nil {
		return err
	}
	appCtx, err := initAppContext(bc, config.DefaultPath)
	if err != nil {
		return err
	}
	// Mark pins in use without touching the hardware, so the reported state
	// matches the saved configuration.
	if err := appCtx.Registry.LoadAll(appCtx.Ctx, appCtx.Cfg, false); err != nil {
		return err
	}

	pins := boardGPIOs(appCtx)
	if gpioListFlag || len(args) == 0 {
		printGPIOList(appCtx, pins)
		return nil
	}

	raw, name, err := gpioSpec(appCtx, args)
	if err != nil {
		return err
	}

	// --release accepts any GPIO, not only the pins the board pin diagram
	// exposes: a pin recorded by an earlier command may well be outside it. It is
	// also the way to drop a record of a pin that never existed, so it skips the
	// pin range check below.
	if gpioReleaseFlag {
		return releaseGPIO(appCtx, raw, name)
	}

	// Nothing may be written to a pin the SoC has no bank for: the record would
	// outlive the command and the boot-time load would write a non-existent
	// register, aborting the whole load.
	if err := validateGPIOPin(appCtx, raw); err != nil {
		return err
	}

	pin := boardGPIOFor(appCtx, raw)
	if pin == nil {
		// A pin outside the board pin diagram has no peripheral owning it, so one
		// is built for the write. Its config module is the canonical pin name the
		// "gpio" subcommands use, so the boot-time load replays it the same way.
		g, err := peripheral.NewGPIOByName(name, appCtx.Deps)
		if err != nil {
			return err
		}
		pin = g
	}

	if gpioPullFlag != "" {
		pull, err := parsePullMode(gpioPullFlag)
		if err != nil {
			return err
		}
		pin.SetPull(pull)
	}
	if gpioDriveFlag >= 0 {
		if gpioDriveFlag > 5 {
			return fmt.Errorf("drive strength level %d out of range (0-5)", gpioDriveFlag)
		}
		pin.SetDS(gpioDriveFlag)
	}

	if err := pin.Enable(appCtx.Ctx); err != nil {
		return err
	}
	fmt.Printf("%s configured: pull=%s drive=%d\n", pin.Name(), pullModeName(pin.Pull()), pin.DriveStrength())
	return nil
}

// boardGPIOs returns the GPIO pins the board exposes in its pin diagram.
func boardGPIOs(appCtx *tui.AppContext) []*peripheral.GPIO {
	var out []*peripheral.GPIO
	for _, p := range appCtx.Registry.All() {
		if g, ok := p.(*peripheral.GPIO); ok {
			out = append(out, g)
		}
	}
	return out
}

// boardGPIOFor returns the GPIO peripheral that owns a raw pin number, or nil
// when the board pin diagram does not expose that pin.
func boardGPIOFor(appCtx *tui.AppContext, raw int) *peripheral.GPIO {
	for _, p := range appCtx.Registry.All() {
		if g, ok := p.(*peripheral.GPIO); ok && g.Pin() == raw {
			return g
		}
	}
	return nil
}

// isGPIOAction reports whether s names one of the per-attribute GPIO actions.
func isGPIOAction(s string) bool {
	switch s {
	case "pull", "drive", "mode":
		return true
	}
	return false
}

// runGPIOAction dispatches the positional form "gpio <pin> <action> <value>".
func runGPIOAction(action, pin, value string) error {
	switch action {
	case "pull":
		return runGPIOPull([]string{pin}, value)
	case "drive":
		return runGPIODrive([]string{pin}, value)
	case "mode":
		return runGPIOMode([]string{pin}, value)
	}
	return fmt.Errorf("unknown action %q, expected pull, drive or mode", action)
}

// validateGPIOPin rejects a raw GPIO number the chip has no bank for.
func validateGPIOPin(appCtx *tui.AppContext, raw int) error {
	pin := board.RawGPIOToPin(raw)
	if _, err := appCtx.Board.ChipInstance.GetGPIOBase(pin.Bank); err != nil {
		return fmt.Errorf("pin %s: %w", pin.String(), err)
	}
	return nil
}

// isDigits reports whether s is a non-empty run of decimal digits.
func isDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// gpioSpec resolves the pin arguments given on the command line to a raw GPIO
// number and to the name that pin is shown under. A bare number is a raw GPIO
// number: that is the form the gpio subcommands have always taken
// ("gpio mode 41 1").
func gpioSpec(appCtx *tui.AppContext, args []string) (int, string, error) {
	if len(args) == 3 {
		args = []string{fmt.Sprintf("GPIO%s_%s%s", args[0], strings.ToUpper(args[1]), args[2])}
	}
	if len(args) != 1 {
		return 0, "", fmt.Errorf("expected one pin or a bank/group/number triple")
	}

	up := strings.ToUpper(strings.TrimSpace(args[0]))
	var raw int
	if isDigits(up) {
		raw, _ = strconv.Atoi(up)
	} else if pin, err := board.ParseGPIO(up); err == nil {
		raw = pin.Raw()
	} else if rmio, rmioErr := board.ParseRMIO(up); rmioErr == nil {
		pin, err := board.RMIOToGPIO(appCtx.Board.ChipInstance, rmio)
		if err != nil {
			return 0, "", fmt.Errorf("RM_IO%d: %w", rmio, err)
		}
		raw = pin.Raw()
	} else {
		return 0, "", fmt.Errorf("invalid pin %q: expected a raw GPIO number, GPIO<bank>_<group><num>, RM_IO<n>, or a bank/group/number triple", args[0])
	}
	return raw, gpioName(appCtx, raw), nil
}

// gpioName returns the name to show a raw pin under: the board's own name when
// its pin diagram exposes the pin, the canonical GPIO name otherwise.
func gpioName(appCtx *tui.AppContext, raw int) string {
	if g := boardGPIOFor(appCtx, raw); g != nil {
		return g.Name()
	}
	return board.RawGPIOToPin(raw).String()
}

// releaseGPIO returns a pin to its default state and drops its saved settings.
// Pins the board pin diagram exposes are released through the pin layer, like
// any other pin: pins outside the diagram have no peripheral of their own.
func releaseGPIO(appCtx *tui.AppContext, raw int, name string) error {
	if err := peripheral.ReleasePin(appCtx.Board.ChipInstance, appCtx.Cfg, appCtx.Diagram, raw, name, pinModuleFor(appCtx, raw)); err != nil {
		return err
	}
	fmt.Printf("%s released (input, no pull, default drive strength)\n", name)
	return nil
}

// parsePullMode parses a --pull value.
func parsePullMode(s string) (peripheral.PullMode, error) {
	return peripheral.ParsePullMode(s)
}

// pullModeName returns the name of a pull mode as used on the command line.
func pullModeName(m peripheral.PullMode) string {
	return peripheral.PullModeName(m)
}

// printGPIOList prints the exposed pins with their saved state. Pins that the
// board names twice (RM_IO index and GPIO name) are listed once per physical
// pin, with the alternative name in parentheses.
func printGPIOList(appCtx *tui.AppContext, pins []*peripheral.GPIO) {
	if len(pins) == 0 {
		fmt.Println("No GPIO pin is exposed by this board")
		return
	}

	type group struct {
		names []string
		pins  []*peripheral.GPIO
	}
	byPin := make(map[int]*group)
	for _, g := range pins {
		grp := byPin[g.Pin()]
		if grp == nil {
			grp = &group{}
			byPin[g.Pin()] = grp
		}
		grp.names = append(grp.names, g.Name())
		grp.pins = append(grp.pins, g)
	}

	keys := make([]int, 0, len(byPin))
	for k := range byPin {
		keys = append(keys, k)
	}
	sort.Ints(keys)

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "PIN\tPULL\tDRIVE\tMODE")
	for _, k := range keys {
		grp := byPin[k]
		sort.Slice(grp.names, func(i, j int) bool { return len(grp.names[i]) < len(grp.names[j]) })
		label := grp.names[0]
		if len(grp.names) > 1 {
			label += " (" + strings.Join(grp.names[1:], ", ") + ")"
		}

		// The override lives under the module of the name that was used to
		// configure the pin, so every name of the physical pin is checked for one.
		pull, drive, mode := "-", "-", "-"
		for _, g := range grp.pins {
			mod := g.ConfigModule()
			if !peripheral.HasPinSetting(appCtx.Cfg, mod) {
				continue
			}
			if m, err := peripheral.ParsePullMode(appCtx.Cfg.Get(mod, "PULL")); err == nil {
				pull = peripheral.PullModeName(m)
			}
			if v := appCtx.Cfg.Get(mod, "DRIVE"); v != "" {
				drive = v
			}
			if v := appCtx.Cfg.Get(mod, "MODE"); v != "" {
				mode = v
			}
			break
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", label, pull, drive, mode)
	}
	_ = w.Flush()
}
