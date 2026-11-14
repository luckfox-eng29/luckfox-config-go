/*
 * SPDX-FileCopyrightText: 2026 Luckfox Team
 *
 * SPDX-License-Identifier: Apache-2.0
 */

package main

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"luckfox-config/internal/backup"
	"luckfox-config/internal/board"
	"luckfox-config/internal/config"
	"luckfox-config/internal/peripheral"
	"luckfox-config/internal/tui"
)

// registerPeripheralCommands adds all peripheral subcommands to root.
func registerPeripheralCommands(root *cobra.Command) {
	root.AddCommand(
		cmdPWM(),
		cmdUART(),
		cmdI2C(),
		cmdSPI(),
		cmdCAN(),
		cmdGetCSIUnite(),
		cmdSetCSIUnite(),
		cmdBackup(),
	)
}

// ─── CSI ──────────────────────────────────────────────────────────────────────

// cmdGetCSIUnite reports the CSI unite mode stored in the boot partition DTB.
// The value is read from the running device tree (/proc/device-tree), not from
// the config store.
// Kept top-level for compatibility with the legacy shell subcommand.
func cmdGetCSIUnite() *cobra.Command {
	return &cobra.Command{
		Use:     "get_csi_unite",
		Short:   "Print CSI unite mode from the boot DTB (0 = standard, 1 = 3840x2160)",
		Example: `  luckfox-config get_csi_unite`,
		Args:    cobra.NoArgs,
		// RunE errors should not dump the usage block.
		SilenceUsage: true,
		RunE: func(_ *cobra.Command, _ []string) error {
			appCtx, cleanup, err := quickInit()
			if err != nil {
				return err
			}
			defer cleanup()

			p, err := appCtx.Registry.Get("csi")
			if err != nil {
				return fmt.Errorf("CSI is not supported on this board")
			}

			unite := p.(*peripheral.CSI).UniteEnabled()
			fmt.Println(unite)
			return nil
		},
	}
}

// cmdSetCSIUnite writes the CSI unite mode into the boot partition DTB and
// enables CSI. A reboot is required for the change to take effect.
// Kept top-level for compatibility with the legacy shell subcommand.
func cmdSetCSIUnite() *cobra.Command {
	return &cobra.Command{
		Use:   "set_csi_unite <0|1>",
		Short: "Set CSI unite mode in the boot DTB (0 = standard, 1 = 3840x2160)",
		Long: `Set CSI unite mode in the boot partition DTB and enable CSI.

  0  standard mode
  1  unite mode (3840x2160)

A reboot is required for the change to take effect.`,
		Example: `  luckfox-config set_csi_unite 1`,
		Args:    cobra.ExactArgs(1),
		// RunE errors should not dump the usage block.
		SilenceUsage: true,
		RunE: func(_ *cobra.Command, args []string) error {
			var unite bool
			switch args[0] {
			case "0":
				unite = false
			case "1":
				unite = true
			default:
				return fmt.Errorf("value must be 0 or 1, got %q", args[0])
			}

			appCtx, cleanup, err := quickInit()
			if err != nil {
				return err
			}
			defer cleanup()

			p, err := appCtx.Registry.Get("csi")
			if err != nil {
				return fmt.Errorf("CSI is not supported on this board")
			}

			csi := p.(*peripheral.CSI)
			csi.SetUnite(unite)
			if err := csi.Enable(context.Background()); err != nil {
				return err
			}

			fmt.Printf("CSI unite set to %s. Reboot required.\n", args[0])
			return nil
		},
	}
}

// ─── Backup ───────────────────────────────────────────────────────────────────

func cmdBackup() *cobra.Command {
	return &cobra.Command{
		Use:   "backup <local|usb|sd>",
		Short: "Backup the root filesystem to an ext4 image",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			var media backup.MediaClass
			switch args[0] {
			case "local":
				media = backup.Local
			case "usb":
				media = backup.USBDisk
			case "sd":
				media = backup.SDCard
			default:
				return fmt.Errorf("invalid media class: %s", args[0])
			}

			progressChan := make(chan backup.Progress)
			go func() {
				for p := range progressChan {
					fmt.Printf("[%d%%] %s\n", p.Percentage, p.Message)
				}
			}()

			root, err := backup.RootPartition()
			if err != nil {
				return err
			}
			if root.FSType != "ext4" {
				return fmt.Errorf("root filesystem is not ext4 (found: %q)", root.FSType)
			}

			return backup.FilesystemBackup(root.Path, root.Name, media, progressChan)
		},
	}
}

// pinModuleFor returns the config module that stores a raw GPIO pin's state: the
// module of the board's GPIO peripheral when the pin diagram exposes the pin,
// otherwise the canonical GPIO name. Pins the diagram does not expose still get
// a module so the boot-time load can replay them.
func pinModuleFor(appCtx *tui.AppContext, gpio int) string {
	if g := boardGPIOFor(appCtx, gpio); g != nil {
		return g.ConfigModule()
	}
	return board.RawGPIOToPin(gpio).String()
}

// savePinKey records one pin setting so the boot-time load re-applies it.
func savePinKey(appCtx *tui.AppContext, gpio int, key, value string) error {
	return peripheral.SavePinSetting(appCtx.Cfg, pinModuleFor(appCtx, gpio), key, value)
}

func runGPIOPull(pinArgs []string, modeStr string) error {
	var mode peripheral.PullMode
	switch modeStr {
	case "none":
		mode = peripheral.PullNone
	case "up":
		mode = peripheral.PullUp
	case "down":
		mode = peripheral.PullDown
	default:
		return fmt.Errorf("pull mode must be none, up, or down")
	}

	appCtx, cleanup, err := quickInit()
	if err != nil {
		return err
	}
	defer cleanup()

	raw, name, err := gpioSpec(appCtx, pinArgs)
	if err != nil {
		return err
	}
	if err := validateGPIOPin(appCtx, raw); err != nil {
		return err
	}

	if err := peripheral.SetPull(appCtx.Board.ChipInstance, raw, mode); err != nil {
		return err
	}
	if err := savePinKey(appCtx, raw, "PULL", modeStr); err != nil {
		return err
	}
	fmt.Printf("%s pull set to %s\n", name, modeStr)
	return nil
}

func runGPIODrive(pinArgs []string, levelStr string) error {
	level, err := strconv.Atoi(levelStr)
	if err != nil || level < 0 || level > 5 {
		return fmt.Errorf("drive level must be 0-5, got %q", levelStr)
	}

	appCtx, cleanup, err := quickInit()
	if err != nil {
		return err
	}
	defer cleanup()

	raw, name, err := gpioSpec(appCtx, pinArgs)
	if err != nil {
		return err
	}
	if err := validateGPIOPin(appCtx, raw); err != nil {
		return err
	}

	if err := peripheral.SetDriveStrength(appCtx.Board.ChipInstance, raw, level); err != nil {
		return err
	}
	if err := savePinKey(appCtx, raw, "DRIVE", strconv.Itoa(level)); err != nil {
		return err
	}
	fmt.Printf("%s drive strength set to level %d\n", name, level)
	return nil
}

func runGPIOMode(pinArgs []string, modeStr string) error {
	mode, err := strconv.Atoi(modeStr)
	if err != nil || mode < 0 {
		return fmt.Errorf("mode must be a non-negative integer, got %q", modeStr)
	}
	appCtx, cleanup, err := quickInit()
	if err != nil {
		return err
	}
	defer cleanup()

	raw, name, err := gpioSpec(appCtx, pinArgs)
	if err != nil {
		return err
	}
	if err := validateGPIOPin(appCtx, raw); err != nil {
		return err
	}

	if err := peripheral.SetPinMode(appCtx.Board.ChipInstance, raw, mode); err != nil {
		return err
	}
	if err := savePinKey(appCtx, raw, "MODE", strconv.Itoa(mode)); err != nil {
		return err
	}
	fmt.Printf("%s iomux mode set to %d\n", name, mode)
	return nil
}

// ─── PWM ─────────────────────────────────────────────────────────────────────

func cmdPWM() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "pwm",
		Short: "PWM channel configuration",
	}
	cmd.AddCommand(cmdPWMEnable(), cmdPWMDisable())
	return cmd
}

func cmdPWMEnable() *cobra.Command {
	var pin int
	c := &cobra.Command{
		Use:   "enable <controller> <channel>",
		Short: "Enable a PWM channel",
		Example: `  luckfox-config pwm enable 0 2 --pin 5   # PWM0 ch2 on RM_IO5
  luckfox-config pwm enable 1 0 --pin 8   # PWM1 ch0 on RM_IO8`,
		Args: cobra.ExactArgs(2),
		RunE: func(_ *cobra.Command, args []string) error {
			ctrl, ch, err := parsePWMArgs(args)
			if err != nil {
				return err
			}
			if pin < 0 || pin > 31 {
				return fmt.Errorf("--pin must be 0-31")
			}
			appCtx, cleanup, err := quickInit()
			if err != nil {
				return err
			}
			defer cleanup()

			id := fmt.Sprintf("pwm%d_ch%d", ctrl, ch)
			p, err := appCtx.Registry.Get(id)
			if err != nil {
				return err
			}
			pw := p.(*peripheral.PWM)
			if pw.SystemReserved() {
				return fmt.Errorf("%s is enabled by the base device tree and cannot be claimed or released at runtime (RK3506 pwm teardown bug); change the pwm node in the board device tree instead", id)
			}
			pw.SetPin(pin)
			if err := p.Enable(context.Background()); err != nil {
				return err
			}
			fmt.Printf("PWM%d channel %d enabled on RM_IO%d\n", ctrl, ch, pin)
			return nil
		},
	}
	c.Flags().IntVar(&pin, "pin", -1, "RM_IO pin number (required)")
	_ = c.MarkFlagRequired("pin")
	return c
}

func cmdPWMDisable() *cobra.Command {
	return &cobra.Command{
		Use:     "disable <controller> <channel>",
		Short:   "Disable a PWM channel",
		Example: `  luckfox-config pwm disable 0 2`,
		Args:    cobra.ExactArgs(2),
		RunE: func(_ *cobra.Command, args []string) error {
			ctrl, ch, err := parsePWMArgs(args)
			if err != nil {
				return err
			}
			appCtx, cleanup, err := quickInit()
			if err != nil {
				return err
			}
			defer cleanup()

			id := fmt.Sprintf("pwm%d_ch%d", ctrl, ch)
			p, err := appCtx.Registry.Get(id)
			if err != nil {
				return err
			}
			if err := p.Disable(context.Background()); err != nil {
				return err
			}
			fmt.Printf("PWM%d channel %d disabled\n", ctrl, ch)
			if strings.ToLower(appCtx.Board.ConfigMode) == "rmio" {
				fmt.Println("note: RK3506 keeps the pwm controller alive until reboot (kernel pwmchip teardown bug), so the channel is released on the next boot")
			}
			return nil
		},
	}
}

func parsePWMArgs(args []string) (ctrl, ch int, err error) {
	ctrl, err = strconv.Atoi(args[0])
	if err != nil || ctrl < 0 || ctrl > 1 {
		return 0, 0, fmt.Errorf("controller must be 0 or 1, got %q", args[0])
	}
	ch, err = strconv.Atoi(args[1])
	if err != nil || ch < 0 || ch > 7 {
		return 0, 0, fmt.Errorf("channel must be 0-7, got %q", args[1])
	}
	return ctrl, ch, nil
}

// ─── UART ────────────────────────────────────────────────────────────────────

func cmdUART() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "uart",
		Short: "UART serial configuration",
	}
	cmd.AddCommand(cmdUARTEnable(), cmdUARTDisable())
	return cmd
}

func cmdUARTEnable() *cobra.Command {
	var tx, rx int
	c := &cobra.Command{
		Use:     "enable <N>",
		Short:   "Enable a UART (N = 1-4)",
		Example: `  luckfox-config uart enable 1 --tx 5 --rx 6`,
		Args:    cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			n, err := parsePeriphNum(args[0], 1, 4, "UART")
			if err != nil {
				return err
			}
			if err := validatePin("--tx", tx); err != nil {
				return err
			}
			if err := validatePin("--rx", rx); err != nil {
				return err
			}
			appCtx, cleanup, err := quickInit()
			if err != nil {
				return err
			}
			defer cleanup()

			p, err := appCtx.Registry.Get(fmt.Sprintf("uart%d", n))
			if err != nil {
				return err
			}
			p.(*peripheral.UART).SetPins(tx, rx)
			if err := p.Enable(context.Background()); err != nil {
				return err
			}
			fmt.Printf("UART%d enabled: TX=RM_IO%d RX=RM_IO%d\n", n, tx, rx)
			return nil
		},
	}
	c.Flags().IntVar(&tx, "tx", -1, "TX RM_IO pin (required)")
	c.Flags().IntVar(&rx, "rx", -1, "RX RM_IO pin (required)")
	_ = c.MarkFlagRequired("tx")
	_ = c.MarkFlagRequired("rx")
	return c
}

func cmdUARTDisable() *cobra.Command {
	return &cobra.Command{
		Use:     "disable <N>",
		Short:   "Disable a UART (N = 1-4)",
		Example: `  luckfox-config uart disable 1`,
		Args:    cobra.ExactArgs(1),
		RunE:    makeDisableCmd("uart", 1, 4, "UART"),
	}
}

// ─── I2C ─────────────────────────────────────────────────────────────────────

func cmdI2C() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "i2c",
		Short: "I2C bus configuration",
	}
	cmd.AddCommand(cmdI2CEnable(), cmdI2CDisable())
	return cmd
}

func cmdI2CEnable() *cobra.Command {
	var sda, scl, speed int
	c := &cobra.Command{
		Use:   "enable <N>",
		Short: "Enable an I2C bus (N = 0-2)",
		Example: `  luckfox-config i2c enable 0 --sda 0 --scl 1
  luckfox-config i2c enable 1 --sda 2 --scl 3 --speed 400000`,
		Args: cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			n, err := parsePeriphNum(args[0], 0, 2, "I2C")
			if err != nil {
				return err
			}
			if err := validatePin("--sda", sda); err != nil {
				return err
			}
			if err := validatePin("--scl", scl); err != nil {
				return err
			}
			appCtx, cleanup, err := quickInit()
			if err != nil {
				return err
			}
			defer cleanup()

			p, err := appCtx.Registry.Get(fmt.Sprintf("i2c%d", n))
			if err != nil {
				return err
			}
			i := p.(*peripheral.I2C)
			i.SetPins(sda, scl)
			if speed > 0 {
				i.SetSpeed(speed)
			}
			if err := p.Enable(context.Background()); err != nil {
				return err
			}
			fmt.Printf("I2C%d enabled: SDA=RM_IO%d SCL=RM_IO%d speed=%dHz\n", n, sda, scl, speed)
			return nil
		},
	}
	c.Flags().IntVar(&sda, "sda", -1, "SDA RM_IO pin (required)")
	c.Flags().IntVar(&scl, "scl", -1, "SCL RM_IO pin (required)")
	c.Flags().IntVar(&speed, "speed", 5000000, "Clock speed in Hz (default 5000000)")
	_ = c.MarkFlagRequired("sda")
	_ = c.MarkFlagRequired("scl")
	return c
}

func cmdI2CDisable() *cobra.Command {
	return &cobra.Command{
		Use:     "disable <N>",
		Short:   "Disable an I2C bus (N = 0-2)",
		Example: `  luckfox-config i2c disable 0`,
		Args:    cobra.ExactArgs(1),
		RunE:    makeDisableCmd("i2c", 0, 2, "I2C"),
	}
}

// ─── SPI ─────────────────────────────────────────────────────────────────────

func cmdSPI() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "spi",
		Short: "SPI bus configuration",
	}
	cmd.AddCommand(cmdSPIEnable(), cmdSPIDisable())
	return cmd
}

func cmdSPIEnable() *cobra.Command {
	var sclk, mosi, miso, cs, speed int
	c := &cobra.Command{
		Use:   "enable <N>",
		Short: "Enable an SPI bus (N = 0-1)",
		Example: `  luckfox-config spi enable 0 --sclk 4 --mosi 5 --miso 6 --cs 7
  luckfox-config spi enable 0 --sclk 4 --mosi 5          # MISO and CS optional`,
		Args: cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			n, err := parsePeriphNum(args[0], 0, 1, "SPI")
			if err != nil {
				return err
			}
			if err := validatePin("--sclk", sclk); err != nil {
				return err
			}
			if err := validatePin("--mosi", mosi); err != nil {
				return err
			}
			appCtx, cleanup, err := quickInit()
			if err != nil {
				return err
			}
			defer cleanup()

			p, err := appCtx.Registry.Get(fmt.Sprintf("spi%d", n))
			if err != nil {
				return err
			}
			s := p.(*peripheral.SPI)
			s.SetPins(sclk, mosi, miso, cs)
			if speed > 0 {
				s.SetSpeed(speed)
			}
			if err := p.Enable(context.Background()); err != nil {
				return err
			}
			fmt.Printf("SPI%d enabled: SCLK=RM_IO%d MOSI=RM_IO%d MISO=%s CS=%s speed=%dHz\n",
				n, sclk, mosi, pinStr(miso), pinStr(cs), speed)
			return nil
		},
	}
	c.Flags().IntVar(&sclk, "sclk", -1, "SCLK RM_IO pin (required)")
	c.Flags().IntVar(&mosi, "mosi", -1, "MOSI RM_IO pin (required)")
	c.Flags().IntVar(&miso, "miso", -1, "MISO RM_IO pin (-1 = disabled)")
	c.Flags().IntVar(&cs, "cs", -1, "CS RM_IO pin (-1 = disabled)")
	c.Flags().IntVar(&speed, "speed", 10000000, "Clock speed in Hz (default 10000000)")
	_ = c.MarkFlagRequired("sclk")
	_ = c.MarkFlagRequired("mosi")
	return c
}

func cmdSPIDisable() *cobra.Command {
	return &cobra.Command{
		Use:     "disable <N>",
		Short:   "Disable an SPI bus (N = 0-1)",
		Example: `  luckfox-config spi disable 0`,
		Args:    cobra.ExactArgs(1),
		RunE:    makeDisableCmd("spi", 0, 1, "SPI"),
	}
}

// ─── CAN ─────────────────────────────────────────────────────────────────────

func cmdCAN() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "can",
		Short: "CAN bus configuration",
	}
	cmd.AddCommand(cmdCANEnable(), cmdCANDisable())
	return cmd
}

func cmdCANEnable() *cobra.Command {
	var tx, rx, speed int
	c := &cobra.Command{
		Use:   "enable <N>",
		Short: "Enable a CAN bus (N = 0-1)",
		Example: `  luckfox-config can enable 0 --tx 8 --rx 9
  luckfox-config can enable 0 --tx 8 --rx 9 --speed 1000000`,
		Args: cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			n, err := parsePeriphNum(args[0], 0, 1, "CAN")
			if err != nil {
				return err
			}
			if err := validatePin("--tx", tx); err != nil {
				return err
			}
			if err := validatePin("--rx", rx); err != nil {
				return err
			}
			appCtx, cleanup, err := quickInit()
			if err != nil {
				return err
			}
			defer cleanup()

			p, err := appCtx.Registry.Get(fmt.Sprintf("can%d", n))
			if err != nil {
				return err
			}
			c := p.(*peripheral.CAN)
			c.SetPins(tx, rx)
			if speed > 0 {
				c.SetSpeed(speed)
			}
			if err := p.Enable(context.Background()); err != nil {
				return err
			}
			fmt.Printf("CAN%d enabled: TX=RM_IO%d RX=RM_IO%d speed=%dHz\n", n, tx, rx, speed)
			return nil
		},
	}
	c.Flags().IntVar(&tx, "tx", -1, "TX RM_IO pin (required)")
	c.Flags().IntVar(&rx, "rx", -1, "RX RM_IO pin (required)")
	c.Flags().IntVar(&speed, "speed", 300000000, "Assigned clock rate in Hz (default 300000000)")
	_ = c.MarkFlagRequired("tx")
	_ = c.MarkFlagRequired("rx")
	return c
}

func cmdCANDisable() *cobra.Command {
	return &cobra.Command{
		Use:     "disable <N>",
		Short:   "Disable a CAN bus (N = 0-1)",
		Example: `  luckfox-config can disable 0`,
		Args:    cobra.ExactArgs(1),
		RunE:    makeDisableCmd("can", 0, 1, "CAN"),
	}
}

// ─── Helpers ──────────────────────────────────────────────────────────────────

// quickInit initialises the board + AppContext with minimal overhead.
// Returns a cleanup func (no-op for now, reserved for future resource release).
func quickInit() (*tui.AppContext, func(), error) {
	bc, err := initBoard()
	if err != nil {
		return nil, nil, err
	}
	appCtx, err := initAppContext(bc, config.DefaultPath)
	if err != nil {
		return nil, nil, err
	}
	return appCtx, func() {}, nil
}

// makeDisableCmd returns a RunE for <prefix> disable <N> commands.
func makeDisableCmd(prefix string, min, max int, label string) func(*cobra.Command, []string) error {
	return func(_ *cobra.Command, args []string) error {
		n, err := parsePeriphNum(args[0], min, max, label)
		if err != nil {
			return err
		}
		appCtx, cleanup, err := quickInit()
		if err != nil {
			return err
		}
		defer cleanup()

		id := fmt.Sprintf("%s%d", prefix, n)
		p, err := appCtx.Registry.Get(id)
		if err != nil {
			// If not found, try searching for any mux (e.g. uart1m0)
			found := false
			for _, item := range appCtx.Registry.All() {
				if strings.HasPrefix(item.ID(), id+"m") {
					p = item
					found = true
					break
				}
			}
			if !found {
				return err
			}
		}

		if err := p.Disable(context.Background()); err != nil {
			return err
		}
		fmt.Printf("%s%d disabled\n", label, n)
		return nil
	}
}

func parsePeriphNum(s string, min, max int, label string) (int, error) {
	n, err := strconv.Atoi(s)
	if err != nil || n < min || n > max {
		return 0, fmt.Errorf("%s number must be %d-%d, got %q", label, min, max, s)
	}
	return n, nil
}

func validatePin(flag string, pin int) error {
	if pin < 0 || pin > 31 {
		return fmt.Errorf("%s must be 0-31", flag)
	}
	return nil
}

func pinStr(pin int) string {
	if pin < 0 {
		return "disabled"
	}
	return fmt.Sprintf("RM_IO%d", pin)
}
