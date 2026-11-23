// MoreLink Relay — optional TCP relay for cross-network operation.
// On Windows it shows a native GUI status window; with -headless it runs
// without a window. It transparently bridges two clients in the same room and
// never sees plaintext (end-to-end keys are derived from the PIN).
package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"sync"
	"time"

	"github.com/donghuaigu123/MoreLink/internal/mlp"
)

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

	if err := startServer(*listen); err != nil {
		addLine("监听失败: " + err.Error())
	}
	if *headless {
		select {}
	}
	runGUIProtected()
}
