package proxy

import (
	"bufio"
	"fmt"
	"net"
	"net/http"
	"strings"
	"time"
)

func handleHTTPProxy(conn net.Conn, upstreamProxy string, upstreamTimeout int) (string, *bufio.Reader, error) {
	// Slow-client protection: 30s deadline for the initial handshake read.
	conn.SetReadDeadline(time.Now().Add(30 * time.Second))

	br := bufio.NewReader(conn)
	req, err := http.ReadRequest(br)
	if err != nil {
		return "", nil, fmt.Errorf("http read request: %w", err)
	}

	// Clear deadline after successful read.
	conn.SetReadDeadline(time.Time{})

	if req.Method == http.MethodConnect {
		host := req.Host
		if !hasPort(host) {
			host = net.JoinHostPort(host, "443")
		}
		resp := &http.Response{
			StatusCode: 200,
			Proto:      "HTTP/1.1",
			ProtoMajor: 1,
			ProtoMinor: 1,
			Header:     make(http.Header),
		}
		if err := resp.Write(conn); err != nil {
			return "", nil, err
		}
		// Return br so the caller can use it for tunnelling;
		// any bytes already buffered from the client are preserved.
		return host, br, nil
	}

	// Plain HTTP request — forward entirely through the upstream CONNECT tunnel.
	host := req.Host
	if !hasPort(host) {
		host = net.JoinHostPort(host, "80")
	}

	upstream, err := net.DialTimeout("tcp", upstreamProxy, time.Duration(upstreamTimeout)*time.Second)
	if err != nil {
		conn.Write([]byte("HTTP/1.1 502 Bad Gateway\r\n\r\n"))
		return "", nil, fmt.Errorf("dial upstream: %w", err)
	}
	defer upstream.Close()

	var connectReq strings.Builder
	connectReq.WriteString("CONNECT ")
	connectReq.WriteString(host)
	connectReq.WriteString(" HTTP/1.1\r\nHost: ")
	connectReq.WriteString(host)
	connectReq.WriteString("\r\n\r\n")
	if _, err := upstream.Write([]byte(connectReq.String())); err != nil {
		conn.Write([]byte("HTTP/1.1 502 Bad Gateway\r\n\r\n"))
		return "", nil, err
	}

	upstreamBR := bufio.NewReader(upstream)
	resp, err := http.ReadResponse(upstreamBR, nil)
	if err != nil {
		conn.Write([]byte("HTTP/1.1 502 Bad Gateway\r\n\r\n"))
		return "", nil, fmt.Errorf("upstream read response: %w", err)
	}
	if resp.StatusCode != 200 {
		conn.Write([]byte("HTTP/1.1 502 Bad Gateway\r\n\r\n"))
		return "", nil, fmt.Errorf("upstream CONNECT failed: %d", resp.StatusCode)
	}

	if err := req.Write(upstream); err != nil {
		return "", nil, err
	}

	tunnelCopyPooled(conn, upstream)
	return "", nil, nil
}
