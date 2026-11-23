// MoreLink Relay — optional TCP relay for cross-network operation.
// On Windows it shows a native GUI status window; with -headless it runs
// without a window. It transparently bridges two clients in the same room and
// never sees plaintext (end-to-end keys are derived from the PIN).
package main

import (
	"bufio"
	"crypto/rand"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"regexp"
	"sync"
	"time"

	"github.com/donghuaigu123/MoreLink/internal/mlp"
)

// Connection info that clients need in order to join this relay. The relay
// generates a ready-to-use room code and pairing PIN and prints/shows them so
// users can simply copy the three values into both apps (" foolproof" setup).
type connectionInfo struct {
	mu        sync.Mutex
	room      string // human-friendly room code, e.g. ML-7K3Q2W
	pin       string // 6-digit pairing PIN shared by both clients
	listen    string // raw listen address, e.g. :47893
	public    string // externally advertised host:port (LAN IP or public IP/domain)
	interfaces []string
}

var ci = &connectionInfo{}

var rePort = regexp.MustCompile(`:(\d+)$`)

func listenPort(addr string) int {
	if m := rePort.FindStringSubmatch(addr); m != nil {
		var p int
		if _, err := fmt.Sscanf(m[1], "%d", &p); err == nil && p > 0 {
			return p
		}
	}
	return mlp.RelayPort
}

// localIPv4 returns all non-loopback IPv4 addresses of this machine.
func localIPv4() []string {
	var out []string
	ifs, err := net.Interfaces()
	if err != nil {
		return out
	}
	for _, i := range ifs {
		if i.Flags&net.FlagUp == 0 || i.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, _ := i.Addrs()
		for _, a := range addrs {
			if ipnet, ok := a.(*net.IPNet); ok {
				if ip4 := ipnet.IP.To4(); ip4 != nil {
					out = append(out, ip4.String())
				}
			}
		}
	}
	return out
}

// genRoomCode makes a short, unambiguous room code (no 0/O/1/I/l).
func genRoomCode() string {
	const alpha = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789"
	b := make([]byte, 6)
	if _, err := rand.Read(b); err != nil {
		// Extremely unlikely; fall back to a time-based value so the code is
		// never empty/constant even on a system without a CSPRNG.
		n := time.Now().UnixNano()
		for i := range b {
			b[i] = byte(n >> (uint(i) * 8))
		}
	}
	out := make([]byte, len(b))
	for i, v := range b {
		out[i] = alpha[int(v)%len(alpha)]
	}
	return "ML-" + string(out)
}

// initConnection prepares a fresh room + PIN and detects LAN addresses.
func initConnection(listen string) {
	ci.mu.Lock()
	ci.room = genRoomCode()
	ci.pin = mlp.MakePIN()
	ci.listen = listen
	ci.interfaces = localIPv4()
	port := listenPort(listen)
	host := "127.0.0.1"
	if len(ci.interfaces) > 0 {
		host = ci.interfaces[0]
	}
	ci.public = fmt.Sprintf("%s:%d", host, port)
	ci.mu.Unlock()
}

// regenerateCodes creates a new room code and PIN (addresses stay the same).
func regenerateCodes() (string, string) {
	ci.mu.Lock()
	ci.room = genRoomCode()
	ci.pin = mlp.MakePIN()
	room, pin := ci.room, ci.pin
	ci.mu.Unlock()
	addLine(fmt.Sprintf("已生成新的连接信息：房间 %s  配对码 %s", room, pin))
	return room, pin
}

// setPublicHost lets the user override the advertised address (public IP/domain).
func setPublicHost(host string) string {
	ci.mu.Lock()
	if host == "" {
		host = "127.0.0.1"
	}
	port := listenPort(ci.listen)
	// If the user typed host:port, keep as-is; otherwise append our port.
	if rePort.MatchString(host) {
		ci.public = host
	} else {
		ci.public = fmt.Sprintf("%s:%d", host, port)
	}
	pub := ci.public
	ci.mu.Unlock()
	return pub
}

// connInfoText returns a copy-paste block with everything both clients need.
func connInfoText() (room, pin, public string, ifaces []string) {
	ci.mu.Lock()
	defer ci.mu.Unlock()
	return ci.room, ci.pin, ci.public, append([]string(nil), ci.interfaces...)
}

func printConnInfo() {
	room, pin, pub, ifaces := connInfoText()
	fmt.Println("========== MoreLink 中继连接信息 ==========")
	fmt.Printf("服务器地址:端口 : %s\n", pub)
	if len(ifaces) > 1 {
		fmt.Printf("本机其它地址     : %v\n", ifaces[1:])
	}
	fmt.Printf("房间码           : %s\n", room)
	fmt.Printf("配对码           : %s\n", pin)
	fmt.Println("两端客户端填入以上相同的地址/房间码/配对码即可连接")
	fmt.Println("==========================================")
}

type client struct {
	conn net.Conn
	room string
	done chan struct{} // closed by the initiator when the bridge ends
}

type room struct {
	mu    sync.Mutex
	queue []*client
}

var rooms = struct {
	sync.Mutex
	m map[string]*room
}{m: map[string]*room{}}

type status struct {
	mu         sync.Mutex
	clients    int
	bridges    int
	lines      []string
	listenAddr string
	running    bool
}

var st = &status{}

func addLine(s string) {
	st.mu.Lock()
	st.lines = append([]string{fmt.Sprintf("[%s] %s", time.Now().Format("15:04:05"), s)}, st.lines...)
	if len(st.lines) > 100 {
		st.lines = st.lines[:100]
	}
	st.mu.Unlock()
	log.Print(s)
}

func getRoom(name string) *room {
	rooms.Lock()
	defer rooms.Unlock()
	r := rooms.m[name]
	if r == nil {
		r = &room{}
		rooms.m[name] = r
	}
	return r
}

func (r *room) add(c *client) {
	st.mu.Lock()
	st.clients++
	st.mu.Unlock()
	addLine(fmt.Sprintf("房间 %q: 设备加入 (在线 %d)", c.room, countClients()))
}

func (r *room) remove(c *client) {
	st.mu.Lock()
	if st.clients > 0 {
		st.clients--
	}
	st.mu.Unlock()
}

// take registers c and returns a peer plus whether c must drive the bridge.
// CRITICAL FIX: exactly ONE side (the initiator who found a waiting peer)
// starts the bidirectional pump. Previously BOTH clients started a pump,
// creating four concurrent io.Copy streams over two sockets => corruption.
func (r *room) take(c *client) (*client, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for len(r.queue) > 0 {
		p := r.queue[0]
		r.queue = r.queue[1:]
		if p.conn != nil {
			return p, true // I found a waiter: I drive the bridge
		}
	}
	r.queue = append(r.queue, c)
	return nil, false // wait for someone to pick me up
}

// leave removes a client from the waiting queue if it is still there.
func (r *room) leave(c *client) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for i, x := range r.queue {
		if x == c {
			r.queue = append(r.queue[:i], r.queue[i+1:]...)
			break
		}
	}
}

func countClients() int {
	st.mu.Lock()
	defer st.mu.Unlock()
	return st.clients
}

func bridgePair(a, b net.Conn) {
	_ = a.SetDeadline(time.Time{})
	_ = b.SetDeadline(time.Time{})
	var wg sync.WaitGroup
	wg.Add(2)
	pump := func(dst, src net.Conn) {
		defer wg.Done()
		_, _ = io.Copy(dst, src)
		if tc, ok := dst.(*net.TCPConn); ok {
			_ = tc.CloseWrite()
		}
	}
	go pump(a, b)
	go pump(b, a)
	wg.Wait()
}

func handle(c net.Conn) {
	defer c.Close()
	_ = c.SetDeadline(time.Now().Add(20 * time.Second))
	r := bufio.NewReader(c)
	typ, payload, err := mlp.ReadFrame(r, nil)
	if err != nil || typ != mlp.TypeHello {
		return
	}
	var h mlp.Hello
	if json.Unmarshal(payload, &h) != nil || h.Room == "" {
		return
	}
	cl := &client{conn: c, room: h.Room, done: make(chan struct{})}
	rm := getRoom(h.Room)
	rm.add(cl)
	defer rm.remove(cl)
	defer rm.leave(cl)

	// Wait up to 60s for a peer to arrive in the same room.
	peer, initiator := rm.take(cl)
	if !initiator {
		select {
		case <-cl.done:
			return
		case <-time.After(60 * time.Second):
			return
		}
	}

	// Re-read peer's HELLO? No — both HELLOs were consumed by the relay itself.
	// The clients' handleConn emits a second plaintext HELLO once bridged, so
	// transparent byte forwarding is exactly right here.
	addLine(fmt.Sprintf("房间 %q: 开始桥接 %s <-> %s", h.Room, c.RemoteAddr(), peer.conn.RemoteAddr()))
	st.mu.Lock()
	st.bridges++
	st.mu.Unlock()
	defer func() {
		st.mu.Lock()
		if st.bridges > 0 {
			st.bridges--
		}
		st.mu.Unlock()
		// Notify the waiting side so its handle() can return.
		close(cl.done)
		if peer.done != nil {
			select {
			case <-peer.done:
			default:
				close(peer.done)
			}
		}
		addLine(fmt.Sprintf("房间 %q: 桥接结束", h.Room))
	}()

	bridgePair(c, peer.conn)
	_ = peer.conn.Close()
}

var (
	srvMu      sync.Mutex
	srvLn      net.Listener
	srvStop    chan struct{}
	srvAddr    string
)

// startServer starts the relay listener. Safe to call repeatedly (stops old first).
func startServer(listenAddr string) error {
	srvMu.Lock()
	defer srvMu.Unlock()
	if srvLn != nil {
		_ = srvLn.Close()
		if srvStop != nil {
			close(srvStop)
		}
		srvLn = nil
	}
	ln, err := net.Listen("tcp", listenAddr)
	if err != nil {
		st.mu.Lock()
		st.running = false
		st.mu.Unlock()
		return err
	}
	srvLn = ln
	srvStop = make(chan struct{})
	srvAddr = listenAddr
	st.mu.Lock()
	st.listenAddr = listenAddr
	st.running = true
	st.mu.Unlock()
	addLine("中继服务器已启动，监听 " + listenAddr)
	if ci.room == "" {
		initConnection(listenAddr)
	}
	room, pin, pub, _ := connInfoText()
	addLine(fmt.Sprintf("连接信息 → 地址 %s | 房间码 %s | 配对码 %s", pub, room, pin))
	go func(l net.Listener, stop chan struct{}) {
		for {
			c, err := l.Accept()
			if err != nil {
				select {
				case <-stop:
					return
				default:
					continue
				}
			}
			go handle(c)
		}
	}(ln, srvStop)
	return nil
}

func stopServer() {
	srvMu.Lock()
	defer srvMu.Unlock()
	if srvLn != nil {
		_ = srvLn.Close()
		srvLn = nil
	}
	if srvStop != nil {
		close(srvStop)
		srvStop = nil
	}
	st.mu.Lock()
	st.running = false
	st.mu.Unlock()
	addLine("中继服务器已停止")
}

func main() {
	listen := flag.String("listen", fmt.Sprintf(":%d", mlp.RelayPort), "中继监听地址")
	headless := flag.Bool("headless", false, "无界面模式")
	flag.Parse()

	initConnection(*listen)
	if err := startServer(*listen); err != nil {
		addLine("监听失败: " + err.Error())
	}
	if *headless {
		printConnInfo()
		select {}
	}
	runGUIProtected()
}
