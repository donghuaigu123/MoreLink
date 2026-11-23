//go:build windows

// MoreLink Windows — native GUI desktop client.
// Pure Go + Win32, no console window, no browser, no external dependencies.
// Works on Windows 7 SP1 and later.
package main

import (
	"bufio"
	"crypto/sha256"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"github.com/donghuaigu123/MoreLink/internal/mlp"
	"github.com/donghuaigu123/MoreLink/winui"
)

// ---------- Win32 clipboard (works on Windows 7, unlike Set-Clipboard) ----------

var (
	k32           = syscall.NewLazyDLL("kernel32.dll")
	u32           = syscall.NewLazyDLL("user32.dll")
	pOpenClipboard    = u32.NewProc("OpenClipboard")
	pCloseClipboard   = u32.NewProc("CloseClipboard")
	pEmptyClipboard   = u32.NewProc("EmptyClipboard")
	pSetClipboardData = u32.NewProc("SetClipboardData")
	pGetClipboardData = u32.NewProc("GetClipboardData")
	pGlobalAlloc      = k32.NewProc("GlobalAlloc")
	pGlobalFree       = k32.NewProc("GlobalFree")
	pGlobalLock       = k32.NewProc("GlobalLock")
	pGlobalUnlock     = k32.NewProc("GlobalUnlock")
)

const (
	CF_UNICODETEXT = 13
	GMEM_MOVEABLE  = 0x0002
)

func setClipboardWin(text string) error {
	pOpenClipboard.Call(0)
	_, _, _ = pEmptyClipboard.Call()
	data, _ := syscall.UTF16FromString(text)
	size := uintptr(len(data)) * 2
	hMem, _, _ := pGlobalAlloc.Call(GMEM_MOVEABLE, size)
	if hMem == 0 {
		_, _, _ = pCloseClipboard.Call()
		return fmt.Errorf("GlobalAlloc failed")
	}
	ptr, _, _ := pGlobalLock.Call(hMem)
	copy((*[1 << 30]uint16)(unsafe.Pointer(ptr))[:len(data)], data)
	_, _, _ = pGlobalUnlock.Call(hMem)
	ret, _, err := pSetClipboardData.Call(CF_UNICODETEXT, hMem)
	_, _, _ = pCloseClipboard.Call()
	if ret == 0 {
		// Ownership did not transfer; we must free it ourselves.
		_, _, _ = pGlobalFree.Call(hMem)
		if err != nil && err != syscall.Errno(0) {
			return err
		}
		return fmt.Errorf("SetClipboardData failed")
	}
	// On success the system owns hMem — do NOT free it.
	return nil
}

func getClipboardWin() string {
	if r, _, _ := pOpenClipboard.Call(0); r == 0 {
		return ""
	}
	defer pCloseClipboard.Call()
	h, _, _ := pGetClipboardData.Call(CF_UNICODETEXT)
	if h == 0 {
		return ""
	}
	ptr, _, _ := pGlobalLock.Call(h)
	if ptr == 0 {
		return ""
	}
	n := 0
	max := 1 << 20
	arr := (*[1 << 20]uint16)(unsafe.Pointer(ptr))[:max:max]
	for n < max && arr[n] != 0 {
		n++
	}
	s := syscall.UTF16ToString(arr[:n])
	_, _, _ = pGlobalUnlock.Call(h)
	return s
}

// ---------- application state ----------

type peer struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Platform string `json:"platform"`
	IP       string `json:"ip"`
	Port     int    `json:"port"`
	LastSeen int64  `json:"lastSeen"`
}

type notifyItem struct {
	App   string `json:"app"`
	Title string `json:"title"`
	Text  string `json:"text"`
	TS    int64  `json:"ts"`
}

type transferItem struct {
	ID     string  `json:"id"`
	Name   string  `json:"name"`
	Size   int64   `json:"size"`
	Got    int64   `json:"got"`
	Dir    string  `json:"dir"`
	Status string  `json:"status"`
	Out    bool    `json:"out"`
	Pct    float64 `json:"pct"`
}

type config struct {
	Download  string `json:"download"`
	RelayAddr string `json:"relay_addr"` // host:port of relay server
	RelayRoom string `json:"relay_room"`
	RelayPin  string `json:"relay_pin"`  // pairing PIN used via relay
	AutoStart bool   `json:"autostart"`
}

func configPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, "MoreLink", "config.json")
}

func loadConfig() config {
	c := config{
		Download:  filepath.Join(homeDir(), "MoreLink", "Received"),
		RelayAddr: "",
		RelayRoom: "",
	}
	b, err := os.ReadFile(configPath())
	if err == nil {
		_ = json.Unmarshal(b, &c)
	}
	if c.Download == "" {
		c.Download = filepath.Join(homeDir(), "MoreLink", "Received")
	}
	_ = os.MkdirAll(c.Download, 0755)
	return c
}

func (c config) save() {
	b, _ := json.MarshalIndent(c, "", "  ")
	_ = os.WriteFile(configPath(), b, 0644)
}

func homeDir() string {
	h, _ := os.UserHomeDir()
	return h
}

type app struct {
	mu        sync.Mutex
	id        string
	name      string
	pin       string
	key       []byte
	paired    bool
	peers     map[string]*peer
	notifies  []notifyItem
	transfers []transferItem
	download  string
	status    string
	log       []string
	relayAddr string
	room      string
	cfg       config
	relayOn   bool
	relayStop chan struct{}
	relayConn net.Conn

	win       *winui.Window
	ctrlPin   *winui.Control
	ctrlPeers *winui.Control
	ctrlNotify *winui.Control
	ctrlLog   *winui.Control
	ctrlStatus *winui.Control
	ctrlID    *winui.Control
	ctrlRelayAddr *winui.Control
	ctrlRelayRoom *winui.Control
	ctrlRelayPin  *winui.Control
	ctrlRelayBtn  *winui.Control
	ctrlAutoStart *winui.Control
	ctrlDownload  *winui.Control
}

func newApp() *app {
	c := loadConfig()
	return &app{
		id:       mlp.MakeID(),
		name:     hostName(),
		peers:    map[string]*peer{},
		download: c.Download,
		cfg:      c,
		status:   "就绪",
	}
}

func hostName() string {
	if h, err := os.Hostname(); err == nil && h != "" {
		return h
	}
	return "Windows-PC"
}

func (a *app) logf(format string, args ...interface{}) {
	line := fmt.Sprintf("[%s] %s", time.Now().Format("15:04:05"), fmt.Sprintf(format, args...))
	a.mu.Lock()
	a.log = append(a.log, line)
	if len(a.log) > 200 {
		a.log = a.log[1:]
	}
	a.mu.Unlock()
}

func (a *app) setStatus(s string) {
	a.mu.Lock()
	a.status = s
	a.mu.Unlock()
}

func (a *app) hello() mlp.Hello {
	return mlp.Hello{V: 1, ID: a.id, Name: a.name, Platform: "windows", TCP: mlp.DataPort}
}

func (a *app) helloJSON() []byte {
	b, _ := json.Marshal(a.hello())
	return b
}

// ---------- pairing ----------

func (a *app) pair(pin, peerID string) error {
	if len(pin) != 6 {
		return fmt.Errorf("配对码必须是 6 位数字")
	}
	a.mu.Lock()
	a.pin = pin
	a.key = mlp.DeriveKey(pin, a.id, peerID)
	a.paired = true
	a.status = "已配对 · PIN " + pin
	a.mu.Unlock()
	a.logf("配对成功，PIN=%s，对端=%s", pin, peerID)
	return nil
}

// ---------- discovery ----------

func (a *app) discoveryLoop(stop <-chan struct{}) {
	ln, err := mlp.Announce(a.hello())
	if err != nil {
		a.logf("局域网发现失败: %v", err)
		return
	}
	defer ln.Close()
	go mlp.StartBeacon(a.hello(), stop)

	ch := make(chan mlp.Hello, 64)
	go mlp.ListenDiscovery(ln, ch, stop)
	for {
		select {
		case <-stop:
			return
		case h := <-ch:
			if h.ID == a.id {
				continue
			}
			a.mu.Lock()
			p := a.peers[h.ID]
			if p == nil {
				p = &peer{ID: h.ID, Name: h.Name, Platform: h.Platform, Port: h.TCP}
				a.peers[h.ID] = p
			}
			p.Name, p.Platform, p.Port = h.Name, h.Platform, h.TCP
			if h.IP != "" {
				p.IP = h.IP
			}
			p.LastSeen = time.Now().Unix()
			a.mu.Unlock()
		}
	}
}

// ---------- TCP server ----------

func (a *app) serveTCP(ln net.Listener) {
	for {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		go a.handleConn(c)
	}
}

func (a *app) handleConn(c net.Conn) {
	defer c.Close()
	_ = c.SetDeadline(time.Now().Add(30 * time.Second))
	r := bufio.NewReader(c)

	_ = mlp.WriteFrame(c, mlp.TypeHello, a.helloJSON(), nil)
	typ, payload, err := mlp.ReadFrame(r, nil)
	if err != nil || typ != mlp.TypeHello {
		return
	}
	var hello mlp.Hello
	if json.Unmarshal(payload, &hello) != nil || hello.ID == "" {
		return
	}

	a.mu.Lock()
	key := a.key
	a.peers[hello.ID] = &peer{
		ID: hello.ID, Name: hello.Name, Platform: hello.Platform,
		IP: strings.Split(c.RemoteAddr().String(), ":")[0],
		Port: hello.TCP, LastSeen: time.Now().Unix(),
	}
	a.mu.Unlock()

	if !a.paired || key == nil {
		typ, payload, err = mlp.ReadFrame(r, nil)
		if err != nil || typ != mlp.TypePairReq {
			return
		}
		var req mlp.PairReq
		if json.Unmarshal(payload, &req) != nil || len(req.PIN) != 6 {
			res, _ := json.Marshal(mlp.PairResult{OK: false})
			_ = mlp.WriteFrame(c, mlp.TypePairResult, res, nil)
			return
		}
		a.mu.Lock()
		a.pin = req.PIN
		a.key = mlp.DeriveKey(req.PIN, a.id, req.ID)
		key = a.key
		a.paired = true
		a.status = "已配对 · PIN " + req.PIN
		a.mu.Unlock()
		a.logf("手机发起配对，PIN=%s", req.PIN)
		res, _ := json.Marshal(mlp.PairResult{OK: true, Token: "ok"})
		_ = mlp.WriteFrame(c, mlp.TypePairResult, res, nil)
	}

	_ = c.SetDeadline(time.Time{})
	a.logf("设备 %s 已连接", hello.Name)
	for {
		t, p, err := mlp.ReadFrame(r, key)
		if err != nil {
			return
		}
		switch t {
		case mlp.TypePing:
			_ = mlp.WriteFrame(c, mlp.TypePong, nil, key)
		case mlp.TypeClipText:
			if err := setClipboardWin(string(p)); err == nil {
				a.setStatus("已接收剪贴板文本")
				a.logf("收到剪贴板文本 (%d 字符)", len(string(p)))
			}
		case mlp.TypeNotify:
			var n mlp.Notify
			if json.Unmarshal(p, &n) == nil {
				a.mu.Lock()
				a.notifies = append([]notifyItem{{n.App, n.Title, n.Text, n.TS}}, a.notifies...)
				if len(a.notifies) > 200 {
					a.notifies = a.notifies[:200]
				}
				a.mu.Unlock()
				a.logf("收到通知: %s — %s", n.Title, n.Text)
			}
		case mlp.TypeFileOffer:
			// FIX: handle file receive INLINE on the same reader to avoid the
			// data race caused by two bufio.Readers on one socket.
			a.receiveFile(c, r, p, key)
		}
	}
}

func (a *app) receiveFile(c net.Conn, r *bufio.Reader, offerBytes []byte, key []byte) {
	var of mlp.FileOffer
	if json.Unmarshal(offerBytes, &of) != nil {
		return
	}
	safe := filepath.Base(of.Name)
	dst := filepath.Join(a.download, safe)
	f, err := os.Create(dst)
	if err != nil {
		a.logf("文件接收失败: %v", err)
		return
	}
	defer f.Close()

	a.mu.Lock()
	a.transfers = append([]transferItem{{
		ID: of.ID, Name: of.Name, Size: of.Size, Dir: dst, Status: "接收中",
	}}, a.transfers...)
	idx := 0
	a.mu.Unlock()

	ack, _ := json.Marshal(mlp.FileAck{ID: of.ID, Offset: 0, OK: true})
	_ = mlp.WriteFrame(c, mlp.TypeFileAck, ack, key)

	var got int64
	var lastAck int64
	for got < of.Size {
		_ = c.SetReadDeadline(time.Now().Add(120 * time.Second))
		t, p, err := mlp.ReadFrame(r, key)
		if err != nil {
			a.logf("文件接收中断: %v", err)
			return
		}
		if t != mlp.TypeFileChunk || len(p) < 20 {
			continue
		}
		offset := int64(uint32(p[16])<<24 | uint32(p[17])<<16 | uint32(p[18])<<8 | uint32(p[19]))
		data := p[20:]
		if _, err := f.WriteAt(data, offset); err != nil {
			return
		}
		got = offset + int64(len(data))
		if got-lastAck > 1<<20 || got == of.Size {
			ackBytes, _ := json.Marshal(mlp.FileAck{ID: of.ID, Offset: got, OK: true})
			_ = mlp.WriteFrame(c, mlp.TypeFileAck, ackBytes, key)
			lastAck = got
			a.mu.Lock()
			if idx < len(a.transfers) {
				a.transfers[idx].Got = got
				if of.Size > 0 {
					a.transfers[idx].Pct = float64(got) / float64(of.Size) * 100
				}
			}
			a.mu.Unlock()
		}
	}
	h := sha256.New()
	if _, err := f.Seek(0, io.SeekStart); err == nil {
		_, _ = io.Copy(h, f)
	}
	done, _ := json.Marshal(mlp.FileDone{ID: of.ID, SHA256: fmt.Sprintf("%x", h.Sum(nil))})
	_ = mlp.WriteFrame(c, mlp.TypeFileDone, done, key)
	a.mu.Lock()
	if idx < len(a.transfers) {
		a.transfers[idx].Status = "已完成"
		a.transfers[idx].Pct = 100
	}
	a.status = "文件已接收: " + safe
	a.mu.Unlock()
	a.logf("文件接收完成: %s (%d 字节)", safe, got)
}

// ---------- outbound operations ----------

func (a *app) dialPeer(ip string, port int) (net.Conn, *bufio.Reader, error) {
	a.mu.Lock()
	key := a.key
	paired := a.paired
	a.mu.Unlock()
	if !paired || key == nil {
		return nil, nil, fmt.Errorf("请先配对")
	}
	conn, err := net.DialTimeout("tcp", fmt.Sprintf("%s:%d", ip, port), 6*time.Second)
	if err != nil {
		return nil, nil, err
	}
	_ = conn.SetDeadline(time.Now().Add(15 * time.Second))
	r := bufio.NewReader(conn)
	if err := mlp.WriteFrame(conn, mlp.TypeHello, a.helloJSON(), nil); err != nil {
		conn.Close()
		return nil, nil, err
	}
	t, _, err := mlp.ReadFrame(r, nil)
	if err != nil || t != mlp.TypeHello {
		conn.Close()
		return nil, nil, fmt.Errorf("握手失败")
	}
	return conn, r, nil
}

func (a *app) sendClipText(ip string, port int, text string) error {
	conn, _, err := a.dialPeer(ip, port)
	if err != nil {
		return err
	}
	defer conn.Close()
	a.mu.Lock()
	key := a.key
	a.mu.Unlock()
	if err := mlp.WriteFrame(conn, mlp.TypeClipText, []byte(text), key); err != nil {
		return err
	}
	a.logf("已发送剪贴板文本到 %s (%d 字符)", ip, len(text))
	return nil
}

func (a *app) sendFile(path, ip string, port int) error {
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	conn, r, err := a.dialPeer(ip, port)
	if err != nil {
		return err
	}
	defer conn.Close()
	a.mu.Lock()
	key := a.key
	a.mu.Unlock()

	id := mlp.MakeID()
	of, _ := json.Marshal(mlp.FileOffer{ID: id, Name: filepath.Base(path), Size: info.Size()})
	if err := mlp.WriteFrame(conn, mlp.TypeFileOffer, of, key); err != nil {
		return err
	}
	t, _, err := mlp.ReadFrame(r, key)
	if err != nil || t != mlp.TypeFileAck {
		return fmt.Errorf("对端未接受文件")
	}

	a.mu.Lock()
	a.transfers = append([]transferItem{{
		ID: id, Name: filepath.Base(path), Size: info.Size(), Status: "发送中", Out: true,
	}}, a.transfers...)
	idx := 0
	a.mu.Unlock()

	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	buf := make([]byte, 256<<10)
	var offset int64
	for {
		n, e := f.Read(buf)
		if n > 0 {
			chunk := make([]byte, 20+n)
			copy(chunk[:16], []byte(id))
			chunk[16] = byte(offset >> 24)
			chunk[17] = byte(offset >> 16)
			chunk[18] = byte(offset >> 8)
			chunk[19] = byte(offset)
			copy(chunk[20:], buf[:n])
			if we := mlp.WriteFrame(conn, mlp.TypeFileChunk, chunk, key); we != nil {
				return we
			}
			offset += int64(n)
			a.mu.Lock()
			if idx < len(a.transfers) {
				a.transfers[idx].Got = offset
				a.transfers[idx].Pct = float64(offset) / float64(info.Size()) * 100
			}
			a.mu.Unlock()
		}
		if e == io.EOF {
			break
		}
		if e != nil {
			return e
		}
	}
	_, _, _ = mlp.ReadFrame(r, key) // FILE_DONE
	a.mu.Lock()
	if idx < len(a.transfers) {
		a.transfers[idx].Status = "已完成"
		a.transfers[idx].Pct = 100
	}
	a.mu.Unlock()
	a.logf("文件发送完成: %s", filepath.Base(path))
	return nil
}

// Pair by actively connecting to a peer and sending a PAIR_REQ with our PIN.
func (a *app) pairWithPeer(ip string, port int, pin string) error {
	if len(pin) != 6 {
		return fmt.Errorf("配对码必须是 6 位数字")
	}
	conn, err := net.DialTimeout("tcp", fmt.Sprintf("%s:%d", ip, port), 6*time.Second)
	if err != nil {
		return err
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(10 * time.Second))
	r := bufio.NewReader(conn)
	_ = mlp.WriteFrame(conn, mlp.TypeHello, a.helloJSON(), nil)
	t, pl, err := mlp.ReadFrame(r, nil)
	if err != nil || t != mlp.TypeHello {
		return fmt.Errorf("握手失败")
	}
	var hello mlp.Hello
	_ = json.Unmarshal(pl, &hello)
	req, _ := json.Marshal(mlp.PairReq{PIN: pin, ID: a.id, Name: a.name})
	_ = mlp.WriteFrame(conn, mlp.TypePairReq, req, nil)
	t, _, err = mlp.ReadFrame(r, nil)
	if err != nil || t != mlp.TypePairResult {
		return fmt.Errorf("未收到配对结果")
	}
	return a.pair(pin, hello.ID)
}

// ---------- relay mode ----------

// relayLoop connects to the relay, joins a room, and serves the encrypted
// session transparently tunnelled through the relay. The PIN from the UI is
// used to derive the key so both peers agree even without direct discovery.
func (a *app) relayLoop(addr, room, pin string, stop <-chan struct{}) {
	// Ensure the session key is derived from the configured PIN.
	if len(pin) == 6 {
		a.mu.Lock()
		a.pin = pin
		a.key = mlp.DeriveKey(pin, "", "")
		a.paired = true
		a.mu.Unlock()
	}
	for {
		select {
		case <-stop:
			return
		default:
		}
		// If relay was turned off, stop the loop.
		a.mu.Lock()
		on := a.relayOn
		a.mu.Unlock()
		if !on {
			return
		}
		conn, err := net.DialTimeout("tcp", addr, 5*time.Second)
		if err != nil {
			a.setStatus("中继连接失败: " + err.Error())
			select {
			case <-stop:
				return
			case <-time.After(5 * time.Second):
			}
			continue
		}
		// Save the connection so toggleRelay can force-close it.
		a.mu.Lock()
		a.relayConn = conn
		a.mu.Unlock()
		h := a.hello()
		h.Room = room
		b, _ := json.Marshal(h)
		_ = mlp.WriteFrame(conn, mlp.TypeHello, b, nil)
		a.setStatus("已连接中继房间 " + room)
		a.logf("已连接中继 %s 房间 %s", addr, room)
		// Serve frames over the relayed connection. Since we are already paired,
		// handleConn skips PAIR_REQ and goes straight to the encrypted loop.
		a.handleConn(conn)
		select {
		case <-stop:
			return
		case <-time.After(3 * time.Second):
		}
	}
}

// toggleRelay starts or stops the relay session based on current UI config.
func (a *app) toggleRelay() string {
	a.mu.Lock()
	on := a.relayOn
	a.mu.Unlock()
	if on {
		// Stop: close the live connection so the blocked reader unblocks, and
		// close the stop channel so the reconnect loop exits immediately.
		a.mu.Lock()
		a.relayOn = false
		if a.relayConn != nil {
			_ = a.relayConn.Close()
			a.relayConn = nil
		}
		if a.relayStop != nil {
			close(a.relayStop)
			a.relayStop = nil
		}
		a.key = nil
		a.paired = false
		a.mu.Unlock()
		a.setStatus("中继已断开")
		a.logf("中继已断开")
		return "已断开中继"
	}
	addr := strings.TrimSpace(a.cfg.RelayAddr)
	room := strings.TrimSpace(a.cfg.RelayRoom)
	pin := strings.TrimSpace(a.cfg.RelayPin)
	if addr == "" || room == "" || len(pin) != 6 {
		return "请填写中继地址、房间码和 6 位配对码"
	}
	a.mu.Lock()
	a.relayOn = true
	stop := make(chan struct{})
	a.relayStop = stop
	a.mu.Unlock()
	go a.relayLoop(addr, room, pin, stop)
	a.logf("正在连接中继 %s 房间 %s", addr, room)
	return "正在连接中继…（对端也需连接同一房间）"
}

// ---------- GUI ----------

const (
	cmdGenPin = iota + 2000
	cmdPair
	cmdSendClip
	cmdSendFile
	cmdOpenFolder
	cmdClearNotify
	cmdRefreshPeers
	cmdRelayConnect
	cmdCopyPin
	cmdAutoStart
	cmdChooseDir
	cmdTrayRestore
	cmdTrayQuit
)

func (a *app) buildUI(w *winui.Window) {
	a.win = w
	winui.AddTrayIcon(w.H, "MoreLink 跨设备协同")

	// Row 1: identity + pairing
	w.Label("本机设备 ID:", 20, 84, 110, 24)
	a.ctrlID = w.Label(a.id, 130, 84, 330, 24)
	w.Label("配对码:", 470, 84, 60, 24)
	a.ctrlPin = w.Edit("", 534, 80, 110, 28)
	w.ButtonID(cmdGenPin, "🎲 生成", 652, 78, 80, 32)
	w.ButtonID(cmdCopyPin, "📋 复制", 738, 78, 80, 32)
	a.ctrlAutoStart = w.ButtonID(cmdAutoStart, "开机自启", 824, 78, 110, 32)

	// Relay config row
	w.Label("跨网络中继（无公网IP时）：", 20, 116, 210, 24)
	a.ctrlRelayAddr = w.Edit(a.cfg.RelayAddr, 230, 112, 250, 28)
	a.ctrlRelayRoom = w.Edit(a.cfg.RelayRoom, 488, 112, 120, 28)
	a.ctrlRelayPin = w.Edit(a.cfg.RelayPin, 616, 112, 110, 28)
	a.ctrlRelayBtn = w.ButtonID(cmdRelayConnect, "连接中继", 732, 110, 110, 32)
	w.Label("服务器地址:端口      房间码       配对码", 230, 140, 500, 20)

	// Download directory row
	w.Label("接收目录:", 20, 164, 80, 24)
	a.ctrlDownload = w.Label(a.download, 100, 164, 640, 24)
	w.ButtonID(cmdChooseDir, "📁 更改目录", 750, 160, 184, 30)

	// Peers list
	w.Label("附近设备 / 已配对设备（点击选择）", 20, 196, 500, 24)
	a.ctrlPeers = w.ListBox(20, 222, 560, 180)
	w.ButtonID(cmdPair, "配对到选中设备", 600, 226, 320, 36)

	// Status
	w.Label("状态:", 20, 412, 50, 24)
	a.ctrlStatus = w.Label("就绪", 74, 412, 810, 24)

	// Actions
	w.Label("操作（先选中设备）", 20, 442, 400, 24)
	w.ButtonID(cmdSendClip, "📄 发送剪贴板文本", 20, 470, 200, 38)
	w.ButtonID(cmdSendFile, "📎 发送文件", 230, 470, 180, 38)
	w.ButtonID(cmdOpenFolder, "📂 打开接收目录", 420, 470, 200, 38)
	w.ButtonID(cmdClearNotify, "🧹 清空通知", 630, 470, 160, 38)
	w.ButtonID(cmdRefreshPeers, "🔄 重扫设备", 800, 470, 134, 38)

	// Notifications
	w.Label("手机通知（配对后自动同步）", 20, 520, 500, 24)
	a.ctrlNotify = w.ListBox(20, 546, 560, 180)

	// Log
	w.Label("事件日志", 600, 520, 200, 24)
	a.ctrlLog = w.MultiEdit(600, 546, 334, 180)

	w.OnCommand = a.onCommand
	w.OnTimer = a.refreshUI
	w.OnClose = func() bool {
		winui.HideWindow(w.H)
		winui.UpdateTrayIcon(w.H, "MoreLink（后台运行中）")
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
				{ID: cmdTrayQuit, Text: "退出 MoreLink"},
			})
			switch sel {
			case cmdTrayRestore:
				winui.ShowWindow(w.H)
			case cmdTrayQuit:
				winui.RemoveTrayIcon(w.H)
				w.Quit()
			}
		}
	}
}

func (a *app) refreshUI() {
	if a.ctrlPeers == nil {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()

	// Peers
	selected := a.ctrlPeers.ListCurSel()
	selectedID := ""
	if selected >= 0 && selected < a.ctrlPeers.ListCount() {
		selectedID = a.ctrlPeers.ListText(selected)
	}
	peers := make([]*peer, 0, len(a.peers))
	for _, p := range a.peers {
		peers = append(peers, p)
	}
	sort.Slice(peers, func(i, j int) bool { return peers[i].LastSeen > peers[j].LastSeen })
	a.ctrlPeers.ListClear()
	peerLines := []string{}
	for _, p := range peers {
		line := fmt.Sprintf("%s | %s | %s:%d | %s", p.Name, p.Platform, p.IP, p.Port, p.ID)
		peerLines = append(peerLines, line)
		a.ctrlPeers.ListAdd(line)
	}
	if selectedID != "" {
		for i, l := range peerLines {
			if l == selectedID {
				a.ctrlPeers.ListSetCurSel(i)
				break
			}
		}
	}

	// Notifications
	a.ctrlNotify.ListClear()
	for _, n := range a.notifies {
		a.ctrlNotify.ListAdd(fmt.Sprintf("[%s] %s: %s — %s",
			time.Unix(n.TS, 0).Format("01-02 15:04"), n.App, n.Title, n.Text))
	}

	// Status
	a.ctrlStatus.SetText(a.status)
	a.ctrlID.SetText(a.id)
}

func (a *app) selectedPeer() *peer {
	a.mu.Lock()
	defer a.mu.Unlock()
	w := a.win
	_ = w
	return nil
}

func (a *app) getSelectedPeer() (*peer, bool) {
	i := a.ctrlPeers.ListCurSel()
	if i < 0 {
		return nil, false
	}
	line := a.ctrlPeers.ListText(i)
	parts := strings.Split(line, " | ")
	if len(parts) < 3 {
		return nil, false
	}
	ipPort := parts[2]
	ps := strings.Split(ipPort, ":")
	if len(ps) < 2 {
		return nil, false
	}
	port := mlp.DataPort
	fmt.Sscanf(ps[len(ps)-1], "%d", &port)
	var id string
	if len(parts) >= 4 {
		id = parts[3]
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if p, ok := a.peers[id]; ok {
		return p, true
	}
	for _, p := range a.peers {
		if p.IP == strings.Join(ps[:len(ps)-1], ":") {
			return p, true
		}
	}
	return &peer{IP: strings.Join(ps[:len(ps)-1], ":"), Port: port}, true
}

func (a *app) onCommand(id uintptr) {
	switch id {
	case cmdGenPin:
		pin := mlp.MakePIN()
		a.ctrlPin.SetText(pin)
		a.mu.Lock()
		a.pin = pin
		a.mu.Unlock()
		a.logf("生成配对码 %s（请在手机端输入相同码）", pin)
		winui.MessageBox(a.win.H, "配对码已生成："+pin+"\n\n请在手机端输入相同配对码，然后点击“配对到选中设备”。",
			"MoreLink 配对码", 0x40)

	case cmdCopyPin:
		pin := a.ctrlPin.Text()
		if len(pin) == 6 {
			_ = setClipboardWin(pin)
			a.logf("已复制配对码到剪贴板")
		}

	case cmdPair:
		p, ok := a.getSelectedPeer()
		if !ok {
			winui.MessageBox(a.win.H, "请先在列表中选择一个设备。", "提示", 0x30)
			return
		}
		pin := a.ctrlPin.Text()
		if len(pin) != 6 {
			winui.MessageBox(a.win.H, "请先输入或生成 6 位配对码。", "提示", 0x30)
			return
		}
		go func() {
			if err := a.pairWithPeer(p.IP, p.Port, pin); err != nil {
				a.logf("配对失败: %v", err)
				a.setStatus("配对失败: " + err.Error())
			} else {
				a.setStatus("配对成功")
			}
		}()

	case cmdSendClip:
		p, ok := a.getSelectedPeer()
		if !ok {
			winui.MessageBox(a.win.H, "请先选择设备。", "提示", 0x30)
			return
		}
		text := getClipboardWin()
		if text == "" {
			winui.MessageBox(a.win.H, "剪贴板为空，请先复制一段文本。", "提示", 0x30)
			return
		}
		go func() {
			if err := a.sendClipText(p.IP, p.Port, text); err != nil {
				a.logf("发送剪贴板失败: %v", err)
				a.setStatus("发送失败: " + err.Error())
			}
		}()

	case cmdSendFile:
		p, ok := a.getSelectedPeer()
		if !ok {
			winui.MessageBox(a.win.H, "请先选择设备。", "提示", 0x30)
			return
		}
		path := winui.OpenFileDialog(a.win.H, "选择要发送的文件", "所有文件 (*.*)\x00*.*\x00")
		if path == "" {
			return
		}
		go func() {
			if err := a.sendFile(path, p.IP, p.Port); err != nil {
				a.logf("发送文件失败: %v", err)
				a.setStatus("发送失败: " + err.Error())
			}
		}()

	case cmdOpenFolder:
		_ = os.MkdirAll(a.download, 0755)
		winui.ShellOpen(a.download)

	case cmdClearNotify:
		a.mu.Lock()
		a.notifies = nil
		a.mu.Unlock()

	case cmdRefreshPeers:
		a.logf("重新扫描局域网设备…")

	case cmdRelayConnect:
		// Persist relay config from UI.
		a.cfg.RelayAddr = strings.TrimSpace(a.ctrlRelayAddr.Text())
		a.cfg.RelayRoom = strings.TrimSpace(a.ctrlRelayRoom.Text())
		a.cfg.RelayPin = strings.TrimSpace(a.ctrlRelayPin.Text())
		a.cfg.save()
		msg := a.toggleRelay()
		a.ctrlRelayBtn.SetText(map[bool]string{true: "断开中继", false: "连接中继"}[a.isRelayOn()])
		winui.MessageBox(a.win.H, msg, "跨网络中继", winui.MB_ICONINFORMATION)

	case cmdAutoStart:
		on := a.ctrlAutoStart.Checked()
		if err := winui.SetAutoStart("MoreLink", on); err != nil {
			a.logf("设置自启动失败: %v", err)
		}
		a.cfg.AutoStart = on
		a.cfg.save()
		if on {
			a.logf("已开启开机自启动")
		} else {
			a.logf("已关闭开机自启动")
		}

	case cmdChooseDir:
		dir := winui.SelectFolder(a.win.H, "选择文件接收目录")
		if dir == "" {
			return
		}
		if err := os.MkdirAll(dir, 0755); err != nil {
			winui.MessageBox(a.win.H, "目录不可写："+err.Error(), "提示", winui.MB_ICONERROR)
			return
		}
		a.mu.Lock()
		a.download = dir
		a.cfg.Download = dir
		a.mu.Unlock()
		a.cfg.save()
		a.ctrlDownload.SetText(dir)
		a.logf("接收目录已改为: %s", dir)
	}
}

func (a *app) isRelayOn() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.relayOn
}

func main() {
	defer winui.RecoverPanic()
	winui.InitLog("client.log")

	dataPort := flag.Int("port", mlp.DataPort, "TCP 端口")
	relayAddr := flag.String("relay", "", "中继地址 host:port（可选）")
	room := flag.String("room", "", "中继房间码")
	flag.Parse()

	a := newApp()
	a.relayAddr = *relayAddr
	a.room = *room
	a.logf("MoreLink 启动 · 设备 ID %s", a.id)

	ln, err := net.Listen("tcp", fmt.Sprintf("0.0.0.0:%d", *dataPort))
	if err != nil {
		winui.MessageBox(0, "端口 "+fmt.Sprint(*dataPort)+" 被占用:\n"+err.Error(), "MoreLink 错误", 0x10)
		return
	}
	go a.serveTCP(ln)
	stop := make(chan struct{})
	go a.discoveryLoop(stop)
	if *relayAddr != "" && *room != "" {
		// Command-line relay mode (legacy): PIN falls back to config.
		a.cfg.RelayAddr = *relayAddr
		a.cfg.RelayRoom = *room
		a.relayOn = true
		go a.relayLoop(*relayAddr, *room, a.cfg.RelayPin, stop)
	}

	winui.Logf("启动窗口 960x750，准备注册 Win32 窗口类")
	w := winui.NewWindow("MoreLink · 跨设备协同中心 v2.1", 960, 750)
	w.OnCreate = func() { a.buildUI(w) }
	winui.Logf("进入消息循环")
	err = w.Create()
	if err != nil {
		winui.Logf("窗口创建失败: %v", err)
	}
	winui.Logf("消息循环结束，程序退出")
}
