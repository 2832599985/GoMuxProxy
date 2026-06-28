package proxy

import (
	"bufio"
	"fmt"
	"net"
	"net/http"
	"time"
)

func handleHTTPProxy(conn net.Conn, upstreamProxy string) (string, error) {
	br := bufio.NewReader(conn)
	req, err := http.ReadRequest(br)
	if err != nil {
		return "", fmt.Errorf("http read request: %w", err)
	}

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
		resp.Header.Set("Proxy-Agent", "GoMuxProxy")
		if err := resp.Write(conn); err != nil {
			return "", err
		}
		return host, nil
	}

	host := req.Host
	if !hasPort(host) {
		host = net.JoinHostPort(host, "80")
	}

	upstream, err := net.DialTimeout("tcp", upstreamProxy, 10*time.Second)
	if err != nil {
		return "", fmt.Errorf("dial upstream: %w", err)
	}
	defer upstream.Close()

	connectReq := fmt.Sprintf("CONNECT %s HTTP/1.1\r\nHost: %s\r\n\r\n", host, host)
	if _, err := upstream.Write([]byte(connectReq)); err != nil {
		return "", err
	}

	upstreamBR := bufio.NewReader(upstream)
	resp, err := http.ReadResponse(upstreamBR, nil)
	if err != nil {
		return "", fmt.Errorf("upstream read response: %w", err)
	}
	if resp.StatusCode != 200 {
		return "", fmt.Errorf("upstream CONNECT failed: %d", resp.StatusCode)
	}

	if err := req.Write(upstream); err != nil {
		return "", err
	}

	tunnel(conn, upstream)
	return "", nil
}
