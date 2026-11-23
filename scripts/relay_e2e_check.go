//go:build ignore

// relay_e2e_check.go — live check against a running relay binary.
// Usage: go run scripts/relay_e2e_check.go 127.0.0.1:44893
package main

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"time"

	"github.com/donghuaigu123/MoreLink/internal/mlp"
)

func main() {
	addr := os.Args[1]
	pin := "246810"
	key := mlp.DeriveKey(pin, "", "")
	room := "e2eroom"

	// Connect two clients to the live relay.
	ca, err := net.DialTimeout("tcp", addr, 3*time.Second)
	if err != nil { panic(err) }
	cb, err := net.DialTimeout("tcp", addr, 3*time.Second)
	if err != nil { panic(err) }
	defer ca.Close()
	defer cb.Close()

	ha, _ := json.Marshal(mlp.Hello{V: 1, ID: "phone001", Name: "Phone", Platform: "android", TCP: mlp.DataPort, Room: room})
	hb, _ := json.Marshal(mlp.Hello{V: 1, ID: "pc001", Name: "PC", Platform: "windows", TCP: mlp.DataPort, Room: room})
	mlp.WriteFrame(ca, mlp.TypeHello, ha, nil) // consumed by relay
	mlp.WriteFrame(cb, mlp.TypeHello, hb, nil) // consumed by relay
	time.Sleep(300 * time.Millisecond) // let relay pair + start single bridge

	ra := bufio.NewReader(ca)
	rb := bufio.NewReader(cb)
	// Second HELLO travels through the transparent bridge.
	mlp.WriteFrame(ca, mlp.TypeHello, ha, nil)
	t, p, err := mlp.ReadFrame(rb, nil)
	if err != nil || t != mlp.TypeHello { panic(fmt.Sprintf("peer HELLO A->B failed: t=%d err=%v", t, err)) }
	mlp.WriteFrame(cb, mlp.TypeHello, hb, nil)
	t, _, err = mlp.ReadFrame(ra, nil)
	if err != nil || t != mlp.TypeHello { panic(fmt.Sprintf("peer HELLO B->A failed: t=%d err=%v", t, err)) }
	fmt.Println("  [OK] 双向 HELLO 握手经真实中继完成")

	// Encrypted clipboard.
	msg := []byte("跨网络剪贴板 via REAL relay ✓")
	mlp.WriteFrame(ca, mlp.TypeClipText, msg, key)
	t, p, err = mlp.ReadFrame(rb, key)
	if err != nil || t != mlp.TypeClipText || !bytes.Equal(p, msg) {
		panic(fmt.Sprintf("clipboard failed: t=%d p=%q err=%v", t, p, err))
	}
	fmt.Println("  [OK] 端到端加密剪贴板经真实中继完成")

	// File transfer via relay.
	data := bytes.Repeat([]byte("REAL-RELAY-FILE-"), 6000) // ~96KB
	fid := "file1234567890ab"
	offer, _ := json.Marshal(mlp.FileOffer{ID: fid, Name: "real-relay.bin", Size: int64(len(data))})
	mlp.WriteFrame(ca, mlp.TypeFileOffer, offer, key)
	t, _, err = mlp.ReadFrame(rb, key)
	if err != nil || t != mlp.TypeFileOffer { panic(fmt.Sprintf("offer failed: t=%d err=%v", t, err)) }
	ack, _ := json.Marshal(mlp.FileAck{ID: fid, Offset: 0, OK: true})
	mlp.WriteFrame(cb, mlp.TypeFileAck, ack, key)
	t, _, err = mlp.ReadFrame(ra, key)
	if err != nil || t != mlp.TypeFileAck { panic(fmt.Sprintf("ack failed: t=%d err=%v", t, err)) }

	var recv bytes.Buffer
	cs := 256 << 10
	for off := 0; off < len(data); off += cs {
		end := off + cs
		if end > len(data) { end = len(data) }
		chunk := make([]byte, 20+end-off)
		copy(chunk[:16], []byte(fid))
		chunk[16] = byte(off >> 24); chunk[17] = byte(off >> 16); chunk[18] = byte(off >> 8); chunk[19] = byte(off)
		copy(chunk[20:], data[off:end])
		mlp.WriteFrame(ca, mlp.TypeFileChunk, chunk, key)
		t, p, err = mlp.ReadFrame(rb, key)
		if err != nil || t != mlp.TypeFileChunk { panic(fmt.Sprintf("chunk failed at %d: t=%d err=%v", off, t, err)) }
		recv.Write(p[20:])
	}
	if !bytes.Equal(recv.Bytes(), data) { panic("file corrupted via relay") }
	h := sha256.Sum256(recv.Bytes())
	fmt.Printf("  [OK] 文件传输经真实中继完成（%d 字节，SHA256=%x）\n", recv.Len(), h[:8])
	fmt.Println("ALL_RELAY_E2E_OK")
	_ = io.Discard
}
