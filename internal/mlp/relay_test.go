package mlp

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"sync"
	"testing"
	"time"
)

// fakeRelay mimics the fixed relay: two clients in a room, exactly one
// bidirectional bridge (two pumps), transparent byte forwarding.
func fakeRelay(t *testing.T, addr string, ready chan struct{}) {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	ready <- struct{}{}

	conns := make(chan net.Conn, 2)
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			conns <- c
		}
	}()

	// Wait for two clients, then bridge once.
	a := <-conns
	b := <-conns
	// Consume each client's first HELLO (the one carrying the room code).
	r := bufio.NewReader(a)
	ReadFrame(r, nil)
	r2 := bufio.NewReader(b)
	ReadFrame(r2, nil)

	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); io.Copy(a, b) }()
	go func() { defer wg.Done(); io.Copy(b, a) }()
	wg.Wait()
}

// TestRelayEndToEnd proves two clients can talk through the transparent relay:
// plaintext HELLO exchange, PIN-derived key, encrypted clipboard + file chunks.
func TestRelayEndToEnd(t *testing.T) {
	addr := "127.0.0.1:44791"
	ready := make(chan struct{})
	go fakeRelay(t, addr, ready)
	<-ready
	time.Sleep(50 * time.Millisecond)

	pin := "135790"
	key := DeriveKey(pin, "a", "b")

	// Two clients connect to the relay.
	ca, err := net.DialTimeout("tcp", addr, 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	cb, err := net.DialTimeout("tcp", addr, 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer ca.Close()
	defer cb.Close()

	// Each sends HELLO with room code (consumed by relay).
	ha := Hello{V: 1, ID: "devA", Name: "Phone", Platform: "android", TCP: DataPort, Room: "room1"}
	hb := Hello{V: 1, ID: "devB", Name: "PC", Platform: "windows", TCP: DataPort, Room: "room1"}
	ba, _ := json.Marshal(ha)
	bb, _ := json.Marshal(hb)
	WriteFrame(ca, TypeHello, ba, nil)
	WriteFrame(cb, TypeHello, bb, nil)
	time.Sleep(100 * time.Millisecond) // let relay consume hellos + start bridge

	ra := bufio.NewReader(ca)
	rb := bufio.NewReader(cb)

	// Client A sends second plaintext HELLO; B receives it (transparent forward).
	WriteFrame(ca, TypeHello, ba, nil)
	typ, payload, err := ReadFrame(rb, nil)
	if err != nil || typ != TypeHello {
		t.Fatalf("B did not receive A's HELLO: typ=%d err=%v", typ, err)
	}
	var gotA Hello
	json.Unmarshal(payload, &gotA)
	if gotA.ID != "devA" {
		t.Fatalf("wrong HELLO id: %s", gotA.ID)
	}
	// B replies with its HELLO.
	WriteFrame(cb, TypeHello, bb, nil)
	typ, payload, err = ReadFrame(ra, nil)
	if err != nil || typ != TypeHello {
		t.Fatalf("A did not receive B's HELLO: typ=%d err=%v", typ, err)
	}

	// Encrypted clipboard from phone to PC.
	msg := []byte("hello via relay 跨网络剪贴板")
	if err := WriteFrame(ca, TypeClipText, msg, key); err != nil {
		t.Fatal(err)
	}
	typ, payload, err = ReadFrame(rb, key)
	if err != nil || typ != TypeClipText || !bytes.Equal(payload, msg) {
		t.Fatalf("clipboard via relay failed: typ=%d payload=%q err=%v", typ, payload, err)
	}

	// Encrypted file transfer via relay: offer + chunks.
	fileData := bytes.Repeat([]byte("RELAY-FILE-DATA-"), 5000) // ~75KB
	offer, _ := json.Marshal(FileOffer{ID: "xfer1234567890ab", Name: "relay.bin", Size: int64(len(fileData))})
	WriteFrame(ca, TypeFileOffer, offer, key)
	typ, payload, err = ReadFrame(rb, key)
	if err != nil || typ != TypeFileOffer {
		t.Fatalf("file offer via relay failed: typ=%d err=%v", typ, err)
	}
	ack, _ := json.Marshal(FileAck{ID: "xfer1234567890ab", Offset: 0, OK: true})
	WriteFrame(cb, TypeFileAck, ack, key)
	typ, _, err = ReadFrame(ra, key)
	if err != nil || typ != TypeFileAck {
		t.Fatalf("ack via relay failed: typ=%d err=%v", typ, err)
	}

	var received bytes.Buffer
	chunkSize := 256 << 10
	for off := 0; off < len(fileData); off += chunkSize {
		end := off + chunkSize
		if end > len(fileData) {
			end = len(fileData)
		}
		chunk := make([]byte, 20+end-off)
		copy(chunk[:16], []byte("xfer1234567890ab"))
		chunk[16] = byte(off >> 24)
		chunk[17] = byte(off >> 16)
		chunk[18] = byte(off >> 8)
		chunk[19] = byte(off)
		copy(chunk[20:], fileData[off:end])
		WriteFrame(ca, TypeFileChunk, chunk, key)
		typ, payload, err = ReadFrame(rb, key)
		if err != nil || typ != TypeFileChunk || len(payload) < 20 {
			t.Fatalf("chunk via relay failed at %d: typ=%d err=%v", off, typ, err)
		}
		received.Write(payload[20:])
	}
	if !bytes.Equal(received.Bytes(), fileData) {
		t.Fatalf("file via relay corrupted: got %d bytes want %d", received.Len(), len(fileData))
	}
	fmt.Println("relay end-to-end: hello + encrypted clipboard + file transfer OK")
}
