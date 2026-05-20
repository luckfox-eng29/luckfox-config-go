/*
 * SPDX-FileCopyrightText: 2026 Luckfox Team
 *
 * SPDX-License-Identifier: Apache-2.0
 */

package logger

import (
	"context"
	"io"
	"log/slog"
	"os"
)

var (
	Default *slog.Logger
	level   *slog.LevelVar
)

func init() {
	level = &slog.LevelVar{}
	level.Set(slog.LevelInfo)
	Default = slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{
		Level: level,
	}))
}

// SetLevel sets the global log level.
func SetLevel(l slog.Level) {
	level.Set(l)
}

// SetOutput sets the output writer for the global logger.
func SetOutput(w io.Writer) {
	Default = slog.New(slog.NewTextHandler(w, &slog.HandlerOptions{
		Level: level,
	}))
}

func Info(msg string, args ...any) {
	Default.Info(msg, args...)
}

func Error(msg string, args ...any) {
	Default.Error(msg, args...)
}

func Warn(msg string, args ...any) {
	Default.Warn(msg, args...)
}

func Debug(msg string, args ...any) {
	Default.Debug(msg, args...)
}

func Log(ctx context.Context, l slog.Level, msg string, args ...any) {
	Default.Log(ctx, l, msg, args...)
}
