package mlp

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net"
	"testing"
	"time"
)

// TestV2KeyIsPINOnly verifies the critical fix: both peers derive the same key
// from the PIN alone, regardless of what device IDs they pass. This mirrors the
// exact bug where Android used a hardcoded "windows-peer" ID and Windows used
// the real ID, so keys never matched.
func TestV2KeyIsPINOnly(t *testing.T) {
	const pin = "123456"
	kWindows := DeriveKey(pin, "win-real-id", "android-real-id")
	kAndroid := DeriveKey(pin, "android-real-id", "windows-peer") // different IDs!
	if !bytes.Equal(kWindows, kAndroid) {
		t.Fatal("v2 key must depend only on PIN")
	}
	want := sha256.Sum256([]byte("MoreLink-v2|pin:" + pin))
	if !bytes.Equal(kWindows, want[:]) {
		t.Fatal("v2 key formula mismatch with Java implementation")
	}
}

// TestV2FullSession simulates a complete real session with the v2 key:
// HELLO handshake -> PAIR_REQ/RESULT -> encrypted clipboard -> two file
// transfers in sequence on ONE reader (the inline-receive fix).
func TestV2FullSession(t *testing.T) {
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	defer ln.Close()
	addr := ln.Addr().String()
	const pin = "654321"
	key := DeriveKey(pin, "", "")

	errCh := make(chan error, 1)
	go func() {
		c, err := ln.Accept()
		if err != nil {
			errCh <- err
			return
		}
		defer c.Close()
		r := bufio.NewReader(c)
		// plaintext hello
		_ = WriteFrame(c, TypeHello, []byte(`{"v":1,"id":"phone","name":"Phone","platform":"android","tcp":47892}`), nil)
		typ, _, err := ReadFrame(r, nil)
		if err != nil || typ != TypeHello {
			errCh <- fmt.Errorf("hello: %v", err)
			return
		}
		// pair request
		typ, p, err := ReadFrame(r, nil)
		if err != nil || typ != TypePairReq {
			errCh <- fmt.Errorf("pairreq: %v", err)
			return
		}
		res, _ := json.Marshal(PairResult{OK: true})
		_ = WriteFrame(c, TypePairResult, res, nil)

		// read clipboard
		typ, p, err = ReadFrame(r, key)
		if err != nil || typ != TypeClipText || string(p) != "v2 works" {
			errCh <- fmt.Errorf("clip: %v %s", err, p)
			return
		}

		// receive two files back-to-back on the SAME reader
		for i := 0; i < 2; i++ {
			typ, p, err = ReadFrame(r, key)
			if err != nil || typ != TypeFileOffer {
				errCh <- fmt.Errorf("offer%d: %v", i, err)
				return
			}
			var of FileOffer
			_ = json.Unmarshal(p, &of)
			ack, _ := json.Marshal(FileAck{ID: of.ID, Offset: 0, OK: true})
			_ = WriteFrame(c, TypeFileAck, ack, key)
			typ, p, err = ReadFrame(r, key)
			if err != nil || typ != TypeFileChunk {
				errCh <- fmt.Errorf("chunk%d: %v", i, err)
				return
			}
			data := p[20:]
			if string(data) != fmt.Sprintf("file-%d", i) {
				errCh <- fmt.Errorf("file data mismatch: %s", data)
				return
			}
			done, _ := json.Marshal(FileDone{ID: of.ID})
			_ = WriteFrame(c, TypeFileDone, done, key)
		}
		errCh <- nil
	}()

	c, _ := net.Dial("tcp", addr)
	defer c.Close()
	_ = c.SetDeadline(time.Now().Add(10 * time.Second))
	r := bufio.NewReader(c)
	_ = WriteFrame(c, TypeHello, []byte(`{"v":1,"id":"pc","name":"PC","platform":"windows","tcp":47892}`), nil)
	if _, _, err := ReadFrame(r, nil); err != nil {
		t.Fatal(err)
	}
	req, _ := json.Marshal(PairReq{PIN: pin, ID: "pc"})
	_ = WriteFrame(c, TypePairReq, req, nil)
	if _, _, err := ReadFrame(r, nil); err != nil {
		t.Fatal(err)
	}
	_ = WriteFrame(c, TypeClipText, []byte("v2 works"), key)
	for i := 0; i < 2; i++ {
		data := []byte(fmt.Sprintf("file-%d", i))
		of, _ := json.Marshal(FileOffer{ID: MakeID(), Name: "f", Size: int64(len(data))})
		_ = WriteFrame(c, TypeFileOffer, of, key)
		if _, _, err := ReadFrame(r, key); err != nil {
			t.Fatal(err)
		}
		chunk := make([]byte, 20+len(data))
		copy(chunk[:16], MakeID())
		chunk[19] = byte(len(data))
		copy(chunk[20:], data)
		_ = WriteFrame(c, TypeFileChunk, chunk, key)
		if _, _, err := ReadFrame(r, key); err != nil {
			t.Fatal(err)
		}
	}
	if err := <-errCh; err != nil {
		t.Fatal(err)
	}
	t.Log("v2 full session: pairing + clipboard + sequential file transfers OK")
}
