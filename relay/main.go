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
}

type room struct {
	mu      sync.Mutex
	clients []*client
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
	r.mu.Lock()
	r.clients = append(r.clients, c)
	n := len(r.clients)
	r.mu.Unlock()
	st.mu.Lock()
	st.clients++
	st.mu.Unlock()
	addLine(fmt.Sprintf("房间 %q: 设备加入 (当前 %d)", c.room, n))
}

func (r *room) remove(c *client) {
	r.mu.Lock()
	for i, x := range r.clients {
		if x == c {
			r.clients = append(r.clients[:i], r.clients[i+1:]...)
			break
		}
	}
	r.mu.Unlock()
	st.mu.Lock()
	if st.clients > 0 {
		st.clients--
	}
	st.mu.Unlock()
}

func (r *room) pair(c *client) (*client, bool) {
	for i := 0; i < 40; i++ {
		r.mu.Lock()
		if len(r.clients) >= 2 {
			for _, x := range r.clients {
				if x != c {
					r.mu.Unlock()
					return x, true
				}
			}
		}
		r.mu.Unlock()
		time.Sleep(250 * time.Millisecond)
	}
	return nil, false
}

func handle(c net.Conn) {
	defer c.Close()
	_ = c.SetDeadline(time.Now().Add(15 * time.Second))
	r := bufio.NewReader(c)
	typ, payload, err := mlp.ReadFrame(r, nil)
	if err != nil || typ != mlp.TypeHello {
		return
	}
	var h mlp.Hello
	if json.Unmarshal(payload, &h) != nil || h.Room == "" {
		return
	}
	cl := &client{conn: c, room: h.Room}
	rm := getRoom(h.Room)
	rm.add(cl)
	defer rm.remove(cl)

	peer, ok := rm.pair(cl)
	if !ok {
		return
	}
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
	}()

	_ = c.SetDeadline(time.Time{})
	_ = peer.conn.SetDeadline(time.Time{})
	var wg sync.WaitGroup
	wg.Add(2)
	pump := func(dst, src net.Conn) {
		defer wg.Done()
		_, _ = io.Copy(dst, src)
		if tc, ok := dst.(*net.TCPConn); ok {
			_ = tc.CloseWrite()
		}
	}
	go pump(c, peer.conn)
	go pump(peer.conn, c)
	wg.Wait()
	addLine(fmt.Sprintf("房间 %q: 桥接结束", h.Room))
}

func serve(listenAddr string) {
	ln, err := net.Listen("tcp", listenAddr)
	if err != nil {
		addLine("监听失败: " + err.Error())
		return
	}
	defer ln.Close()
	st.mu.Lock()
	st.listenAddr = listenAddr
	st.mu.Unlock()
	addLine("中继服务器已启动，监听 " + listenAddr)
	for {
		c, err := ln.Accept()
		if err != nil {
			continue
		}
		go handle(c)
	}
}

func main() {
	listen := flag.String("listen", fmt.Sprintf(":%d", mlp.RelayPort), "中继监听地址")
	headless := flag.Bool("headless", false, "无界面模式")
	flag.Parse()

	go serve(*listen)
	if *headless {
		select {}
	}
	runGUI()
}
