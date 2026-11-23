// MoreLink Windows — desktop client.
// Provides a local Web UI on 127.0.0.1:47890 and speaks MLP v1.
package main

import (
	"bufio"
	"crypto/sha256"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/donghuaigu123/MoreLink/internal/mlp"
)

type peer struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Platform string `json:"platform"`
	IP       string `json:"ip"`
	Port     int    `json:"port"`
	Room     string `json:"room"`
	Online   bool   `json:"online"`
	LastSeen int64  `json:"lastSeen"`
}

type notifyItem struct {
	App   string `json:"app"`
	Title string `json:"title"`
	Text  string `json:"text"`
	TS    int64  `json:"ts"`
}

type transferItem struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Size   int64  `json:"size"`
	Dir    string `json:"dir"`
	Status string `json:"status"`
}

type app struct {
	mu       sync.Mutex
	id       string
	name     string
	key      []byte
	paired   bool
	pin      string
	peers    map[string]*peer
	notifies []notifyItem
	transfers []transferItem
	download string
	status   string
}

func newApp() *app {
	home, _ := os.UserHomeDir()
	dl := filepath.Join(home, "MoreLink", "Received")
	_ = os.MkdirAll(dl, 0755)
	return &app{
		id:       mlp.MakeID(),
		name:     hostName(),
		peers:    map[string]*peer{},
		download: dl,
		status:   "就绪",
	}
}

func hostName() string {
	if h, err := os.Hostname(); err == nil && h != "" {
		return h
	}
	return "Windows"
}

func (a *app) self() mlp.Hello {
	return mlp.Hello{V: 1, ID: a.id, Name: a.name, Platform: "windows", TCP: mlp.DataPort}
}

func (a *app) helloJSON() []byte {
	b, _ := json.Marshal(a.self())
	return b
}

// pairFromUI is called by the Web UI when the user enters a 6-digit PIN
// displayed on the phone. It returns the derived key.
func (a *app) pairFromUI(pin, peerID string) ([]byte, error) {
	if len(pin) != 6 {
		return nil, fmt.Errorf("PIN 必须为 6 位数字")
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.pin = pin
	a.key = mlp.DeriveKey(pin, a.id, peerID)
	a.paired = true
	a.status = "已配对"
	return a.key, nil
}

// discoveryLoop broadcasts and collects LAN peers.
func (a *app) discoveryLoop(stop <-chan struct{}) {
	listen, err := mlp.Announce(a.self())
	if err != nil {
		log.Printf("discovery: %v", err)
		return
	}
	defer listen.Close()
	go mlp.StartBeacon(a.self(), stop)

	ch := make(chan mlp.Hello, 32)
	go mlp.ListenDiscovery(listen, ch, stop)
	for {
		select {
		case <-stop:
			return
		case h := <-ch:
			if h.ID == a.id {
				continue
			}
			// Sender IP is not carried in Hello; resolve from unicast source
			// by performing a direct probe. For LAN we store IP from the
			// discovery socket via a parallel map filled in listenLAN.
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
			p.Online = true
			a.mu.Unlock()
		}
	}
}

// serveTCP accepts MLP connections from the phone or relay.
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

	// Handshake: plaintext HELLO both ways.
	mlp.WriteFrame(c, mlp.TypeHello, a.helloJSON(), nil)
	typ, payload, err := mlp.ReadFrame(r, nil)
	if err != nil || typ != mlp.TypeHello {
		return
	}
	var hello mlp.Hello
	if json.Unmarshal(payload, &hello) != nil {
		return
	}

	a.mu.Lock()
	key := a.key
	peerID := hello.ID
	a.peers[peerID] = &peer{
		ID: peerID, Name: hello.Name, Platform: hello.Platform,
		IP: strings.Split(c.RemoteAddr().String(), ":")[0], Port: hello.TCP,
		Online: true, LastSeen: time.Now().Unix(),
	}
	a.mu.Unlock()
	if !a.paired || key == nil {
		// Not paired yet: wait for a plaintext pairing request.
		typ, payload, err = mlp.ReadFrame(r, nil)
		if err != nil || typ != mlp.TypePairReq {
			return
		}
		var req mlp.PairReq
		if json.Unmarshal(payload, &req) != nil {
			return
		}
		if len(req.PIN) != 6 {
			res, _ := json.Marshal(mlp.PairResult{OK: false})
			mlp.WriteFrame(c, mlp.TypePairResult, res, nil)
			return
		}
		a.mu.Lock()
		a.pin = req.PIN
		a.key = mlp.DeriveKey(req.PIN, a.id, req.ID)
		a.paired = true
		key = a.key
		a.status = "已配对"
		a.mu.Unlock()
		res, _ := json.Marshal(mlp.PairResult{OK: true, Token: "ok"})
		mlp.WriteFrame(c, mlp.TypePairResult, res, nil)
	}

	// From here on everything is encrypted.
	_ = c.SetDeadline(time.Time{})
	for {
		t, p, err := mlp.ReadFrame(r, key)
		if err != nil {
			return
		}
		switch t {
		case mlp.TypePing:
			mlp.WriteFrame(c, mlp.TypePong, nil, key)
		case mlp.TypeClipText:
			a.setClipboard(string(p))
			a.mu.Lock()
			a.status = "收到剪贴板文本"
			a.mu.Unlock()
		case mlp.TypeNotify:
			var n mlp.Notify
			if json.Unmarshal(p, &n) == nil {
				a.mu.Lock()
				a.notifies = append([]notifyItem{{n.App, n.Title, n.Text, n.TS}}, a.notifies...)
				if len(a.notifies) > 100 {
					a.notifies = a.notifies[:100]
				}
				a.mu.Unlock()
				showToast(n.Title, n.Text)
			}
		case mlp.TypeFileOffer:
			go a.receiveFile(c, p, key)
		}
	}
}

func (a *app) receiveFile(c net.Conn, offerBytes []byte, key []byte) {
	var of mlp.FileOffer
	if json.Unmarshal(offerBytes, &of) != nil {
		return
	}
	safe := filepath.Base(of.Name)
	dst := filepath.Join(a.download, safe)
	f, err := os.Create(dst)
	if err != nil {
		return
	}
	defer f.Close()

	a.mu.Lock()
	a.transfers = append([]transferItem{{ID: of.ID, Name: of.Name, Size: of.Size, Dir: dst, Status: "接收中"}}, a.transfers...)
	a.mu.Unlock()

	ack, _ := json.Marshal(mlp.FileAck{ID: of.ID, Offset: 0, OK: true})
	_ = mlp.WriteFrame(c, mlp.TypeFileAck, ack, key)

	var got int64
	var lastAck int64
	r := bufio.NewReader(c)
	for got < of.Size {
		_ = c.SetReadDeadline(time.Now().Add(60 * time.Second))
		t, p, err := mlp.ReadFrame(r, key)
		if err != nil {
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
		}
	}
	h := sha256.New()
	if _, err := f.Seek(0, io.SeekStart); err == nil {
		_, _ = io.Copy(h, f)
	}
	done, _ := json.Marshal(mlp.FileDone{ID: of.ID, SHA256: fmt.Sprintf("%x", h.Sum(nil))})
	_ = mlp.WriteFrame(c, mlp.TypeFileDone, done, key)
	a.mu.Lock()
	for i := range a.transfers {
		if a.transfers[i].ID == of.ID {
			a.transfers[i].Status = "已完成"
		}
	}
	a.status = "文件已接收: " + safe
	a.mu.Unlock()
}

// sendFile pushes a local file to the connected peer at ip:port.
func (a *app) sendFile(path, ip string, port int) error {
	if !a.paired || a.key == nil {
		return fmt.Errorf("请先配对")
	}
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	conn, err := net.DialTimeout("tcp", fmt.Sprintf("%s:%d", ip, port), 5*time.Second)
	if err != nil {
		return err
	}
	defer conn.Close()
	r := bufio.NewReader(conn)
	mlp.WriteFrame(conn, mlp.TypeHello, a.helloJSON(), nil)
	t, _, err := mlp.ReadFrame(r, nil)
	if err != nil || t != mlp.TypeHello {
		return fmt.Errorf("握手失败")
	}
	id := mlp.MakeID()
	of, _ := json.Marshal(mlp.FileOffer{ID: id, Name: filepath.Base(path), Size: info.Size()})
	_ = mlp.WriteFrame(conn, mlp.TypeFileOffer, of, a.key)
	t, _, err = mlp.ReadFrame(r, a.key)
	if err != nil || t != mlp.TypeFileAck {
		return fmt.Errorf("对端未接受文件")
	}
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	buf := make([]byte, 256<<10)
	var offset int64
	for {
		n, err := f.Read(buf)
		if n > 0 {
			chunk := make([]byte, 20+n)
			// transfer id is 16 hex chars encoded as raw 16 bytes
			copy(chunk[:16], []byte(id)[:16])
			chunk[16] = byte(offset >> 24)
			chunk[17] = byte(offset >> 16)
			chunk[18] = byte(offset >> 8)
			chunk[19] = byte(offset)
			copy(chunk[20:], buf[:n])
			if e := mlp.WriteFrame(conn, mlp.TypeFileChunk, chunk, a.key); e != nil {
				return e
			}
			offset += int64(n)
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
	}
	return nil
}

func (a *app) setClipboard(text string) {
	// Windows: use PowerShell Set-Clipboard (no external dependency).
	ps := fmt.Sprintf("Set-Clipboard -Value %s", psQuote(text))
	cmd := exec.Command("powershell", "-NoProfile", "-Command", ps)
	_ = cmd.Run()
}

func psQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "''") + "'"
}

func showToast(title, text string) {
	ps := fmt.Sprintf(`Add-Type -AssemblyName System.Windows.Forms; [System.Windows.Forms.NotifyIcon]::new() | Out-Null; $n=New-Object System.Windows.Forms.NotifyIcon; $n.Icon=[System.Drawing.SystemIcons]::Information; $n.Visible=$true; $n.ShowBalloonTip(5000,%s,%s,[System.Windows.Forms.ToolTipIcon]::Info); Start-Sleep -Seconds 6; $n.Dispose()`, psQuote(title), psQuote(text))
	cmd := exec.Command("powershell", "-NoProfile", "-Command", ps)
	_ = cmd.Run()
}

// relayMode connects to a relay room and bridges MLP frames over TCP.
func (a *app) relayMode(relayAddr, room string) {
	for {
		conn, err := net.DialTimeout("tcp", relayAddr, 5*time.Second)
		if err != nil {
			a.mu.Lock()
			a.status = "中继连接失败: " + err.Error()
			a.mu.Unlock()
			time.Sleep(5 * time.Second)
			continue
		}
		h := a.self()
		h.Room = room
		b, _ := json.Marshal(h)
		_ = mlp.WriteFrame(conn, mlp.TypeHello, b, nil)
		a.mu.Lock()
		a.status = "已连接中继房间 " + room
		a.mu.Unlock()
		a.handleConn(conn)
		time.Sleep(3 * time.Second)
	}
}

func (a *app) runWeb(addr string) {
	http.HandleFunc("/", a.handleIndex)
	http.HandleFunc("/api/state", a.handleState)
	http.HandleFunc("/api/pair", a.handlePair)
	http.HandleFunc("/api/clip/send", a.handleClipSend)
	http.HandleFunc("/api/file/send", a.handleFileSend)
	http.HandleFunc("/api/notify/list", a.handleNotifyList)
	log.Printf("Web UI: http://%s", addr)
	log.Fatal(http.ListenAndServe(addr, nil))
}

func writeJSON(w http.ResponseWriter, v interface{}) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(v)
}

func (a *app) handleState(w http.ResponseWriter, r *http.Request) {
	a.mu.Lock()
	defer a.mu.Unlock()
	peers := make([]*peer, 0, len(a.peers))
	for _, p := range a.peers {
		peers = append(peers, p)
	}
	sort.Slice(peers, func(i, j int) bool { return peers[i].LastSeen > peers[j].LastSeen })
	writeJSON(w, map[string]interface{}{
		"id": a.id, "name": a.name, "paired": a.paired, "status": a.status,
		"download": a.download, "peers": peers, "transfers": a.transfers,
	})
}

func (a *app) handlePair(w http.ResponseWriter, r *http.Request) {
	pin := r.URL.Query().Get("pin")
	id := r.URL.Query().Get("id")
	_, err := a.pairFromUI(pin, id)
	if err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	writeJSON(w, map[string]interface{}{"ok": true})
}

func (a *app) handleClipSend(w http.ResponseWriter, r *http.Request) {
	text := r.URL.Query().Get("text")
	ip := r.URL.Query().Get("ip")
	port := 47892
	fmt.Sscanf(r.URL.Query().Get("port"), "%d", &port)
	if !a.paired || a.key == nil {
		http.Error(w, "请先配对", 400)
		return
	}
	conn, err := net.DialTimeout("tcp", fmt.Sprintf("%s:%d", ip, port), 5*time.Second)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	defer conn.Close()
	rr := bufio.NewReader(conn)
	_ = mlp.WriteFrame(conn, mlp.TypeHello, a.helloJSON(), nil)
	_, _, _ = mlp.ReadFrame(rr, nil)
	_ = mlp.WriteFrame(conn, mlp.TypeClipText, []byte(text), a.key)
	writeJSON(w, map[string]interface{}{"ok": true})
}

func (a *app) handleFileSend(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Query().Get("path")
	ip := r.URL.Query().Get("ip")
	port := 47892
	fmt.Sscanf(r.URL.Query().Get("port"), "%d", &port)
	if err := a.sendFile(path, ip, port); err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	writeJSON(w, map[string]interface{}{"ok": true})
}

func (a *app) handleNotifyList(w http.ResponseWriter, r *http.Request) {
	a.mu.Lock()
	defer a.mu.Unlock()
	writeJSON(w, a.notifies)
}

func main() {
	webAddr := flag.String("web", "127.0.0.1:47890", "Web UI listen address")
	tcpPort := flag.Int("port", mlp.DataPort, "MLP TCP port")
	relayAddr := flag.String("relay", "", "relay address host:port (optional)")
	room := flag.String("room", "", "relay room code")
	flag.Parse()

	a := newApp()
	a.mu.Lock()
	a.status = "运行中"
	a.mu.Unlock()

	ln, err := net.Listen("tcp", fmt.Sprintf("0.0.0.0:%d", *tcpPort))
	if err != nil {
		log.Fatal(err)
	}
	go a.serveTCP(ln)
	go a.discoveryLoop(make(chan struct{}))
	if *relayAddr != "" && *room != "" {
		go a.relayMode(*relayAddr, *room)
	}
	log.Printf("MoreLink Windows  id=%s  name=%s", a.id, a.name)
	a.runWeb(*webAddr)
}
