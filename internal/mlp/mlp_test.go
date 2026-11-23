package mlp

import (
	"bufio"
	"bytes"
	"testing"
)

func TestFramePlaintext(t *testing.T) {
	payload := []byte(`{"v":1,"id":"abc","name":"Test"}`)
	var buf bytes.Buffer
	if err := WriteFrame(&buf, TypeHello, payload, nil); err != nil {
		t.Fatal(err)
	}
	r := bufio.NewReader(&buf)
	typ, got, err := ReadFrame(r, nil)
	if err != nil {
		t.Fatal(err)
	}
	if typ != TypeHello || !bytes.Equal(got, payload) {
		t.Fatalf("roundtrip mismatch: type=%d got=%s", typ, got)
	}
}

func TestFrameEncrypted(t *testing.T) {
	key := DeriveKey("123456", "deviceAAAA", "deviceBBBB")
	payload := []byte("剪贴板文本内容 hello world 1234567890")
	var buf bytes.Buffer
	if err := WriteFrame(&buf, TypeClipText, payload, key); err != nil {
		t.Fatal(err)
	}
	// Ciphertext must not contain the plaintext.
	if bytes.Contains(buf.Bytes(), payload) {
		t.Fatal("plaintext leaked into ciphertext")
	}
	r := bufio.NewReader(&buf)
	typ, got, err := ReadFrame(r, key)
	if err != nil {
		t.Fatal(err)
	}
	if typ != TypeClipText || !bytes.Equal(got, payload) {
		t.Fatalf("decrypt mismatch: type=%d got=%s", typ, got)
	}
}

func TestWrongKeyFails(t *testing.T) {
	key1 := DeriveKey("123456", "a", "b")
	key2 := DeriveKey("654321", "a", "b")
	var buf bytes.Buffer
	_ = WriteFrame(&buf, TypeClipText, []byte("secret"), key1)
	r := bufio.NewReader(&buf)
	if _, _, err := ReadFrame(r, key2); err == nil {
		t.Fatal("decrypt with wrong key should fail")
	}
}

func TestKeyOrderIndependence(t *testing.T) {
	k1 := DeriveKey("123456", "aaa", "bbb")
	k2 := DeriveKey("123456", "bbb", "aaa")
	if !bytes.Equal(k1, k2) {
		t.Fatal("key derivation must be order-independent")
	}
}

func TestLargeFileChunk(t *testing.T) {
	key := DeriveKey("000000", "x", "y")
	payload := make([]byte, 256<<10)
	for i := range payload {
		payload[i] = byte(i % 251)
	}
	var buf bytes.Buffer
	if err := WriteFrame(&buf, TypeFileChunk, payload, key); err != nil {
		t.Fatal(err)
	}
	r := bufio.NewReader(&buf)
	_, got, err := ReadFrame(r, key)
	if err != nil || !bytes.Equal(got, payload) {
		t.Fatalf("large chunk failed: err=%v len=%d", err, len(got))
	}
}
