//go:build windows

package main

import (
	"strings"

	"github.com/donghuaigu123/MoreLink/winui"
)

const (
	cmdRelayToggle = iota + 3000
	cmdRelayApply
	cmdRelayAutoStart
	cmdTrayRestore
	cmdTrayQuit
)

func runGUIProtected() {
	defer winui.RecoverPanic()
	winui.InitLog("relay.log")
	runGUI()
}

func runGUI() {
	w := winui.NewWindow("MoreLink · 中继服务器 v2.1", 780, 620)

	var info *winui.Control
	var logBox *winui.Control
	var addrEdit *winui.Control
	var autoChk *winui.Control
	var toggleBtn *winui.Control

	w.OnCreate = func() {
		winui.Logf("中继窗口控件创建开始")
		winui.AddTrayIcon(w.H, "MoreLink 中继服务器")

		w.Label("跨网络中继服务", 20, 88, 400, 28)
		w.Label("监听地址:", 20, 124, 90, 26)
		addrEdit = w.Edit(stListenAddr(), 116, 120, 300, 30)
		toggleBtn = w.ButtonID(cmdRelayToggle, "停止", 430, 118, 100, 34)
		if stRunning() {
			toggleBtn.SetText("停止")
		} else {
			toggleBtn.SetText("启动")
		}
		w.ButtonID(cmdRelayApply, "应用地址", 540, 118, 110, 34)

		autoChk = w.Checkbox("开机自动启动中继服务器", 20, 162, 340, 28)
		autoChk.SetChecked(winui.IsAutoStart("MoreLinkRelay"))

		info = w.Label("启动中…", 20, 202, 720, 30)
		w.Label("提示：两端客户端填入本服务器地址、相同房间码和配对码即可跨网络互通。", 20, 236, 740, 28)
		w.Label("中继仅转发加密字节，无法读取内容。请在防火墙放行对应 TCP 端口。", 20, 266, 740, 28)
		w.Label("事件日志", 20, 306, 400, 26)
		logBox = w.MultiEdit(20, 334, 740, 250)
		winui.Logf("中继窗口控件创建完成")
	}

	w.OnCommand = func(id uintptr) {
		switch id {
		case cmdRelayApply:
			addr := strings.TrimSpace(addrEdit.Text())
			if addr == "" {
				addr = ":47893"
			}
			if err := startServer(addr); err != nil {
				winui.MessageBox(w.H, "启动失败："+err.Error(), "MoreLink", winui.MB_ICONERROR)
			} else {
				toggleBtn.SetText("停止")
				winui.Logf("监听地址已切换为 %s", addr)
			}
		case cmdRelayToggle:
			if stRunning() {
				stopServer()
				toggleBtn.SetText("启动")
			} else {
				addr := strings.TrimSpace(addrEdit.Text())
				if addr == "" {
					addr = ":47893"
				}
				if err := startServer(addr); err != nil {
					winui.MessageBox(w.H, "启动失败："+err.Error(), "MoreLink", winui.MB_ICONERROR)
				} else {
					toggleBtn.SetText("停止")
				}
			}
		case cmdRelayAutoStart:
			on := autoChk.Checked()
			if err := winui.SetAutoStart("MoreLinkRelay", on); err != nil {
				winui.Logf("设置自启动失败: %v", err)
			} else if on {
				addLine("已开启开机自启动")
			} else {
				addLine("已关闭开机自启动")
			}
		}
	}

	w.OnTimer = func() {
		if info == nil || logBox == nil {
			return
		}
		st.mu.Lock()
		clients, bridges, addr, running := st.clients, st.bridges, st.listenAddr, st.running
		st.mu.Unlock()
		state := "运行中"
		if !running {
			state = "已停止"
		}
		info.SetText("状态: " + state + "   监听: " + addr + "   在线: " + itoa(clients) + "   桥接: " + itoa(bridges))

		st.mu.Lock()
		lines := append([]string(nil), st.lines...)
		st.mu.Unlock()
		want := strings.Join(lines, "\n")
		if want != logBox.Text() {
			logBox.SetText(want)
		}
		winui.UpdateTrayIcon(w.H, "MoreLink 中继:"+state+" 在线"+itoa(clients))
	}

	// Close hides to tray; double-click restores; right-click shows menu.
	w.OnClose = func() bool {
		winui.HideWindow(w.H)
		winui.UpdateTrayIcon(w.H, "MoreLink 中继（后台运行中）")
		return true
	}
	w.OnTray = func(msg uintptr) {
		switch msg {
		case 0x0203: // WM_LBUTTONDBLCLK
			winui.ShowWindow(w.H)
		case 0x0205: // WM_RBUTTONUP
			sel := winui.TrackPopupMenu(w.H, []winui.TrayMenuItem{
				{ID: cmdTrayRestore, Text: "显示窗口"},
				{Sep: true},
				{ID: cmdTrayQuit, Text: "退出中继服务器"},
			})
			switch sel {
			case cmdTrayRestore:
				winui.ShowWindow(w.H)
			case cmdTrayQuit:
				winui.RemoveTrayIcon(w.H)
				stopServer()
				w.Quit()
			}
		}
	}

	winui.Logf("中继进入消息循环")
	err := w.Create()
	if err != nil {
		winui.Logf("中继窗口创建失败: %v", err)
	}
}

func stRunning() bool {
	st.mu.Lock()
	defer st.mu.Unlock()
	return st.running
}

func stListenAddr() string {
	st.mu.Lock()
	defer st.mu.Unlock()
	if st.listenAddr == "" {
		return ":47893"
	}
	return st.listenAddr
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}
