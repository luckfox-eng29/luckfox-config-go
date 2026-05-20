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

	"luckfox-config/internal/config"
	"luckfox-config/internal/logger"
)

type SDMMC struct {
	deps Deps
}

func NewSDMMC(deps Deps) *SDMMC {
	return &SDMMC{deps: deps}
}

func (s *SDMMC) ID() string      { return "sdmmc" }
func (s *SDMMC) PinsUsed() []int { return nil }
func (s *SDMMC) ConfigKeys() []string {
	return []string{"SDMMC_ENABLE"}
}

func (s *SDMMC) IsActive() bool {
	overlayPath := "/sys/kernel/config/device-tree/overlays/SDMMC"
	if _, err := os.Stat(overlayPath); err == nil {
		return true
	}
	return false
}

func (s *SDMMC) IsConfigEnabled(cfg *config.Store) bool {
	return cfg.Get("SDMMC", "ENABLE") == "1"
}

func (s *SDMMC) Enable(ctx context.Context) error {
	mmcPath, err := s.deps.ResolveAlias("mmc1")
	if err != nil {
		return fmt.Errorf("sdmmc: resolve mmc1: %w", err)
	}

	dts := fmt.Sprintf(`/dts-v1/;
/plugin/;

&{%s} {
	status = "okay";
};
`, mmcPath)

	if err := s.deps.Dynamic.Apply("SDMMC", dts); err != nil {
		return err
	}

	logger.Info("SDMMC enabled")
	return s.deps.Cfg.Set("SDMMC", "ENABLE", "1")
}

func (s *SDMMC) Disable(ctx context.Context) error {
	mmcPath, err := s.deps.ResolveAlias("mmc1")
	if err != nil {
		return fmt.Errorf("sdmmc: resolve mmc1: %w", err)
	}

	dts := fmt.Sprintf(`/dts-v1/;
/plugin/;

&{%s} {
	status = "disabled";
};
`, mmcPath)

	if err := s.deps.Dynamic.Apply("SDMMC", dts); err != nil {
		return err
	}

	logger.Info("SDMMC disabled")
	return s.deps.Cfg.Set("SDMMC", "ENABLE", "0")
}

func (s *SDMMC) LoadFromConfig(ctx context.Context, cfg *config.Store, apply bool) error {
	s.deps.Cfg = cfg
	if cfg.Get("SDMMC", "ENABLE") != "1" {
		return nil
	}
	if apply {
		return s.Enable(ctx)
	}
	return nil
}

var _ Peripheral = (*SDMMC)(nil)
