# luckfox-config-go

![luckfox](https://github.com/LuckfoxTECH/luckfox-pico/assets/144299491/cec5c4a5-22b9-4a9a-abb1-704b11651e88 "luckfox")

# Luckfox Pico SDK

[中文说明](./README_CN.md)

`luckfox-config` is a Linux configuration tool for Luckfox boards. It provides a TUI (terminal UI) and CLI subcommands to manage common peripheral and pin configurations.

## Features

- TUI for interactive board configuration
- GPIO: pin mux mode / pull resistor / drive strength
- Peripheral management: PWM / UART / I2C / SPI, etc.

## Supported boards & systems

- Boards: Luckfox Pico / Lyra / Aura series
- OS: Linux

## Build

Requirements:

- Go (see `go.mod`, currently `go 1.24.2`)
- GNU Make

Build for ARM (armv7) / ARM64:

```bash
make build-arm
make build-arm64
```

The outputs are placed in `dist/`:

- `dist/luckfox-config-arm`
- `dist/luckfox-config-arm64`

## Install via curl (recommended)

This installs the latest GitHub Release binary to `/usr/bin` (or `~/.local/bin` if sudo is not available).

```bash
curl -sSL https://raw.githubusercontent.com/LuckfoxTECH/luckfox-config-go/master/script/install.sh | bash
```

## Quick start

Run TUI:

```bash
sudo luckfox-config
```

Show version:

```bash
luckfox-config --version
```

## License

Apache-2.0. See [LICENSE](LICENSE).
