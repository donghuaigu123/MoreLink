# MoreLink

**跨设备协同 · 局域网 / 远程 · 端到端加密 · 100% 原创开源**

> **v2.2.0 中继与易用性重大增强**
> - ✅ **修复中继不可用的核心 Bug**：原实现两端各自启动双向 pump，4 条管道操作 2 个连接导致数据混乱。改为「每房间仅一方桥接」，并用真实中继 + 两端客户端完成握手/加密剪贴板/96KB 文件传输全链路验证。
> - ✅ **两端新增中继配置界面**：Windows 与 Android 均可填写「服务器地址:端口 / 房间码 / 配对码」并一键连接/断开；Android 连接时自动进入中继长连接会话。
> - ✅ **文件保存目录可选**：Windows 端可点「📁 更改目录」选择任意接收目录（持久化到 config.json）；Android 端显示接收目录路径。
> - ✅ **开机自启动**：Windows 客户端与中继服务器均有「开机自启」复选框（写当前用户注册表 Run 项，无需管理员）。
> - ✅ **最小化到系统托盘**：两端 Windows 程序关闭窗口时隐藏到托盘，双击托盘图标恢复，右键菜单可显示/退出，后台继续运行。
>
> **v2.1.0 关键修复（Windows 程序打不开问题）**
> - ✅ **修复致命崩溃**：`SetBkMode`/`SetTextColor`/`SetBkColor` 是 GDI 函数却被错误地从 `user32.dll` 加载，导致窗口一绘制（WM_PAINT）就 panic，GUI 程序无控制台 → 静默退出，表现为"打不开/双击没反应"。现已改为 `gdi32.dll`。
> - ✅ **修复空白窗口**：`WM_CREATE` 回调创建控件时窗口句柄 `w.H` 尚未赋值（仍为 0），导致全部子控件创建失败、窗口空白。现改为在消息处理中用回调传入的 `hwnd` 先赋值再建控件。
> - ✅ **新增崩溃日志与弹窗**：任何 panic 不再静默退出，会写入 `~/MoreLink/client.log` 或 `relay.log` 并弹出错误框。
> - ✅ 已用 Wine + Xvfb 真实验证：Windows 客户端与中继窗口均正常渲染，按钮点击→配对码弹窗交互链路测试通过。
>
> **v2.0.0 重大更新（彻底重写）**
> - ✅ Windows 端改为**原生 Win32 可视化窗口**（不再是黑色控制台，无需浏览器，兼容 Windows 7）
> - ✅ 中继服务器也提供**原生 GUI 状态窗口**
> - ✅ **修复致命 Bug**：配对密钥两端不一致（v1 永远无法真正通信），v2 密钥只由配对码派生，两端必然一致
> - ✅ **修复文件传输**：v1 用两个 reader 并发读同一 socket 导致数据错乱，v2 改为串行处理
> - ✅ **补全功能**：Android 端新增「发送文件到电脑」，Windows 端用 Win32 API 剪贴板（不依赖 PowerShell 5，Win7 可用）
> - ✅ Android UI 全面重设计为高对比度深色主题

MoreLink 是一个由本人从零编写的开源项目，让 Android 手机与 Windows 电脑像一台设备一样协同工作。它参考了同类软件的**功能理念**（文件互传、剪贴板共享、通知同步），但**没有复制任何第三方项目的代码**。所有协议、加密、客户端均为原创实现。

> ⚠️ 本项目名称 MoreLink 与 logo 均为原创，与任何其他项目无关联。

## 功能

| 功能 | Android | Windows | 说明 |
|---|:---:|:---:|---|
| 局域网自动发现 | ✅ | ✅ | UDP 广播，无需配置 |
| 端到端加密 | ✅ | ✅ | AES-256-GCM，密钥由配对码派生 |
| 配对 | ✅ | ✅ | 6 位配对码 |
| 剪贴板文本互传 | ✅ | ✅ | 一端复制，另一端粘贴 |
| 文件互传 | ✅ | ✅ | 256 KiB 分块，SHA-256 校验 |
| 通知同步 | ✅ 发送 | ✅ 接收 | 手机通知实时显示在电脑 |
| 远程模式 | ✅ | ✅ | 通过自建中继服务器跨网络连接 |

## 架构

```
Android App (Java)          Windows App (Go)
      │                           │
      └──── MoreLink Protocol ────┘
             v1 (原创)
   明文握手 → 6 位 PIN 配对 → AES-256-GCM 加密帧
              │
      ┌───────┴───────┐
   局域网直连      中继服务器 (Go)
   UDP 发现 + TCP   TCP 房间码转发
```

- **协议规范**：[`protocol/PROTOCOL.md`](protocol/PROTOCOL.md)
- **共享 Go 协议库**：[`internal/mlp/`](internal/mlp/)（含单元测试）
- **Windows 客户端**：[`windows/`](windows/) — Go 编写，原生 Win32 GUI 窗口（GUI 组件在 [`winui/`](winui/)）
- **Android 客户端**：[`android/`](android/) — Java，纯 Android Framework API
- **中继服务器**：[`relay/`](relay/) — Go，用于跨网络

## 下载 / 构建产物

**请从 [Releases 页面](https://github.com/donghuaigu123/MoreLink/releases) 下载最新构建产物**（二进制不存入 git 仓库，以减小仓库体积）：

- 最新稳定版：[v2.2.0](https://github.com/donghuaigu123/MoreLink/releases/tag/v2.2.0)

各版本包含：

- `MoreLink-Windows.exe` — Windows x64 客户端（单文件，无需安装）
- `MoreLink-Android.apk` — Android 客户端（minSdk 26 / Android 8.0+）
- `morelink-relay.exe` — Windows 版中继服务器
- `morelink-relay-linux` — Linux 版中继服务器

## 快速开始

### 1. 局域网使用

1. 两端安装 MoreLink，确保在同一 Wi-Fi / 局域网。
2. **Windows**：双击 `MoreLink-Windows.exe`，会直接打开原生窗口（不再是黑框）。
3. **Android**：打开 App，点「启动 / 保持后台运行」，并授予通知读取权限。
4. **配对**：
   - 在 Windows 窗口点「🎲 生成」得到 6 位配对码；
   - 在 Android 端输入相同配对码，填入电脑 IP，点「配对到电脑」；
   - 也可在 Android 生成配对码，Windows 选中设备后点「配对到选中设备」。
5. 配对完成后，「附近设备」列表会显示对端，点击即可选中。

### 2. 发送剪贴板文本

- **手机 → 电脑**：App 中填电脑 IP 和文本，点「发送文本」。
- **电脑 → 手机**：先在设备列表选中手机，复制好文本后点 Windows 窗口的「📄 发送剪贴板文本」。

### 3. 发送文件

- **电脑 → 手机**：在设备列表选中手机，点「📎 发送文件」，在文件对话框中选择文件。
  - 手机接收目录：`Android/data/com.morelink.app/files/Received/`
- **手机 → 电脑**：App 中点「选择文件发送到电脑」；接收目录为 Windows 用户目录下的 `MoreLink/Received/`（可用「📂 打开接收目录」直接打开）。

### 4. 通知同步

1. Android 端点「开启通知读取权限」，在系统设置中授予 MoreLink 通知使用权。
2. 配对后，手机收到的通知会实时显示在 Windows 窗口的「手机通知」列表区。

### 5. 故障排查

程序遇到任何错误会写入日志：`%USERPROFILE%\MoreLink\client.log`（客户端）、`relay.log`（中继），并弹出错误提示框。若窗口空白或按钮无响应，请先查看该日志。

### 6. 跨网络远程（中继模式）

两端的**图形界面**里都能直接配置中继，无需命令行：

1. **架设中继**：在有公网 IP 的服务器上运行 `morelink-relay.exe`（Windows，可视化窗口）或 `morelink-relay-linux`（Linux）。在 Windows 中继窗口里可修改「监听地址」并点「应用地址」/「启动」「停止」，勾选「开机自动启动」可让中继随系统启动；关闭窗口会最小化到系统托盘继续运行。
2. **Windows 客户端**：在窗口「跨网络中继」一行填入 `服务器地址:端口`（如 `1.2.3.4:47893`）、`房间码`、`6 位配对码`，点「连接中继」；断开点同一按钮。
3. **Android 客户端**：在 App 的「⑤ 跨网络中继」区填入服务器地址、端口、房间码、配对码，点「连接中继」；断开点「断开中继」。
4. **注意事项**：两端必须使用**相同的房间码和相同的 6 位配对码**；中继只做透明字节转发，无法解密内容（端到端密钥由配对码派生，不经过中继）。在防火墙放行中继端口。

> 命令行方式仍然保留（兼容旧用法）：`MoreLink-Windows.exe -relay 1.2.3.4:47893 -room myroom123`，但配对码需通过界面配置。

## 从源码构建

### Windows / 中继 / 协议测试（Go 1.21+）

```bash
go test ./...                                             # 运行协议一致性测试
CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -o dist/MoreLink-Windows.exe ./windows
CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -o dist/morelink-relay.exe ./relay
```

### Android APK（JDK 17 + Android SDK 34）

```bash
cd android
./gradlew assembleDebug
# 产物: app/build/outputs/apk/debug/app-debug.apk
```

## 安全说明

- 配对码是唯一共享密钥材料，建议使用随机 6 位数并仅在可信设备间交换。
- 中继服务器只转发密文，但知道房间码的人可以尝试连接；请为房间码设置足够长度和随机性。
- 本软件定位为个人/小团队工具，未经专业安全审计，请勿用于高敏感场景。

## 许可证

本项目采用 **MIT License** — 详见 [`LICENSE`](LICENSE)。

> 本项目为原创作品，仅参考了同类软件的功能列表，未使用其任何源代码、商标或素材。

## 致谢

感谢所有开源社区让跨设备协同成为可能。MoreLink 是独立实现，与任何商业或开源项目无隶属关系。
