# MoreLink Protocol (MLP) v1

MoreLink 两端（Android / Windows）通过本协议互通。本协议为 MoreLink 原创设计，与任何第三方项目无关。

## 1. 传输层

| 场景 | 方式 |
|---|---|
| 局域网发现 | UDP 广播，端口 `47891`，发送 JSON hello 包 |
| 局域网数据 | TCP，默认端口 `47892`（可配置） |
| 跨网络中继 | 可选中继服务器 TCP `47893`，房间码配对后双向转发字节流 |

所有多字节整数均为 **大端序（big-endian）**。

## 2. 帧格式

```
+--------+--------+--------+----------------+
| len(4) | type(1)| nonce(12) | payload(N)    |   加密帧（握手后）
+--------+--------+--------+----------------+
| len(4) | type(1)| payload(N)                |   明文帧（握手阶段）
+--------+--------+--------+----------------+
```

- `len` = 从 `type` 字节到帧尾的总字节数，不含自身 4 字节。
- 单帧上限 8 MiB。
- 握手（HELLO / PAIR_*）使用明文帧；配对成功后所有帧使用 AES-256-GCM 加密帧，`nonce` 为 12 字节随机值，每个帧独立随机。

## 3. 密钥派生（v2）

配对码 PIN 为 6 位数字。双方配对成功后：

```
key = SHA-256( "MoreLink-v2|pin:" + PIN )
```

**v2 变更说明**：v1 曾把两端设备 ID 混入密钥派生，但两个客户端对「对端 ID」的记录不一致（一端用真实 ID，一端用占位符），导致密钥永不匹配、所有加密消息无法解密。v2 改为**只由配对码派生**（带域名分隔前缀），两端必然得到相同密钥。设备 ID 仍在 HELLO 中交换，仅用于界面显示。

两端用同一 `key` 加解密。中继服务器不参与密钥交换，无法解密内容（端到端）。

## 4. 消息类型

| 值 | 名称 | 载荷 |
|---|---|---|
| `0x01` | HELLO | JSON: `{"v":1,"id":"...","name":"...","platform":"android\|windows","tcp":47892}` |
| `0x02` | PAIR_REQUEST | JSON: `{"pin":"123456","id":"...","name":"..."}` |
| `0x03` | PAIR_RESULT | JSON: `{"ok":true,"token":"..."}` |
| `0x10` | CLIP_TEXT | UTF-8 文本 |
| `0x11` | CLIP_IMAGE | 图片字节（PNG/JPEG），MIME 由扩展字段给出 |
| `0x20` | NOTIFY | JSON: `{"app":"...","title":"...","text":"...","ts":1699000000}` |
| `0x30` | FILE_OFFER | JSON: `{"id":"...","name":"...","size":123,"mime":"..."}` |
| `0x31` | FILE_CHUNK | 16 字节 transferId + 4 字节 offset + 数据 |
| `0x32` | FILE_ACK | JSON: `{"id":"...","offset":12345,"ok":true}` |
| `0x33` | FILE_DONE | JSON: `{"id":"...","sha256":"..."}` |
| `0x40` | PING | 空 |
| `0x41` | PONG | 空 |
| `0x50` | PEER_INFO | JSON: 对端信息 |

## 5. 配对流程

1. A 生成 6 位 PIN 并显示；用户在 B 上输入相同 PIN。
2. B 发送 `PAIR_REQUEST`（明文）。
3. A 校验成功则回 `PAIR_RESULT(ok=true)`，双方按第 3 节派生密钥并持久化 `deviceId`、`token`。
4. 之后每个 TCP 连接：双方先交换明文 HELLO 确认 `id` 与协议版本，随后所有帧加密。

## 6. 发现流程

- 每 3 秒向 `255.255.255.255:47891` 广播 HELLO（明文）。
- 收到 HELLO 的设备记录 `id/name/platform/ip/tcp`，在 UI 中列出。
- 用户选择某设备并发起连接；也支持手动输入 IP:端口。

## 7. 文件传输

1. 发送方发送 `FILE_OFFER`（含随机 16 字节 id、文件名、大小）。
2. 接收方回 `FILE_ACK(offset=0)`。
3. 发送方按 256 KiB 分块发送 `FILE_CHUNK`，每块携带累计 offset；接收方每收到若干块回 `FILE_ACK`。
4. 发送完毕后回 `FILE_DONE`（含整文件 SHA-256），接收方校验并落盘到「接收目录」。
5. 支持断点续传：接收方 ACK 的 offset 决定起点。

## 8. 剪贴板与通知

- 文本：`CLIP_TEXT`，UTF-8；接收方写入本地剪贴板。
- 图片：`CLIP_IMAGE`，接收方写入剪贴板/保存到图片目录。
- 通知：Android 端通过 `NotificationListenerService` 读取后发 `NOTIFY`；Windows 端收到后显示系统通知并写入消息列表。

## 9. 中继模式（跨网络）

- 自建中继运行 `morelink-relay -listen 47893`。
- 两端连接中继，先发送明文 `HELLO` 并携带 `room`（房间码）。
- 中继把同一房间内的两路连接互相**透明转发字节流**，不解析、不解密 MLP 帧。
- 房间人数为 2 时开始转发；任一方断开则另一方收到 EOF。
- 端到端加密仍由第 3 节密钥保证，中继只见密文。
