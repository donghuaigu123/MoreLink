// Package mlp implements the MoreLink Protocol v1 wire format.
// It is shared by the Windows client, the relay server and the conformance tool.
package mlp

import (
	"bufio"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
)

const (
	MaxFrame = 8 << 20 // 8 MiB

	TypeHello      byte = 0x01
	TypePairReq    byte = 0x02
	TypePairResult byte = 0x03
	TypeClipText   byte = 0x10
	TypeClipImage  byte = 0x11
	TypeNotify     byte = 0x20
	TypeFileOffer  byte = 0x30
	TypeFileChunk  byte = 0x31
	TypeFileAck    byte = 0x32
	TypeFileDone   byte = 0x33
	TypePing       byte = 0x40
	TypePong       byte = 0x41
	TypePeerInfo   byte = 0x50

	DiscoveryPort = 47891
	DataPort      = 47892
	RelayPort     = 47893
	ProtocolVer   = 1
)

var ErrTooLarge = errors.New("mlp: frame exceeds 8 MiB")

type Hello struct {
	V        int    `json:"v"`
	ID       string `json:"id"`
	Name     string `json:"name"`
	Platform string `json:"platform"`
	TCP      int    `json:"tcp"`
	IP       string `json:"ip,omitempty"`
	Room     string `json:"room,omitempty"`
}

type PairReq struct {
	PIN  string `json:"pin"`
	ID   string `json:"id"`
	Name string `json:"name"`
}

type PairResult struct {
	OK    bool   `json:"ok"`
	Token string `json:"token"`
}

type Notify struct {
	App   string `json:"app"`
	Title string `json:"title"`
	Text  string `json:"text"`
	TS    int64  `json:"ts"`
}

type FileOffer struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Size int64  `json:"size"`
	MIME string `json:"mime"`
}

type FileAck struct {
	ID     string `json:"id"`
	Offset int64  `json:"offset"`
	OK     bool   `json:"ok"`
}

type FileDone struct {
	ID     string `json:"id"`
	SHA256 string `json:"sha256"`
}

// DeriveKey derives the AES-256 session key from the pairing PIN.
//
// v2 change: the key depends ONLY on the shared PIN (with a domain separator).
// The previous design also mixed in both device IDs, but the two apps did not
// agree on those IDs (one used a hardcoded placeholder), so the keys never
// matched and every encrypted message failed to decrypt. Deriving from the PIN
// alone guarantees both peers compute the identical key regardless of which
// IDs each side stored — this is the single most important fix for real
// interoperability. Device IDs are still exchanged during HELLO for display.
func DeriveKey(pin, idA, idB string) []byte {
	// idA/idB are intentionally ignored; kept in the signature for source
	// compatibility with v1 call sites and the protocol handshake.
	_, _ = idA, idB
	sum := sha256.Sum256([]byte("MoreLink-v2|pin:" + pin))
	return sum[:]
}

func aesGCM(key []byte) (cipher.AEAD, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

// WriteFrame writes one frame. If key is non-nil the payload is encrypted
// with AES-256-GCM and a 12-byte random nonce is placed after the type byte.
func WriteFrame(w io.Writer, typ byte, payload []byte, key []byte) error {
	if len(payload) > MaxFrame {
		return ErrTooLarge
	}
	var body []byte
	if key == nil {
		body = make([]byte, 0, 1+len(payload))
		body = append(body, typ)
		body = append(body, payload...)
	} else {
		aead, err := aesGCM(key)
		if err != nil {
			return err
		}
		nonce := make([]byte, aead.NonceSize())
		if _, err := rand.Read(nonce); err != nil {
			return err
		}
		ct := aead.Seal(nil, nonce, payload, []byte{typ})
		body = make([]byte, 0, 1+len(nonce)+len(ct))
		body = append(body, typ)
		body = append(body, nonce...)
		body = append(body, ct...)
	}
	if len(body) > MaxFrame {
		return ErrTooLarge
	}
	var hdr [4]byte
	binary.BigEndian.PutUint32(hdr[:], uint32(len(body)))
	if _, err := w.Write(hdr[:]); err != nil {
		return err
	}
	_, err := w.Write(body)
	return err
}

// ReadFrame reads one frame using a bufio.Reader for efficiency.
func ReadFrame(r *bufio.Reader, key []byte) (byte, []byte, error) {
	var hdr [4]byte
	if _, err := io.ReadFull(r, hdr[:]); err != nil {
		return 0, nil, err
	}
	n := binary.BigEndian.Uint32(hdr[:])
	if n > uint32(MaxFrame) || n < 1 {
		return 0, nil, fmt.Errorf("mlp: invalid frame length %d", n)
	}
	body := make([]byte, n)
	if _, err := io.ReadFull(r, body); err != nil {
		return 0, nil, err
	}
	typ := body[0]
	rest := body[1:]
	if key == nil {
		return typ, rest, nil
	}
	if len(rest) < 12 {
		return 0, nil, errors.New("mlp: encrypted frame too short")
	}
	aead, err := aesGCM(key)
	if err != nil {
		return 0, nil, err
	}
	nonce := rest[:aead.NonceSize()]
	ct := rest[aead.NonceSize():]
	plain, err := aead.Open(nil, nonce, ct, []byte{typ})
	if err != nil {
		return 0, nil, fmt.Errorf("mlp: decrypt failed: %w", err)
	}
	return typ, plain, nil
}

// MakeID returns a random 16-character hex device/transfer id.
func MakeID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// MakePIN returns a random 6-digit pairing code.
func MakePIN() string {
	b := make([]byte, 4)
	_, _ = rand.Read(b)
	n := binary.BigEndian.Uint32(b) % 1000000
	return fmt.Sprintf("%06d", n)
}
