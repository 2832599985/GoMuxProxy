package proxy

import (
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
)

func handleSocks5(conn net.Conn) (string, error) {
	header := make([]byte, 2)
	if _, err := io.ReadFull(conn, header); err != nil {
		return "", fmt.Errorf("socks5 read header: %w", err)
	}
	if header[0] != 0x05 {
		return "", fmt.Errorf("socks5 unsupported version: %d", header[0])
	}
	nMethods := int(header[1])
	methods := make([]byte, nMethods)
	if _, err := io.ReadFull(conn, methods); err != nil {
		return "", fmt.Errorf("socks5 read methods: %w", err)
	}

	if _, err := conn.Write([]byte{0x05, 0x00}); err != nil {
		return "", fmt.Errorf("socks5 write auth reply: %w", err)
	}

	req := make([]byte, 4)
	if _, err := io.ReadFull(conn, req); err != nil {
		return "", fmt.Errorf("socks5 read request: %w", err)
	}
	if req[1] != 0x01 {
		writeSocks5Reply(conn, 0x07) // command not supported
		return "", fmt.Errorf("socks5 unsupported command: %d", req[1])
	}

	var host string
	switch req[3] {
	case 0x01:
		addr := make([]byte, 4)
		if _, err := io.ReadFull(conn, addr); err != nil {
			return "", err
		}
		host = net.IP(addr).String()
	case 0x03:
		dLen := make([]byte, 1)
		if _, err := io.ReadFull(conn, dLen); err != nil {
			return "", err
		}
		domain := make([]byte, dLen[0])
		if _, err := io.ReadFull(conn, domain); err != nil {
			return "", err
		}
		host = string(domain)
		if strings.ContainsAny(host, "\r\n\x00") {
			return "", fmt.Errorf("socks5 invalid hostname: %q", host)
		}
	case 0x04:
		addr := make([]byte, 16)
		if _, err := io.ReadFull(conn, addr); err != nil {
			return "", err
		}
		host = net.IP(addr).String()
	default:
		writeSocks5Reply(conn, 0x08) // address type not supported
		return "", fmt.Errorf("socks5 unsupported address type: %d", req[3])
	}

	portBuf := make([]byte, 2)
	if _, err := io.ReadFull(conn, portBuf); err != nil {
		return "", err
	}
	port := binary.BigEndian.Uint16(portBuf)
	target := net.JoinHostPort(host, strconv.Itoa(int(port)))

	// Success reply is sent by the caller after upstream connection is established.
	return target, nil
}

// writeSocks5Reply sends a SOCKS5 reply with the given status code.
// Status codes: 0x00 success, 0x04 host unreachable, 0x05 connection refused, etc.
func writeSocks5Reply(conn net.Conn, status byte) error {
	reply := []byte{0x05, status, 0x00, 0x01, 0, 0, 0, 0, 0, 0}
	_, err := conn.Write(reply)
	return err
}
