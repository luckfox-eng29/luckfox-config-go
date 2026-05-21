# luckfox-config-go

![luckfox](https://github.com/LuckfoxTECH/luckfox-pico/assets/144299491/cec5c4a5-22b9-4a9a-abb1-704b11651e88 "luckfox")

# Luckfox Pico SDK

[English Version](./README.md)

`luckfox-config` 是面向 Luckfox 板卡的 Linux 配置工具，提供 TUI（终端交互界面）以及命令行子命令，用于管理常见外设与引脚相关配置。

## 功能

- TUI 交互式配置界面
- GPIO：复用模式 / 上下拉 / 驱动能力
- 外设管理：PWM / UART / I2C / SPI  等

## 适用版型与系统

- 版型：Luckfox Pico / Lyra / Aura 系列
- 系统：Linux 

## 编译

依赖：

- Go（参考 `go.mod`，当前为 `go 1.24.2`）
- GNU Make

编译 ARM（armv7）/ ARM64：

```bash
make build-arm
make build-arm64
```

产物位于 `dist/` 目录：

- `dist/luckfox-config-arm`
- `dist/luckfox-config-arm64`

## 通过 curl 安装（推荐）

会从 GitHub Release 下载最新版本并安装到 `/usr/bin`（若无 sudo 则安装到 `~/.local/bin`）。

```bash
curl -sSL https://raw.githubusercontent.com/LuckfoxTECH/luckfox-config-go/master/script/install.sh | bash
```

## 快速开始

运行 TUI：

```bash
sudo luckfox-config
```

查看版本：

```bash
luckfox-config --version
```

## 开源协议

Apache-2.0，详见 [LICENSE](LICENSE)。
