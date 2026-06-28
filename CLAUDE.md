# GoMuxProxy

轻量级本地代理转发桌面工具，通过 HTTP CONNECT 将 SOCKS5/HTTP 流量转发至上游代理。

## 技术栈

- Go 1.24+ / Fyne v2.7.4 GUI
- 上游代理：HTTP CONNECT（默认 127.0.0.1:10810）
- 协议：SOCKS5 (RFC 1928)、HTTP CONNECT、混合模式（首字节自动检测）

## 项目结构

```
main.go                  — 入口，单实例锁(127.0.0.1:48321)
proxy/
  proxy.go               — ProxyEngine 核心：监听管理、连接处理、配置读写、验证、缓冲池
  socks5.go              — SOCKS5 握手（无认证，支持 IPv4/IPv6/域名）
  http.go                — HTTP 代理（CONNECT 隧道 + 普通 HTTP 代理）
  tunnel.go              — 辅助：bufReader、HTTP 响应读取、multiReader
gui/
  app.go                 — Fyne 主窗口、Tab 布局、事件回调（fyne.Do 线程安全）
  dashboard.go           — 状态监控 Tab：统计卡片 + 端口状态 + 连接表
  logview.go             — 日志 Tab：过滤、清空、5000 行上限
  config.go              — 配置 Tab：上游地址、监听列表增删改查、保存/加载 config.json
  tray.go                — 系统托盘：显示窗口 / 退出
  icon.go                — //go:embed icon_256.png
  icon_256.png           — 窗口/托盘图标（256x256）
  icon_32.png            — 小图标（32x32）
resource.rc              — Windows 资源：1 ICON "icon.ico" + manifest
GoMuxProxy.exe.manifest  — DPI 感知、长路径支持
icon.ico                 — exe 文件图标（多尺寸）
```

## 编译方法

### 前置依赖

- Go 1.24+
- LLVM-MinGW（windres 编译 .rc -> .syso）
- Python Pillow（仅修改图标时需要）

### 编译命令（PowerShell）

```powershell
cd D:\_Code\go\GoMuxProxy

# 1. 编译 Windows 资源
$windres = "C:\Users\Administrator.DESKTOP-FV5MPI3\AppData\Local\Microsoft\WinGet\Packages\MartinStorsjo.LLVM-MinGW.UCRT_Microsoft.Winget.Source_8wekyb3d8bbwe\llvm-mingw-20251216-ucrt-x86_64\bin\x86_64-w64-mingw32-windres.exe"
& $windres resource.rc -o resource.syso

# 2. 编译 Go（-H windowsgui 隐藏控制台窗口）
$env:CC = "C:\Users\Administrator.DESKTOP-FV5MPI3\AppData\Local\Microsoft\WinGet\Packages\MartinStorsjo.LLVM-MinGW.UCRT_Microsoft.Winget.Source_8wekyb3d8bbwe\llvm-mingw-20251216-ucrt-x86_64\bin\gcc.exe"
go build -ldflags "-H windowsgui" -o GoMuxProxy.exe .
```

### 更改图标流程

1. 准备 256x256 PNG 图标文件
2. Python 转换：
   ```python
   from PIL import Image
   img = Image.open("新图标.png")
   img.save("icon.ico", format="ICO", sizes=[(16,16),(32,32),(48,48),(64,64),(128,128),(256,256)])
   img.resize((256,256), Image.LANCZOS).save("gui/icon_256.png")
   img.resize((32,32), Image.LANCZOS).save("gui/icon_32.png")
   ```
3. 重新执行编译命令

## 配置文件

`config.json`（程序目录下，GUI 或手动编辑）：

```json
{
  "upstream_proxy": "127.0.0.1:10810",
  "listeners": [
    {"network": "tcp", "address": "127.0.0.1:1081", "protocol": "mixed", "enabled": true},
    {"network": "tcp", "address": "127.0.0.1:1082", "protocol": "mixed", "enabled": true},
    {"network": "tcp", "address": "127.0.0.1:1083", "protocol": "mixed", "enabled": true}
  ],
  "upstream_timeout": 10,
  "mixed_detect_timeout": 5,
  "max_connections": 1000
}
```

可选字段（省略时使用默认值）：
- `upstream_timeout`：上游连接超时秒数（默认 10）
- `mixed_detect_timeout`：混合模式首字节检测超时秒数（默认 5）
- `max_connections`：最大并发连接数（默认 1000）

protocol 可选值：`socks5`、`http`、`mixed`（自动检测）

## 关键设计决策

- **单实例保护**：监听 127.0.0.1:48321，失败则退出，防止重复启动
- **混合协议检测**：首字节 0x05 -> SOCKS5，ASCII 字母 -> HTTP，用 `mixedConn` 回放首字节
- **线程安全**：GUI 更新用 `fyne.Do()` 包裹，ProxyEngine 用 `sync.RWMutex` + `atomic.Int64` 计数器
- **连接限制**：`connSemaphore` 信号量限制并发连接数（默认 1000，可配置）
- **缓冲池**：`sync.Pool` 复用 32KB 缓冲区，减少 GC 压力
- **SOCKS5 RFC 合规**：先建立上游连接再回复成功状态码
- **配置验证**：`Config.Validate()` 校验地址格式、重复端口、协议值
- **关闭窗口不退出**：最小化到系统托盘，托盘菜单退出程序
- **无控制台窗口**：`-H windowsgui` 链接器标志

## 常见维护任务

| 任务 | 修改文件 |
|------|----------|
| 添加新端口默认值 | `main.go` cfg.Listeners |
| 修改上游代理默认地址 | `main.go` cfg.UpstreamProxy |
| 调整日志行数上限 | `gui/logview.go` maxLogLines |
| 修改窗口标题 | `gui/app.go` NewWindow 参数 |
| 调整 Dashboard 刷新频率 | `gui/dashboard.go` autoRefresh 的 time.Second |
| 修改单实例锁端口 | `main.go` lockAddr |
| 修改最大并发连接数 | `config.json` max_connections 或 `proxy/proxy.go` 默认值 |
| 修改上游连接超时 | `config.json` upstream_timeout |
| 修改协议检测超时 | `config.json` mixed_detect_timeout |
