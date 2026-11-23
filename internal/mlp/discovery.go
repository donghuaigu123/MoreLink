package mlp

import (
	"encoding/json"
	"net"
	"time"
)

// Announce broadcasts this device's hello packet on the LAN and returns the
// UDP socket so the caller can keep listening for replies.
func Announce(h Hello) (*net.UDPConn, error) {
	addr, err := net.ResolveUDPAddr("udp4", "255.255.255.255:47891")
	if err != nil {
		return nil, err
	}
	conn, err := net.DialUDP("udp4", nil, addr)
	if err != nil {
		return nil, err
	}
	data, _ := json.Marshal(h)
	_, _ = conn.Write(data)
	_ = conn.Close()

	listen, err := net.ListenUDP("udp4", &net.UDPAddr{Port: DiscoveryPort})
	if err != nil {
		return nil, err
	}
	return listen, nil
}

// StartBeacon periodically broadcasts hello until stop is closed.
func StartBeacon(h Hello, stop <-chan struct{}) {
	data, _ := json.Marshal(h)
	addr, err := net.ResolveUDPAddr("udp4", "255.255.255.255:47891")
	if err != nil {
		return
	}
	ticker := time.NewTicker(3 * time.Second)
	defer ticker.Stop()
	for {
		conn, err := net.DialUDP("udp4", nil, addr)
		if err == nil {
			_, _ = conn.Write(data)
			_ = conn.Close()
		}
		select {
		case <-stop:
			return
		case <-ticker.C:
		}
	}
}

// ListenDiscovery reads hello packets from a UDP socket and sends decoded
// hellos plus the sender IP to out.
func ListenDiscovery(conn *net.UDPConn, out chan<- Hello, stop <-chan struct{}) {
	buf := make([]byte, 2048)
	for {
		select {
		case <-stop:
			return
		default:
		}
		_ = conn.SetReadDeadline(time.Now().Add(500 * time.Millisecond))
		n, addr, err := conn.ReadFromUDP(buf)
		if err != nil {
			continue
		}
		var h Hello
		if json.Unmarshal(buf[:n], &h) != nil || h.ID == "" {
			continue
		}
		if addr != nil && addr.IP != nil {
			h.IP = addr.IP.String()
		}
		out <- h
	}
}
