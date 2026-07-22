/*
 * SPDX-FileCopyrightText: 2026 Luckfox Team
 *
 * SPDX-License-Identifier: Apache-2.0
 */

package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/spf13/cobra"

	"log/slog"

	"luckfox-config/internal/board"
	"luckfox-config/internal/config"
	"luckfox-config/internal/embedcfg"
	"luckfox-config/internal/logger"
	"luckfox-config/internal/overlay"
	"luckfox-config/internal/peripheral"
	"luckfox-config/internal/pindiagram"
	"luckfox-config/internal/tui"
	"luckfox-config/internal/version"
)

var debug bool
var showVersion bool

const (
	loadChildEnv = "LUCKFOX_CONFIG_LOAD_CHILD"
	loadLogPath  = "/tmp/luckfox-config-load.log"
)

func main() {
	root := &cobra.Command{
		Use:   "luckfox-config",
		Short: "Luckfox Lyra board configuration tool",
		PersistentPreRun: func(cmd *cobra.Command, args []string) {
			if showVersion {
				fmt.Fprintln(cmd.OutOrStdout(), version.String())
				os.Exit(0)
			}
			if debug {
				logger.SetLevel(slog.LevelDebug)
			}
		},
		RunE: runTUI,
	}
	root.PersistentFlags().BoolVarP(&debug, "debug", "d", false, "Enable debug logging")
	root.PersistentFlags().BoolVarP(&showVersion, "version", "v", false, "Print version and exit")

	registerPeripheralCommands(root)

	root.AddCommand(
		&cobra.Command{
			Use:   "load",
			Short: "Apply saved configuration (run at boot)",
			RunE:  runLoad,
		},
		&cobra.Command{
			Use:   "show",
			Short: "Print the pin diagram",
			RunE:  runShow,
		},
		&cobra.Command{
			Use:   "update",
			Short: "Refresh FDT from boot partition",
			RunE:  runUpdate,
		},
	)

	if err := root.Execute(); err != nil {
		os.Exit(1)
	}
}

// ─── Init helpers ─────────────────────────────────────────────────────────────

func initBoard() (*board.BoardConfig, error) {
	model, err := board.DetectModel()
	if err != nil {
		return nil, fmt.Errorf("detect board: %w", err)
	}
	logger.Debug("Detected board model", "model", model)
	boards, err := board.LoadAll(embedcfg.FS, "boards")
	if err != nil {
		return nil, fmt.Errorf("load board configs: %w", err)
	}
	bc, err := board.Match(boards, model)
	if err != nil {
		return nil, err
	}
	return bc, nil
}

func initAppContext(bc *board.BoardConfig, cfgPath string) (*tui.AppContext, error) {
	var warnings []string

	cfg, err := config.Open(cfgPath)
	if err != nil {
		// If we can't open config, we might still want to run for viewing status,
		// but many things will fail. We'll proceed with an empty store.
		logger.Error("Failed to open config", "path", cfgPath, "error", err)
		warnings = append(warnings, fmt.Sprintf("Failed to open %s", cfgPath))
		// Create a dummy store so we don't panic
		cfg, _ = config.Open("/dev/null")
		if cfg == nil {
			// fallback if even /dev/null fails (highly unlikely)
			return nil, fmt.Errorf("open config: %w", err)
		}
	}

	// Check for ConfigFS overlays support
	if _, err := os.Stat("/sys/kernel/config/device-tree/overlays"); os.IsNotExist(err) {
		warnings = append(warnings, "ConfigFS overlays not supported (check kernel config)")
	}

	diagram := pindiagram.New(bc.PinDiagram)

	// Pre-mark reserved pins
	for _, rp := range bc.ReservedPins {
		rmio, err := board.RMIOFromGPIOString(bc.ChipInstance, rp.GPIO)
		if err == nil {
			diagram.MarkPin(board.RMIOName(rmio), true)
		}
	}

	dynOverlay := overlay.NewDynamic()

	// Detect boot media
	device, err := detectBootDevice(bc)
	if err != nil {
		// Static overlay may not be needed for all commands; proceed without it.
		device = ""
	}

	var staticOverlay *overlay.StaticOverlay
	if device != "" {
		so, err := overlay.NewStatic(bc.Chip, device)
		if err == nil {
			staticOverlay = so
		} else {
			logger.Error("Failed to open static overlay", "chip", bc.Chip, "device", device, "error", err)
		}
	}

	deps := peripheral.Deps{
		Cfg:           cfg,
		Diagram:       diagram,
		Dynamic:       dynOverlay,
		Static:        staticOverlay,
		Chip:          bc.ChipInstance,
		ConfigMode:    bc.ConfigMode,
		PinctrlNaming: bc.PinctrlNaming,
		SplitSPI:      bc.SplitSPI,
	}

	panels, err := peripheral.LoadPanelFamilies(embedcfg.FS, "panels")
	if err != nil {
		return nil, fmt.Errorf("load panel configs: %w", err)
	}

	registry := buildRegistry(bc, deps, panels)

	return &tui.AppContext{
		Board:    bc,
		Diagram:  diagram,
		Cfg:      cfg,
		Registry: registry,
		Panels:   panels,
		Ctx:      context.Background(),
		Warnings: warnings,
	}, nil
}

func detectBootDevice(bc *board.BoardConfig) (string, error) {
	// Try mmc first, then nand from board config
	for _, key := range []string{"mmc", "nand"} {
		paths, ok := bc.BootMedia[key]
		if !ok {
			continue
		}
		for _, path := range paths {
			if path == "" {
				continue
			}
			if _, err := os.Stat(path); err == nil {
				return path, nil
			}
		}
	}
	return "", fmt.Errorf("no boot device found")
}

// buildRegistry creates and registers all peripherals for the given board.
func buildRegistry(bc *board.BoardConfig, deps peripheral.Deps, panels []peripheral.DSIPanelFamily) *peripheral.Registry {
	reg := peripheral.NewRegistry()

	parse := func(s string) (int, int, int) {
		parts := strings.Split(s, "_")
		if len(parts) == 0 {
			return -1, 0, 0
		}
		num, _ := strconv.Atoi(parts[0])
		mux := 0
		extra := 0
		if len(parts) > 1 {
			if strings.HasPrefix(parts[1], "M") {
				m := strings.TrimPrefix(parts[1], "M")
				mux, _ = strconv.Atoi(m)
			} else {
				// Format might be ctrl_ch or ch_sub
				extra, _ = strconv.Atoi(parts[1])
				if len(parts) > 2 && strings.HasPrefix(parts[2], "M") {
					m := strings.TrimPrefix(parts[2], "M")
					mux, _ = strconv.Atoi(m)
				}
			}
		}
		return num, extra, mux
	}

	// PWM
	for _, s := range bc.Peripherals.PWM {
		p1, p2, mux := parse(s)
		if p1 < 0 {
			continue
		}
		if strings.ToLower(bc.ConfigMode) == "rmio" {
			// For RMIO, PWM format is "ctrl_ch"
			reg.Register(peripheral.NewPWM(p1, p2, deps))
		} else {
			// For Normal, PWM format is "ch_Mmux" or "ctrl_ch_Mmux"
			var p *peripheral.PWM
			if strings.Count(s, "_") >= 2 {
				// ctrl_ch_Mmux
				p = peripheral.NewPWM(p1, p2, deps)
			} else {
				// ch_Mmux
				p = peripheral.NewPWM(0, p1, deps)
			}
			p.SetMux(mux)
			reg.Register(p)
		}
	}

	// UART
	for _, s := range bc.Peripherals.UART {
		num, _, mux := parse(s)
		if num < 0 {
			continue
		}
		u := peripheral.NewUART(num, deps)
		u.SetMux(mux)
		reg.Register(u)
	}

	// I2C
	for _, s := range bc.Peripherals.I2C {
		num, _, mux := parse(s)
		if num < 0 {
			continue
		}
		i := peripheral.NewI2C(num, deps)
		i.SetMux(mux)
		reg.Register(i)
	}

	// SPI
	for _, s := range bc.Peripherals.SPI {
		num, _, mux := parse(s)
		if num < 0 {
			continue
		}
		sp := peripheral.NewSPI(num, deps)
		sp.SetMux(mux)
		reg.Register(sp)
	}

	// CAN
	for _, s := range bc.Peripherals.CAN {
		num, _, mux := parse(s)
		if num < 0 {
			continue
		}
		c := peripheral.NewCAN(num, deps)
		c.SetMux(mux)
		reg.Register(c)
	}

	// DSI
	reg.Register(peripheral.NewDSI(deps, bc.Features.DSIFDTNode, panels))

	// 4G Module (Lyra Pi only)
	if bc.Features.Has4GModule {
		reg.Register(peripheral.NewModule4G(deps))
	}

	// Compatible Apps
	for _, name := range bc.Peripherals.CompatibleApps {
		reg.Register(peripheral.NewCompatibleApp(name, bc.ID, deps))
	}

	// FBTFT
	if bc.Features.HasFBTFT {
		reg.Register(peripheral.NewFBTFT(deps, bc.Peripherals.FBTFT.GPIO, bc.Peripherals.FBTFT.SPI))
	}

	// USB
	if bc.Features.HasUSB {
		reg.Register(peripheral.NewUSB(deps))
	}

	// CSI
	if bc.Features.HasCSI {
		reg.Register(peripheral.NewCSI(deps))
	}

	// RGB
	if bc.Features.HasRGB {
		reg.Register(peripheral.NewRGB(deps, bc.Features.RGBPins))
	}

	// SDMMC
	if bc.Features.HasSDMMC {
		reg.Register(peripheral.NewSDMMC(deps))
	}

	return reg
}

// ─── Commands ─────────────────────────────────────────────────────────────────

func runTUI(_ *cobra.Command, _ []string) error {
	// Redirect logs to file in TUI mode
	f, err := os.OpenFile("/tmp/luckfox-config.log", os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return fmt.Errorf("open log file: %w", err)
	}
	defer f.Close()
	logger.SetOutput(f)

	bc, err := initBoard()
	if err != nil {
		return err
	}
	appCtx, err := initAppContext(bc, config.DefaultPath)
	if err != nil {
		return err
	}

	// Load current config to mark pins in use, but don't apply hardware changes (which could trigger kernel warnings/leaks)
	_ = appCtx.Registry.LoadAll(context.Background(), appCtx.Cfg, false)

	// Verify if config matches system state
	verifyWarnings := appCtx.Registry.Verify(appCtx.Cfg)
	if len(verifyWarnings) > 0 {
		msg := fmt.Sprintf("Some peripherals are not active (maybe run 'luckfox-config load'): \n%s", strings.Join(verifyWarnings, "\n"))
		appCtx.Warnings = append(appCtx.Warnings, msg)
	}

	opts := []tea.ProgramOption{tea.WithAltScreen()}
	opts = append(opts, tui.CompatOptions()...)

	p := tea.NewProgram(tui.New(appCtx), opts...)
	_, err = p.Run()
	return err
}

func runLoad(_ *cobra.Command, _ []string) error {
	if os.Getenv(loadChildEnv) != "1" {
		if err := spawnLoadChild(); err != nil {
			return err
		}
		return nil
	}

	logFile, err := os.OpenFile(loadLogPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return fmt.Errorf("open load log file: %w", err)
	}
	defer logFile.Close()
	logger.SetOutput(logFile)

	bc, err := initBoard()
	if err != nil {
		return err
	}
	appCtx, err := initAppContext(bc, config.DefaultPath)
	if err != nil {
		return err
	}

	logger.Info("Loading and applying configuration", "board", bc.ID)
	if err := appCtx.Registry.LoadAll(context.Background(), appCtx.Cfg, true); err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	logger.Info("Complete configuration loading")
	return nil
}

func spawnLoadChild() error {
	logFile, err := os.OpenFile(loadLogPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return fmt.Errorf("open load log file: %w", err)
	}
	defer logFile.Close()

	cmd := exec.Command(os.Args[0], os.Args[1:]...)
	cmd.Env = append(os.Environ(), loadChildEnv+"=1")
	cmd.Stdin = nil
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Setsid: true,
	}

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start load child: %w", err)
	}

	fmt.Printf("luckfox-config load started in background, pid=%d, log=%s\n", cmd.Process.Pid, loadLogPath)
	return nil
}

func runShow(_ *cobra.Command, _ []string) error {
	bc, err := initBoard()
	if err != nil {
		return err
	}
	appCtx, err := initAppContext(bc, config.DefaultPath)
	if err != nil {
		return err
	}

	// Load current config to mark pins in use, but don't apply hardware changes
	_ = appCtx.Registry.LoadAll(context.Background(), appCtx.Cfg, false)
	fmt.Print(appCtx.Diagram.Render())
	return nil
}

func runUpdate(_ *cobra.Command, _ []string) error {
	bc, err := initBoard()
	if err != nil {
		return err
	}
	device, err := detectBootDevice(bc)
	if err != nil {
		return err
	}
	so, err := overlay.NewStatic(bc.Chip, device)
	if err != nil {
		return fmt.Errorf("open static overlay: %w", err)
	}
	// Apply with no DTS content — just refreshes the FDT cache
	if err := so.Apply(""); err != nil {
		return err
	}
	fmt.Println("FDT cache refreshed successfully from boot partition")
	return nil
}
