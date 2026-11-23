//go:build windows

package main

import (
	"fmt"
	"strings"

	"github.com/donghuaigu123/MoreLink/winui"
)

const (
	cmdRelayToggle = iota + 3000
	cmdRelayApply
	cmdRelayAutoStart
	cmdTrayRestore
	cmdTrayQuit
	cmdCopyAddr
	cmdCopyRoom
	cmdCopyPin
	cmdCopyAll
	cmdRegen
	cmdApplyHost
)

func runGUIProtected() {
	defer winui.RecoverPanic()
	winui.InitLog("relay.log")
	runGUI()
}

func runGUI() {
	w := winui.NewWindow("MoreLink · 中继服务器 v2.3", 820, 860)

	var info *winui.Control
	var logBox *winui.Control
	var addrEdit *winui.Control
	var hostEdit *winui.Control
	var roomLabel, pinLabel, addrLabel *winui.Control
	var autoChk *winui.Control
	var toggleBtn *winui.Control

	w.OnCreate = func() {
		winui.Logf("中继窗口控件创建开始")
		winui.AddTrayIcon(w.H, "MoreLink 中继服务器")

		// ---------- Connection info (the "just works" panel) ----------
		w.Label("连接信息（两端客户端照此填写，可直接复制）", 20, 88, 560, 30)
		winui.Line(w.H, 20, 122, 780, 2, winui.ClrAccent)

		w.Label("服务器地址:端口", 20, 136, 150, 26)
		addrLabel = w.Label("", 176, 134, 420, 30)
		w.ButtonID(cmdCopyAddr, "复制", 604, 130, 80, 34)
		w.ButtonID(cmdCopyAll, "复制全部", 690, 130, 110, 34)

		w.Label("房间码", 20, 176, 150, 26)
		roomLabel = w.Label("", 176, 174, 420, 30)
		w.ButtonID(cmdCopyRoom, "复制", 604, 170, 80, 34)
		w.ButtonID(cmdRegen, "重新生成", 690, 170, 110, 34)

		w.Label("配对码", 20, 216, 150, 26)
		pinLabel = w.Label("", 176, 214, 420, 30)
		w.ButtonID(cmdCopyPin, "复制", 604, 210, 80, 34)

		w.Label("对外地址(公网IP/域名，留空用本机IP):", 20, 256, 330, 26)
		hostEdit = w.Edit("", 356, 252, 330, 30)
		w.ButtonID(cmdApplyHost, "应用地址", 692, 250, 108, 34)
		w.Label("提示：跨网络时把上方地址改成公网IP或域名，并在路由器放行该 TCP 端口。", 20, 290, 780, 26)
		winui.Line(w.H, 20, 322, 780, 2, winui.ClrPanel2)

		// ---------- Server controls ----------
		w.Label("中继服务设置", 20, 334, 400, 28)
		w.Label("监听地址:", 20, 370, 90, 26)
		addrEdit = w.Edit(stListenAddr(), 116, 366, 300, 30)
		toggleBtn = w.ButtonID(cmdRelayToggle, "停止", 430, 364, 100, 34)
		if stRunning() {
			toggleBtn.SetText("停止")
		} else {
			toggleBtn.SetText("启动")
		}
		w.ButtonID(cmdRelayApply, "应用地址", 540, 364, 110, 34)

		autoChk = w.Checkbox("开机自动启动中继服务器", 20, 408, 360, 28)
		autoChk.SetChecked(winui.IsAutoStart("MoreLinkRelay"))

		info = w.Label("启动中…", 20, 448, 780, 30)
		w.Label("中继仅转发加密字节，无法读取内容。两端填相同的地址、房间码、6 位配对码即可互通。", 20, 480, 780, 26)
		w.Label("事件日志", 20, 514, 400, 26)
		logBox = w.MultiEdit(20, 542, 780, 290)
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
		case cmdCopyAddr:
			_, _, pub, _ := connInfoText()
			winui.SetClipboard(pub)
			addLine("已复制服务器地址: " + pub)
		case cmdCopyRoom:
			room, _, _, _ := connInfoText()
			winui.SetClipboard(room)
			addLine("已复制房间码: " + room)
		case cmdCopyPin:
			_, pin, _, _ := connInfoText()
			winui.SetClipboard(pin)
			addLine("已复制配对码: " + pin)
		case cmdCopyAll:
			room, pin, pub, ifaces := connInfoText()
			var sb strings.Builder
			sb.WriteString("MoreLink 中继连接信息\n")
			sb.WriteString("服务器地址:端口: " + pub + "\n")
			if len(ifaces) > 0 {
				sb.WriteString("本机局域网地址: " + strings.Join(ifaces, ", ") + "\n")
			}
			sb.WriteString("房间码: " + room + "\n")
			sb.WriteString("配对码: " + pin + "\n")
			sb.WriteString("两端客户端填入以上相同的地址/房间码/配对码即可连接（中继端口需防火墙放行）")
			winui.SetClipboard(sb.String())
			addLine("已复制全部连接信息")
		case cmdRegen:
			regenerateCodes()
		case cmdApplyHost:
			host := strings.TrimSpace(hostEdit.Text())
			pub := setPublicHost(host)
			addLine("对外地址已设为: " + pub)
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

		room, pin, pub, _ := connInfoText()
		if addrLabel != nil {
			addrLabel.SetText(pub)
		}
		if roomLabel != nil {
			roomLabel.SetText(room)
		}
		if pinLabel != nil {
			pinLabel.SetText(pin)
		}

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

var _ = fmt.Sprintf
