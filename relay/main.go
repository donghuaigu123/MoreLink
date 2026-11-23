// MoreLink Relay — optional TCP relay for cross-network operation.
// It transparently bridges two clients in the same room. It never sees the
// plaintext of MLP frames because end-to-end keys are derived from the PIN.
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
	log.Printf("room %q: client joined (%d)", c.room, n)
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
}

// pair returns two clients to bridge, waiting briefly for a second client.
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
	log.Printf("room %q: bridging %s <-> %s", h.Room, c.RemoteAddr(), peer.conn.RemoteAddr())
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
	log.Printf("room %q: bridge closed", h.Room)
}

func main() {
	listen := flag.String("listen", fmt.Sprintf(":%d", mlp.RelayPort), "relay listen address")
	flag.Parse()
	ln, err := net.Listen("tcp", *listen)
	if err != nil {
		log.Fatal(err)
	}
	defer ln.Close()
	log.Printf("MoreLink relay listening on %s", *listen)
	for {
		c, err := ln.Accept()
		if err != nil {
			continue
		}
		go handle(c)
	}
}
