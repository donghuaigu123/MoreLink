//go:build windows

package main

import (
	"fmt"
	"strings"

	"github.com/donghuaigu123/MoreLink/winui"
)

func runGUI() {
	w := winui.NewWindow("MoreLink · 中继服务器 v2", 760, 560)

	var info *winui.Control
	var logBox *winui.Control

	w.OnCreate = func() {
		w.Label("跨网络中继服务", 20, 90, 400, 30)
		info = w.Label("启动中…", 20, 126, 720, 30)
		w.Label("提示：两端客户端使用相同房间码连接本服务器的地址:端口即可跨网络互通。", 20, 166, 720, 28)
		w.Label("中继仅转发加密数据，无法读取内容。请在防火墙放行 TCP 端口。", 20, 196, 720, 28)
		w.Label("事件日志", 20, 236, 400, 26)
		logBox = w.MultiEdit(20, 264, 720, 260)
	}

	w.OnTimer = func() {
		if info == nil || logBox == nil {
			return
		}
		st.mu.Lock()
		clients, bridges, addr := st.clients, st.bridges, st.listenAddr
		st.mu.Unlock()
		info.SetText(fmt.Sprintf("监听: %s   在线连接: %d   活跃桥接: %d", addr, clients, bridges))

		st.mu.Lock()
		lines := append([]string(nil), st.lines...)
		st.mu.Unlock()
		want := strings.Join(lines, "\n")
		if want != logBox.Text() {
			logBox.SetText(want)
		}
	}

	_ = w.Create()
}
