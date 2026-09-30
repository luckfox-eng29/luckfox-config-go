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
		Short: "Configure a GPIO pin (pull resistor and drive strength)",
		Long: `Configure a GPIO pin the board exposes in its pin diagram.

The pin is selected either by name (RM_IO12 or GPIO1_C6, case insensitive) or by
its bank/group/number triple (1 C 6). Without any pin the exposed pins and their
current state are listed.

By default a pin is switched to GPIO output mode and keeps the pull resistor and
drive strength it already has; --pull and --drive override them.`,
		Example: `  luckfox-config gpio --list
  luckfox-config gpio GPIO1_C6 --pull up
  luckfox-config gpio RM_IO12 --pull down --drive 4
  luckfox-config gpio 1 C 6 --pull up
  luckfox-config gpio GPIO1_C6 --release`,
		Args: cobra.MaximumNArgs(3),
		RunE: runGPIO,
	}
	cmd.Flags().BoolVar(&gpioListFlag, "list", false, "List the GPIO pins exposed by the board")
	cmd.Flags().StringVar(&gpioPullFlag, "pull", "", "Pull resistor: none, up or down (default: keep current)")
	cmd.Flags().IntVar(&gpioDriveFlag, "drive", -1, "Drive strength level 0-5 (default: keep current)")
	cmd.Flags().BoolVar(&gpioReleaseFlag, "release", false, "Release the pin (input, no pull, default drive strength)")
	return cmd
}

func runGPIO(_ *cobra.Command, args []string) error {
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

	name, err := gpioPinName(args)
	if err != nil {
		return err
	}
	pin := findGPIO(pins, name)
	if pin == nil {
		return fmt.Errorf("pin %s is not exposed by this board; run 'luckfox-config gpio --list'", name)
	}

	if gpioReleaseFlag {
		if err := pin.Disable(appCtx.Ctx); err != nil {
			return err
		}
		fmt.Printf("%s released (input, no pull, default drive strength)\n", pin.Name())
		return nil
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
	fmt.Printf("%s enabled: pull=%s drive=%d\n", pin.Name(), pullModeName(pin.Pull()), pin.DriveStrength())
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

// gpioPinName turns the command arguments into a canonical pin name.
func gpioPinName(args []string) (string, error) {
	if len(args) == 3 {
		args = []string{fmt.Sprintf("GPIO%s_%s%s", args[0], strings.ToUpper(args[1]), args[2])}
	}
	if len(args) != 1 {
		return "", fmt.Errorf("expected one pin name or a bank/group/number triple")
	}

	up := strings.ToUpper(strings.TrimSpace(args[0]))
	if rmio, err := board.ParseRMIO(up); err == nil {
		return board.RMIOName(rmio), nil
	}
	pin, err := board.ParseGPIO(up)
	if err != nil {
		return "", fmt.Errorf("invalid pin %q: expected RM_IO<n> or GPIO<bank>_<group><num>", args[0])
	}
	return fmt.Sprintf("GPIO%d_%s%d", pin.Bank, pin.Group, pin.Number), nil
}

// findGPIO looks up a pin by name.
func findGPIO(pins []*peripheral.GPIO, name string) *peripheral.GPIO {
	for _, g := range pins {
		if strings.EqualFold(g.Name(), name) {
			return g
		}
	}
	return nil
}

// parsePullMode parses a --pull value.
func parsePullMode(s string) (peripheral.PullMode, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "none", "no", "off":
		return peripheral.PullNone, nil
	case "up":
		return peripheral.PullUp, nil
	case "down":
		return peripheral.PullDown, nil
	}
	return peripheral.PullNone, fmt.Errorf("invalid pull %q: expected none, up or down", s)
}

// pullModeName returns the name of a pull mode as used on the command line.
func pullModeName(m peripheral.PullMode) string {
	switch m {
	case peripheral.PullUp:
		return "up"
	case peripheral.PullDown:
		return "down"
	}
	return "none"
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
	fmt.Fprintln(w, "PIN\tSTATUS\tPULL\tDRIVE")
	for _, k := range keys {
		grp := byPin[k]
		sort.Slice(grp.names, func(i, j int) bool { return len(grp.names[i]) < len(grp.names[j]) })
		label := grp.names[0]
		if len(grp.names) > 1 {
			label += " (" + strings.Join(grp.names[1:], ", ") + ")"
		}

		// The saved state lives under the module of the name that was used to
		// configure the pin, so every name of the physical pin is checked.
		status, pull, drive := "disabled", "-", "-"
		for _, g := range grp.pins {
			mod := g.ConfigModule()
			if appCtx.Cfg.Get(mod, "STATUS") == "1" {
				status = "enabled"
				level, _ := strconv.Atoi(appCtx.Cfg.Get(mod, "PULL"))
				pull = pullModeName(peripheral.PullMode(level))
				drive = appCtx.Cfg.Get(mod, "DRIVE")
				break
			}
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", label, status, pull, drive)
	}
	_ = w.Flush()
}
