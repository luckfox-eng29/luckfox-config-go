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
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"luckfox-config/internal/config"
	"luckfox-config/internal/logger"
)

// Module4G manages the LTE/4G module available on Lyra Pi boards.
// Supports three connection modes: wwan, ppp, ndis.
type Module4G struct {
	deps Deps
}

func NewModule4G(deps Deps) *Module4G {
	return &Module4G{deps: deps}
}

func (m *Module4G) ID() string { return "module_4g" }
func (m *Module4G) IsActive() bool {
	// Check for any of the common interfaces
	for _, iface := range []string{"wwan0", "usb0", "ppp0"} {
		if _, err := os.Stat("/sys/class/net/" + iface); err == nil {
			return true
		}
	}
	return false
}
func (m *Module4G) IsConfigEnabled(cfg *config.Store) bool {
	return cfg.Get("MODULE_4G", "ENABLE") == "1"
}

func (m *Module4G) PinsUsed() []int { return nil }
func (m *Module4G) ConfigKeys() []string {
	return []string{"MODULE_4G_ENABLE", "MODULE_4G_MODE", "MODULE_4G_APN"}
}

// GetMode returns the 4G module mode from the config.
func (m *Module4G) GetMode() string {
	return m.deps.Cfg.Get("MODULE_4G", "MODE")
}

func (m *Module4G) Enable(ctx context.Context) error {
	if !m.moduleVisible() {
		return fmt.Errorf("4G module not detected: no 1e0e USB device, wwan0 interface or ttyUSB port found")
	}
	mode := m.deps.Cfg.GetDefault("MODULE_4G", "MODE", "ndis")
	apn := m.deps.Cfg.GetDefault("MODULE_4G", "APN", "")
	return m.apply(ctx, mode, apn)
}

// moduleVisible reports whether the LTE module is reachable over USB.
//
// The module enumerates on its own host controller, so the OTG role of the SoC
// says nothing about it: presence is the only useful check. Requiring host mode
// first rejected every RK3506 board, where the module is already up (1e0e:9001,
// wwan0 and the serial ports exist) while dr_mode is not set to host.
func (m *Module4G) moduleVisible() bool {
	if getCurrentPID() != "" {
		return true
	}
	if _, err := os.Stat("/sys/class/net/wwan0"); err == nil {
		return true
	}
	ports, _ := filepath.Glob("/dev/ttyUSB*")
	return len(ports) > 0
}

func (m *Module4G) Disable(ctx context.Context) error {
	m.stopRunningProcesses()
	return m.deps.Cfg.Set("MODULE_4G", "ENABLE", "0")
}

// stopRunningProcesses stops the 4G helpers of this module. udhcpc is shared
// with every other interface, so only the instances bound to the 4G interfaces
// are stopped: a bare killall udhcpc would also drop the lease of the wired
// network.
func (m *Module4G) stopRunningProcesses() {
	logger.Info("Stopping 4G background processes")
	for _, proc := range []string{"simcom-cm", "pppd", "luckfox_4g_reset"} {
		_ = exec.Command("killall", "-q", proc).Run()
	}
	stopDHCP := func(iface string) {
		_ = exec.Command("pkill", "-q", "-f", "udhcpc -i "+iface).Run()
	}
	stopDHCP("wwan0")
	if iface, err := detectLTEInterface(); err == nil && iface != "wwan0" {
		stopDHCP(iface)
	}
	// Bring down wwan0
	_ = exec.Command("ip", "link", "set", "wwan0", "down").Run()
	// Give it a moment to settle
	time.Sleep(2 * time.Second)
}

func (m *Module4G) LoadFromConfig(ctx context.Context, cfg *config.Store, apply bool) error {
	m.deps.Cfg = cfg
	if cfg.Get("MODULE_4G", "ENABLE") != "1" {
		return nil
	}
	if apply {
		return m.Enable(ctx)
	}
	return nil
}

func (m *Module4G) apply(ctx context.Context, mode, apn string) error {
	switch mode {
	case "wwan":
		return m.applyWWAN(ctx)
	case "ppp":
		return m.applyPPP(ctx, apn)
	case "ndis":
		return m.applyNDIS(ctx)
	default:
		return fmt.Errorf("unsupported 4G mode: %q", mode)
	}
}

func (m *Module4G) applyWWAN(ctx context.Context) error {
	logger.Info("Switching to WWAN mode, waiting 5s for system to settle")
	time.Sleep(5 * time.Second)

	m.stopRunningProcesses()

	if getCurrentPID() != "9001" {
		logger.Info("Current PID is not 9001, switching USB PID to 9001")
		if err := sendAT("AT+CUSBPIDSWITCH=9001,1,1"); err != nil {
			return err
		}
		// Wait for module to disconnect
		logger.Info("Waiting 5s for module to disconnect after PID switch")
		time.Sleep(5 * time.Second)
	}

	logger.Info("Waiting for USB device 9001")
	if err := waitForUSBDevice(ctx, "9001", 30*time.Second); err != nil {
		return err
	}
	logger.Info("Waiting for network interface wwan0")
	if err := waitForNetInterface(ctx, "wwan0", 30*time.Second); err != nil {
		return err
	}
	logger.Info("Bringing up wwan0")
	_ = exec.Command("ip", "link", "set", "wwan0", "up").Run()

	logger.Info("Starting simcom-cm and udhcpc")
	go exec.Command("simcom-cm").Run()
	go exec.Command("udhcpc", "-i", "wwan0").Run()

	return m.deps.Cfg.SetMulti("MODULE_4G", map[string]string{
		"ENABLE": "1",
		"MODE":   "wwan",
	})
}

func (m *Module4G) applyPPP(ctx context.Context, apn string) error {
	if apn == "" {
		apn = "ctnet"
	}
	logger.Info("Switching to PPP mode, waiting 10s for system to settle", "apn", apn)
	time.Sleep(10 * time.Second)

	m.stopRunningProcesses()

	// Write chat script config
	chatCfg := fmt.Sprintf(`ABORT "BUSY"
ABORT "NO CARRIER"
ABORT "NO DIALTONE"
ABORT "ERROR"
ABORT "NO ANSWER"
TIMEOUT 30
"" AT
OK ATE0
OK ATI;+CSUB;+CSQ;+CPIN?;+COPS?;+CGREG?;&D2
OK AT+CGDCONT=1,"IP","%s",,0,0
OK ATD*99#
CONNECT
`, apn)
	if err := os.WriteFile("/etc/ppp/peers/simcom-connect-chat", []byte(chatCfg), 0o644); err != nil {
		return fmt.Errorf("write chat config: %w", err)
	}

	// Write pppd peer config
	peerCfg := `connect "/usr/sbin/chat -v -f /etc/ppp/peers/simcom-connect-chat"
/dev/ttyUSB2
115200
noauth
defaultroute
usepeerdns
nodetach
`
	if err := os.WriteFile("/etc/ppp/peers/simcom-pppd", []byte(peerCfg), 0o644); err != nil {
		return fmt.Errorf("write ppp config: %w", err)
	}

	if getCurrentPID() != "9001" {
		logger.Info("Current PID is not 9001, switching USB PID to 9001")
		if err := sendAT("AT+CUSBPIDSWITCH=9001,1,1"); err != nil {
			return err
		}
		// Wait for module to disconnect
		logger.Info("Waiting 5s for module to disconnect after PID switch")
		time.Sleep(5 * time.Second)

		// The PID switch makes the module re-enumerate its USB stack: the old
		// ttyUSB nodes disappear and the new ones only show up when the
		// usb-serial driver probes the re-enumerated device again. Dialing
		// before that makes pppd open a dead port and exit immediately.
		logger.Info("Waiting for USB device 9001")
		if err := waitForUSBDevice(ctx, "9001", 30*time.Second); err != nil {
			return err
		}
	}

	logger.Info("Waiting for network interface wwan0")
	if err := waitForNetInterface(ctx, "wwan0", 30*time.Second); err != nil {
		return err
	}

	// The vendor script brings wwan0 up before dialing; pppd needs the module's
	// network function to be active on some SIMCom firmwares.
	_ = exec.Command("ip", "link", "set", "wwan0", "up").Run()

	// Setup pppd runtime environment
	_ = os.MkdirAll("/var/run/pppd/lock", 0o755)
	_ = exec.Command("chmod", "755", "/var/run/pppd/lock").Run()

	if _, err := os.Stat("/etc/ppp/resolv.conf"); os.IsNotExist(err) {
		_ = os.WriteFile("/etc/ppp/resolv.conf", []byte(""), 0o644)
	}
	_ = os.Remove("/etc/resolv.conf")
	_ = os.Symlink("/etc/ppp/resolv.conf", "/etc/resolv.conf")

	// Give it a moment as in shell script (sleep 10)
	time.Sleep(10 * time.Second)

	logger.Info("Starting pppd call simcom-pppd")
	go runPPPd()

	return m.deps.Cfg.SetMulti("MODULE_4G", map[string]string{
		"ENABLE": "1",
		"MODE":   "ppp",
		"APN":    apn,
	})
}

func (m *Module4G) applyNDIS(ctx context.Context) error {
	logger.Info("Switching to NDIS mode, waiting 5s for system to settle")
	time.Sleep(5 * time.Second)

	m.stopRunningProcesses()

	if getCurrentPID() != "9011" {
		logger.Info("Current PID is not 9011, switching USB PID to 9011")
		if err := sendAT("AT+CUSBPIDSWITCH=9011,1,1"); err != nil {
			return err
		}
		// Wait for module to disconnect
		logger.Info("Waiting 5s for module to disconnect after PID switch")
		time.Sleep(5 * time.Second)
	}

	logger.Info("Waiting for USB device 9011")
	if err := waitForUSBDevice(ctx, "9011", 30*time.Second); err != nil {
		return err
	}

	// Detect gadget interface from sysfs
	iface, err := detectLTEInterface()
	if err != nil {
		return err
	}
	logger.Info("Detected LTE interface", "interface", iface)

	logger.Info("Bringing up interface and starting udhcpc", "interface", iface)
	_ = exec.Command("ip", "link", "set", iface, "up").Run()
	go exec.Command("udhcpc", "-i", iface).Run()

	return m.deps.Cfg.SetMulti("MODULE_4G", map[string]string{
		"ENABLE": "1",
		"MODE":   "ndis",
	})
}

func sendAT(cmd string) error {
	logger.Info("Sending AT command", "cmd", cmd)
	// Try multiple ports as they might shift depending on mode
	ports := []string{"/dev/ttyUSB2", "/dev/ttyUSB1", "/dev/ttyUSB3"}

	// Wait up to 5s for any of the ports to become available
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		for _, port := range ports {
			f, err := os.OpenFile(port, os.O_RDWR, 0)
			if err != nil {
				continue
			}
			defer f.Close()
			_, err = fmt.Fprintf(f, "%s\r\n", cmd)
			if err == nil {
				logger.Info("AT command sent successfully", "port", port)
				time.Sleep(500 * time.Millisecond)
				return nil
			}
		}
		time.Sleep(500 * time.Millisecond)
	}

	return fmt.Errorf("send AT command %s: no serial port available", cmd)
}

func waitForUSBDevice(ctx context.Context, pid string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		out, _ := exec.Command("lsusb").Output()
		if strings.Contains(string(out), pid) {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(500 * time.Millisecond):
		}
	}
	return fmt.Errorf("USB device %s not found within %s", pid, timeout)
}

// pppdLogPath mirrors the vendor script's /tmp/.pppd_log redirection so a
// failed dial can still be diagnosed from the board.
const pppdLogPath = "/tmp/.pppd_log"

// runPPPd starts pppd in the background and records its output. pppd exits as
// soon as it cannot open the serial port or the chat script fails, so without
// capturing the output a failed dial is completely silent.
func runPPPd() {
	cmd := exec.Command("pppd", "call", "simcom-pppd")
	cmd.Dir = "/etc/ppp/peers"
	if logFile, err := os.OpenFile(pppdLogPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644); err == nil {
		cmd.Stdout = logFile
		cmd.Stderr = logFile
	}
	if err := cmd.Run(); err != nil {
		logger.Error("pppd exited", "err", err, "log", pppdLogPath)
	}
}

func waitForNetInterface(ctx context.Context, iface string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if _, err := os.Stat("/sys/class/net/" + iface); err == nil {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(500 * time.Millisecond):
		}
	}
	return fmt.Errorf("network interface %s not found within %s", iface, timeout)
}

func detectLTEInterface() (string, error) {
	// Better way: Iterate through all network interfaces in /sys/class/net
	// and find the one that is a USB device but NOT the gadget (ADB/RNDIS).
	entries, err := os.ReadDir("/sys/class/net")
	if err != nil {
		return "usb1", nil
	}

	for _, e := range entries {
		name := e.Name()
		// Only consider usbX or wwanX interfaces
		if !strings.HasPrefix(name, "usb") && !strings.HasPrefix(name, "wwan") {
			continue
		}

		// Check the symlink to see its physical path
		link, err := os.Readlink(filepath.Join("/sys/class/net", name))
		if err != nil {
			continue
		}

		// If the path contains "gadget", it's the SoC's local USB gadget interface
		if strings.Contains(link, "gadget") {
			logger.Debug("Skipping gadget interface", "name", name)
			continue
		}

		// Found a USB/WWAN interface that isn't the gadget
		logger.Info("Detected LTE interface via sysfs", "name", name, "path", link)
		return name, nil
	}

	// Fallback if not detected
	return "usb1", nil
}

func getCurrentPID() string {
	out, _ := exec.Command("lsusb").Output()
	s := string(out)
	if strings.Contains(s, "1e0e:9001") {
		return "9001"
	}
	if strings.Contains(s, "1e0e:9011") {
		return "9011"
	}
	return ""
}

var _ Peripheral = (*Module4G)(nil)
