package mlp

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestEndToEnd simulates a Windows client and an Android client connecting
// over a TCP socket: plaintext HELLO -> pairing -> encrypted clipboard + file.
func TestEndToEnd(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	addr := ln.Addr().String()

	const pin = "135790"
	idWin, idAnd := "win0000000000001", "and0000000000002"

	serverDone := make(chan error, 1)
	go func() {
		c, err := ln.Accept()
		if err != nil {
			serverDone <- err
			return
		}
		defer c.Close()
		r := bufio.NewReader(c)
		// plaintext hello exchange
		if err := WriteFrame(c, TypeHello, []byte(helloJSON(idAnd, "Android")), nil); err != nil {
			serverDone <- err
			return
		}
		typ, p, err := ReadFrame(r, nil)
		if err != nil || typ != TypeHello {
			serverDone <- fmt.Errorf("bad server hello: %v", err)
			return
		}
		var h Hello
		_ = json.Unmarshal(p, &h)
		if h.ID != idWin {
			serverDone <- fmt.Errorf("unexpected peer id %s", h.ID)
			return
		}
		// pairing request from client
		typ, p, err = ReadFrame(r, nil)
		if err != nil || typ != TypePairReq {
			serverDone <- fmt.Errorf("no pair req: %v", err)
			return
		}
		key := DeriveKey(pin, idWin, idAnd)
		res, _ := json.Marshal(PairResult{OK: true, Token: "ok"})
		if err := WriteFrame(c, TypePairResult, res, nil); err != nil {
			serverDone <- err
			return
		}
		// encrypted clipboard
		typ, p, err = ReadFrame(r, key)
		if err != nil || typ != TypeClipText || string(p) != "你好 MoreLink" {
			serverDone <- fmt.Errorf("clipboard: type=%d val=%s err=%v", typ, p, err)
			return
		}
		// file transfer
		typ, p, err = ReadFrame(r, key)
		if err != nil || typ != TypeFileOffer {
			serverDone <- fmt.Errorf("no file offer: %v", err)
			return
		}
		var of FileOffer
		_ = json.Unmarshal(p, &of)
		ack, _ := json.Marshal(FileAck{ID: of.ID, Offset: 0, OK: true})
		_ = WriteFrame(c, TypeFileAck, ack, key)
		typ, p, err = ReadFrame(r, key)
		if err != nil || typ != TypeFileChunk {
			serverDone <- fmt.Errorf("no file chunk: %v", err)
			return
		}
		var id [16]byte
		copy(id[:], p[:16])
		data := p[20:]
		_ = os.WriteFile(filepath.Join(os.TempDir(), "morelink-test.bin"), data, 0644)
		done, _ := json.Marshal(FileDone{ID: of.ID, SHA256: "test"})
		_ = WriteFrame(c, TypeFileDone, done, key)
		serverDone <- nil
	}()

	// client side
	c, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	_ = c.SetDeadline(time.Now().Add(10 * time.Second))
	r := bufio.NewReader(c)
	if err := WriteFrame(c, TypeHello, []byte(helloJSON(idWin, "Windows")), nil); err != nil {
		t.Fatal(err)
	}
	typ, _, err := ReadFrame(r, nil)
	if err != nil || typ != TypeHello {
		t.Fatalf("client hello: %v", err)
	}
	req, _ := json.Marshal(PairReq{PIN: pin, ID: idWin, Name: "Windows"})
	_ = WriteFrame(c, TypePairReq, req, nil)
	typ, _, err = ReadFrame(r, nil)
	if err != nil || typ != TypePairResult {
		t.Fatalf("pair result: %v", err)
	}
	key := DeriveKey(pin, idWin, idAnd)
	_ = WriteFrame(c, TypeClipText, []byte("你好 MoreLink"), key)

	content := []byte("MoreLink integration test file content 12345")
	of, _ := json.Marshal(FileOffer{ID: MakeID(), Name: "test.bin", Size: int64(len(content))})
	_ = WriteFrame(c, TypeFileOffer, of, key)
	typ, _, err = ReadFrame(r, key)
	if err != nil || typ != TypeFileAck {
		t.Fatalf("file ack: %v", err)
	}
	chunk := make([]byte, 20+len(content))
	copy(chunk[:16], MakeID()[:16])
	chunk[19] = byte(len(content))
	copy(chunk[20:], content)
	_ = WriteFrame(c, TypeFileChunk, chunk, key)
	typ, _, err = ReadFrame(r, key)
	if err != nil || typ != TypeFileDone {
		t.Fatalf("file done: %v", err)
	}

	if err := <-serverDone; err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(filepath.Join(os.TempDir(), "morelink-test.bin"))
	if !bytes.Equal(got, content) {
		t.Fatalf("file mismatch: %s", got)
	}
	t.Log("end-to-end handshake, pairing, encrypted clipboard and file transfer OK")
}

func helloJSON(id, name string) string {
	return fmt.Sprintf(`{"v":1,"id":"%s","name":"%s","platform":"test","tcp":47892}`, id, name)
}

var _ = io.EOF
