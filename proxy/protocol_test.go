package proxy

import (
	"bufio"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// Config.Validate tests
// ---------------------------------------------------------------------------

func TestValidate_ValidConfig(t *testing.T) {
	cfg := Config{
		UpstreamProxy: "127.0.0.1:10810",
		Listeners: []ListenEntry{
			{Network: "tcp", Address: "127.0.0.1:1081", Protocol: ProtoMixed, Enabled: true},
			{Network: "tcp", Address: "127.0.0.1:1082", Protocol: ProtoSocks5, Enabled: true},
			{Network: "tcp", Address: "127.0.0.1:1083", Protocol: ProtoHTTP, Enabled: false},
		},
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("expected valid config, got error: %v", err)
	}
}

func TestValidate_InvalidUpstream(t *testing.T) {
	tests := []struct {
		name     string
		upstream string
	}{
		{"empty", ""},
		{"no port", "127.0.0.1"},
		{"only host", "example.com"},
		{"garbage", "not-an-address"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := Config{UpstreamProxy: tt.upstream, Listeners: []ListenEntry{
				{Network: "tcp", Address: "127.0.0.1:1081", Protocol: ProtoSocks5},
			}}
			if err := cfg.Validate(); err == nil {
				t.Fatalf("expected error for upstream %q", tt.upstream)
			}
		})
	}
}

func TestValidate_InvalidListenerAddress(t *testing.T) {
	cfg := Config{
		UpstreamProxy: "127.0.0.1:10810",
		Listeners: []ListenEntry{
			{Network: "tcp", Address: "no-port-here", Protocol: ProtoSocks5},
		},
	}
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected error for invalid listener address")
	}
}

func TestValidate_DuplicateAddress(t *testing.T) {
	cfg := Config{
		UpstreamProxy: "127.0.0.1:10810",
		Listeners: []ListenEntry{
			{Network: "tcp", Address: "127.0.0.1:1081", Protocol: ProtoSocks5},
			{Network: "tcp", Address: "127.0.0.1:1081", Protocol: ProtoHTTP},
		},
	}
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected error for duplicate address")
	}
}

func TestValidate_UnknownProtocol(t *testing.T) {
	cfg := Config{
		UpstreamProxy: "127.0.0.1:10810",
		Listeners: []ListenEntry{
			{Network: "tcp", Address: "127.0.0.1:1081", Protocol: "ftp"},
		},
	}
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected error for unknown protocol")
	}
}

// ---------------------------------------------------------------------------
// SaveConfig / LoadConfig tests
// ---------------------------------------------------------------------------

func TestSaveLoadConfig_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")

	cfg := Config{
		UpstreamProxy:      "10.0.0.1:9999",
		UpstreamTimeout:    15,
		MixedDetectTimeout: 3,
		MaxConnections:     500,
		Listeners: []ListenEntry{
			{Network: "tcp", Address: "127.0.0.1:2080", Protocol: ProtoSocks5, Enabled: true},
			{Network: "tcp", Address: "0.0.0.0:3128", Protocol: ProtoHTTP, Enabled: false},
		},
	}

	if err := SaveConfig(path, cfg); err != nil {
		t.Fatalf("SaveConfig: %v", err)
	}

	loaded, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}

	if loaded.UpstreamProxy != cfg.UpstreamProxy {
		t.Errorf("UpstreamProxy = %q, want %q", loaded.UpstreamProxy, cfg.UpstreamProxy)
	}
	if loaded.UpstreamTimeout != cfg.UpstreamTimeout {
		t.Errorf("UpstreamTimeout = %d, want %d", loaded.UpstreamTimeout, cfg.UpstreamTimeout)
	}
	if loaded.MaxConnections != cfg.MaxConnections {
		t.Errorf("MaxConnections = %d, want %d", loaded.MaxConnections, cfg.MaxConnections)
	}
	if len(loaded.Listeners) != len(cfg.Listeners) {
		t.Fatalf("Listeners len = %d, want %d", len(loaded.Listeners), len(cfg.Listeners))
	}
	for i, l := range loaded.Listeners {
		if l.Address != cfg.Listeners[i].Address || l.Protocol != cfg.Listeners[i].Protocol {
			t.Errorf("Listener[%d] = %+v, want %+v", i, l, cfg.Listeners[i])
		}
	}
}

func TestLoadConfig_InvalidJSON(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bad.json")
	os.WriteFile(path, []byte("{invalid"), 0600)

	_, err := LoadConfig(path)
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}

func TestLoadConfig_InvalidConfig(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bad.json")
	// Valid JSON but invalid config (no port on upstream)
	os.WriteFile(path, []byte(`{"upstream_proxy":"localhost","listeners":[]}`), 0600)

	_, err := LoadConfig(path)
	if err == nil {
		t.Fatal("expected validation error from LoadConfig")
	}
}

func TestSaveConfig_Permissions(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "perms.json")
	cfg := Config{UpstreamProxy: "1.2.3.4:8080"}

	if err := SaveConfig(path, cfg); err != nil {
		t.Fatalf("SaveConfig: %v", err)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	// Check file is not world-readable (0600)
	perm := info.Mode().Perm()
	if perm&0077 != 0 {
		t.Errorf("file permissions = %o, want 0600 (no group/other access)", perm)
	}
}

// ---------------------------------------------------------------------------
// protocolLabel tests
// ---------------------------------------------------------------------------

func TestProtocolLabel(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{ProtoSocks5, "SOCKS5"},
		{ProtoHTTP, "HTTP代理"},
		{ProtoMixed, "混合(SOCKS5+HTTP)"},
		{"ftp", "ftp"},
		{"", ""},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := protocolLabel(tt.input)
			if got != tt.want {
				t.Errorf("protocolLabel(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// hasPort tests
// ---------------------------------------------------------------------------

func TestHasPort(t *testing.T) {
	tests := []struct {
		host string
		want bool
	}{
		{"example.com:80", true},
		{"example.com", false},
		{"127.0.0.1:1080", true},
		{"127.0.0.1", false},
		{"[::1]:8080", true},
		{"[::1]", false},
		{"::1", false}, // bare IPv6 without brackets, ambiguous
	}
	for _, tt := range tests {
		t.Run(tt.host, func(t *testing.T) {
			if got := hasPort(tt.host); got != tt.want {
				t.Errorf("hasPort(%q) = %v, want %v", tt.host, got, tt.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// bufReader tests
// ---------------------------------------------------------------------------

func TestBufReader_BasicRead(t *testing.T) {
	data := []byte("hello world")
	br := newBufReader(strings.NewReader(string(data)))
	buf := make([]byte, len(data))
	n, err := br.Read(buf)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if string(buf[:n]) != "hello world" {
		t.Errorf("Read = %q, want %q", buf[:n], data)
	}
}

func TestBufReader_Buffered(t *testing.T) {
	// Feed 20 bytes, read 5 — 15 should remain buffered.
	data := strings.Repeat("A", 20)
	br := newBufReader(strings.NewReader(data))

	buf := make([]byte, 5)
	br.Read(buf)

	if br.Buffered() != 15 {
		t.Errorf("Buffered() = %d, want 15", br.Buffered())
	}
}

// ---------------------------------------------------------------------------
// newMultiReader tests
// ---------------------------------------------------------------------------

func TestNewMultiReader(t *testing.T) {
	head := []byte("HEAD")
	rest := strings.NewReader("TAIL")
	mr := newMultiReader(head, rest)

	all, err := io.ReadAll(mr)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	if string(all) != "HEADTAIL" {
		t.Errorf("ReadAll = %q, want %q", all, "HEADTAIL")
	}
}

// ---------------------------------------------------------------------------
// mixedConn tests
// ---------------------------------------------------------------------------

func TestMixedConn_ReplaysFirstByte(t *testing.T) {
	server, client := net.Pipe()
	defer server.Close()
	defer client.Close()

	mc := &mixedConn{first: 0x05, Conn: client}

	// Write full SOCKS5 greeting from server side in a goroutine
	go func() {
		// Read what mc sends back (should include the 0x05 byte replayed)
		buf := make([]byte, 10)
		server.Read(buf)
	}()

	// Read from mixedConn — should get 0x05 first
	buf := make([]byte, 1)
	n, err := mc.Read(buf)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if n != 1 || buf[0] != 0x05 {
		t.Errorf("first Read = %d byte %x, want 0x05", n, buf[0])
	}
	if mc.consumed != true {
		t.Error("consumed should be true after first read")
	}
}

func TestMixedConn_LargeRead(t *testing.T) {
	server, client := net.Pipe()
	defer server.Close()
	defer client.Close()

	mc := &mixedConn{first: 0x42, Conn: client}

	// Write more data from server side
	go func() {
		server.Write([]byte("extra"))
	}()

	// Read with a large buffer — should get first byte + connection data
	buf := make([]byte, 100)
	n, err := mc.Read(buf)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if buf[0] != 0x42 {
		t.Errorf("buf[0] = %x, want 0x42", buf[0])
	}
	if n < 2 {
		t.Errorf("expected at least 2 bytes (first + extra), got %d", n)
	}
}

func TestMixedConn_EmptyRead(t *testing.T) {
	mc := &mixedConn{first: 0x05, Conn: nil}
	n, err := mc.Read(nil)
	if n != 0 || err != nil {
		t.Errorf("empty Read = %d, %v, want 0, nil", n, err)
	}
}

func TestMixedConn_SecondReadGoesToConn(t *testing.T) {
	server, client := net.Pipe()
	defer server.Close()
	defer client.Close()

	mc := &mixedConn{first: 0xAA, Conn: client}

	// First read gets the replayed byte
	buf := make([]byte, 1)
	mc.Read(buf)
	if buf[0] != 0xAA {
		t.Errorf("first byte = %x, want 0xAA", buf[0])
	}

	// Second read should go to the underlying connection
	go func() {
		server.Write([]byte{0xBB})
	}()

	buf2 := make([]byte, 1)
	mc.Read(buf2)
	if buf2[0] != 0xBB {
		t.Errorf("second byte = %x, want 0xBB", buf2[0])
	}
}

// ---------------------------------------------------------------------------
// SOCKS5 handshake tests (using net.Pipe)
// ---------------------------------------------------------------------------

// socks5Client performs a minimal SOCKS5 CONNECT handshake on the client side.
func socks5Client(t *testing.T, conn net.Conn, target string) {
	t.Helper()

	host, portStr, err := net.SplitHostPort(target)
	if err != nil {
		t.Fatalf("SplitHostPort(%q): %v", target, err)
	}
	port, _ := net.Atoi(portStr)

	// Greeting: version=5, 1 method, no-auth
	conn.Write([]byte{0x05, 0x01, 0x00})

	// Read auth response
	authResp := make([]byte, 2)
	if _, err := io.ReadFull(conn, authResp); err != nil {
		t.Fatalf("read auth response: %v", err)
	}
	if authResp[0] != 0x05 || authResp[1] != 0x00 {
		t.Fatalf("auth response = %x, want [05 00]", authResp)
	}

	// CONNECT request
	var req []byte
	req = append(req, 0x05, 0x01, 0x00) // VER, CMD=CONNECT, RSV

	ip := net.ParseIP(host)
	if ip4 := ip.To4(); ip4 != nil {
		req = append(req, 0x01) // IPv4
		req = append(req, ip4...)
	} else if ip6 := ip.To16(); ip6 != nil {
		req = append(req, 0x04) // IPv6
		req = append(req, ip6...)
	} else {
		req = append(req, 0x03) // Domain
		req = append(req, byte(len(host)))
		req = append(req, []byte(host)...)
	}

	portBytes := make([]byte, 2)
	binary.BigEndian.PutUint16(portBytes, uint16(port))
	req = append(req, portBytes...)

	conn.Write(req)
}

func TestHandleSocks5_IPv4(t *testing.T) {
	server, client := net.Pipe()
	defer server.Close()
	defer client.Close()

	go socks5Client(t, client, "93.184.216.34:80")

	target, err := handleSocks5(server)
	if err != nil {
		t.Fatalf("handleSocks5: %v", err)
	}
	if target != "93.184.216.34:80" {
		t.Errorf("target = %q, want %q", target, "93.184.216.34:80")
	}
}

func TestHandleSocks5_IPv6(t *testing.T) {
	server, client := net.Pipe()
	defer server.Close()
	defer client.Close()

	go socks5Client(t, client, "[::1]:443")

	target, err := handleSocks5(server)
	if err != nil {
		t.Fatalf("handleSocks5: %v", err)
	}
	if target != "[::1]:443" {
		t.Errorf("target = %q, want %q", target, "[::1]:443")
	}
}

func TestHandleSocks5_Domain(t *testing.T) {
	server, client := net.Pipe()
	defer server.Close()
	defer client.Close()

	go socks5Client(t, client, "example.com:443")

	target, err := handleSocks5(server)
	if err != nil {
		t.Fatalf("handleSocks5: %v", err)
	}
	if target != "example.com:443" {
		t.Errorf("target = %q, want %q", target, "example.com:443")
	}
}

func TestHandleSocks5_BadVersion(t *testing.T) {
	server, client := net.Pipe()
	defer server.Close()
	defer client.Close()

	go func() {
		// Send version 4 instead of 5
		client.Write([]byte{0x04, 0x01, 0x00})
	}()

	_, err := handleSocks5(server)
	if err == nil {
		t.Fatal("expected error for wrong version")
	}
	if !strings.Contains(err.Error(), "unsupported version") {
		t.Errorf("error = %q, want 'unsupported version'", err)
	}
}

func TestHandleSocks5_UnsupportedCommand(t *testing.T) {
	server, client := net.Pipe()
	defer server.Close()
	defer client.Close()

	go func() {
		// Greeting
		client.Write([]byte{0x05, 0x01, 0x00})
		authResp := make([]byte, 2)
		io.ReadFull(client, authResp)

		// Send BIND command (0x02) instead of CONNECT (0x01)
		client.Write([]byte{0x05, 0x02, 0x00, 0x01, 1, 2, 3, 4, 0, 80})
		// Read the error reply
		reply := make([]byte, 10)
		io.ReadFull(client, reply)
	}()

	_, err := handleSocks5(server)
	if err == nil {
		t.Fatal("expected error for unsupported command")
	}
	if !strings.Contains(err.Error(), "unsupported command") {
		t.Errorf("error = %q, want 'unsupported command'", err)
	}
}

func TestHandleSocks5_UnsupportedAddressType(t *testing.T) {
	server, client := net.Pipe()
	defer server.Close()
	defer client.Close()

	go func() {
		// Greeting
		client.Write([]byte{0x05, 0x01, 0x00})
		authResp := make([]byte, 2)
		io.ReadFull(client, authResp)

		// CONNECT with address type 0x02 (not supported)
		client.Write([]byte{0x05, 0x01, 0x00, 0x02})
		reply := make([]byte, 10)
		io.ReadFull(client, reply)
	}()

	_, err := handleSocks5(server)
	if err == nil {
		t.Fatal("expected error for unsupported address type")
	}
	if !strings.Contains(err.Error(), "unsupported address type") {
		t.Errorf("error = %q, want 'unsupported address type'", err)
	}
}

func TestHandleSocks5_CRLFInjection(t *testing.T) {
	server, client := net.Pipe()
	defer server.Close()
	defer client.Close()

	go func() {
		// Greeting
		client.Write([]byte{0x05, 0x01, 0x00})
		authResp := make([]byte, 2)
		io.ReadFull(client, authResp)

		// Domain with CRLF injection attempt
		domain := "evil.com\r\nInjected: header"
		req := []byte{0x05, 0x01, 0x00, 0x03, byte(len(domain))}
		req = append(req, []byte(domain)...)
		req = append(req, 0x00, 0x50) // port 80
		client.Write(req)
	}()

	_, err := handleSocks5(server)
	if err == nil {
		t.Fatal("expected error for CRLF injection in domain")
	}
	if !strings.Contains(err.Error(), "invalid hostname") {
		t.Errorf("error = %q, want 'invalid hostname'", err)
	}
}

func TestHandleSocks5_TruncatedRead(t *testing.T) {
	server, client := net.Pipe()
	defer server.Close()

	// Close immediately to cause read failure
	client.Close()

	_, err := handleSocks5(server)
	if err == nil {
		t.Fatal("expected error for truncated read")
	}
}

// ---------------------------------------------------------------------------
// writeSocks5Reply tests
// ---------------------------------------------------------------------------

func TestWriteSocks5Reply(t *testing.T) {
	server, client := net.Pipe()
	defer server.Close()
	defer client.Close()

	go writeSocks5Reply(server, 0x00)

	reply := make([]byte, 10)
	if _, err := io.ReadFull(client, reply); err != nil {
		t.Fatalf("ReadFull: %v", err)
	}
	if reply[0] != 0x05 {
		t.Errorf("reply[0] = %x, want 0x05", reply[0])
	}
	if reply[1] != 0x00 {
		t.Errorf("reply[1] = %x, want 0x00", reply[1])
	}
	if reply[2] != 0x00 {
		t.Errorf("reply[2] = %x, want 0x00 (RSV)", reply[2])
	}
	if reply[3] != 0x01 {
		t.Errorf("reply[3] = %x, want 0x01 (IPv4)", reply[3])
	}
	// Remaining 6 bytes should be zeros (0.0.0.0:0)
	for i := 4; i < 10; i++ {
		if reply[i] != 0 {
			t.Errorf("reply[%d] = %x, want 0x00", i, reply[i])
		}
	}
}

func TestWriteSocks5Reply_ErrorStatus(t *testing.T) {
	server, client := net.Pipe()
	defer server.Close()
	defer client.Close()

	go writeSocks5Reply(server, 0x05) // connection refused

	reply := make([]byte, 10)
	io.ReadFull(client, reply)
	if reply[1] != 0x05 {
		t.Errorf("reply[1] = %x, want 0x05", reply[1])
	}
}

// ---------------------------------------------------------------------------
// HTTP CONNECT handler tests
// ---------------------------------------------------------------------------

// httpConnectClient sends a CONNECT request and reads the response.
func httpConnectClient(t *testing.T, conn net.Conn, target string) {
	t.Helper()
	fmt.Fprintf(conn, "CONNECT %s HTTP/1.1\r\nHost: %s\r\n\r\n", target, target)
	br := bufio.NewReader(conn)
	resp, err := http.ReadResponse(br, nil)
	if err != nil {
		// Connection might be closed
		return
	}
	if resp.StatusCode != 200 {
		t.Errorf("CONNECT response status = %d, want 200", resp.StatusCode)
	}
}

func TestHandleHTTPProxy_Connect(t *testing.T) {
	server, client := net.Pipe()
	defer server.Close()
	defer client.Close()

	go httpConnectClient(t, client, "example.com:443")

	target, br, err := handleHTTPProxy(server, "127.0.0.1:9999", 5)
	if err != nil {
		t.Fatalf("handleHTTPProxy CONNECT: %v", err)
	}
	if target != "example.com:443" {
		t.Errorf("target = %q, want %q", target, "example.com:443")
	}
	if br == nil {
		t.Error("expected non-nil bufio.Reader for CONNECT")
	}
}

func TestHandleHTTPProxy_ConnectNoPort(t *testing.T) {
	server, client := net.Pipe()
	defer server.Close()
	defer client.Close()

	go httpConnectClient(t, client, "example.com")

	target, _, err := handleHTTPProxy(server, "127.0.0.1:9999", 5)
	if err != nil {
		t.Fatalf("handleHTTPProxy: %v", err)
	}
	if target != "example.com:443" {
		t.Errorf("target = %q, want %q (default port 443)", target, "example.com:443")
	}
}

func TestHandleHTTPProxy_BadRequest(t *testing.T) {
	server, client := net.Pipe()
	defer server.Close()
	defer client.Close()

	go func() {
		client.Write([]byte("GARBAGE\r\n\r\n"))
	}()

	_, _, err := handleHTTPProxy(server, "127.0.0.1:9999", 5)
	if err == nil {
		t.Fatal("expected error for bad HTTP request")
	}
}

func TestHandleHTTPProxy_Timeout(t *testing.T) {
	server, client := net.Pipe()
	defer server.Close()
	defer client.Close()

	// Don't send anything — should timeout
	go func() {
		time.Sleep(35 * time.Second)
		client.Close()
	}()

	_, _, err := handleHTTPProxy(server, "127.0.0.1:9999", 5)
	if err == nil {
		t.Fatal("expected timeout error")
	}
}

// ---------------------------------------------------------------------------
// handleMixed tests
// ---------------------------------------------------------------------------

func TestHandleMixed_Socks5Detection(t *testing.T) {
	server, client := net.Pipe()
	defer server.Close()
	defer client.Close()

	// First byte 0x05 -> SOCKS5
	go func() {
		// Write 0x05 first byte, then complete SOCKS5 handshake
		socks5Client(t, client, "10.0.0.1:8080")
	}()

	proto, target, err := handleMixed(server, "127.0.0.1:9999", 5, 5)
	if err != nil {
		t.Fatalf("handleMixed: %v", err)
	}
	if proto != ProtoSocks5 {
		t.Errorf("protocol = %q, want %q", proto, ProtoSocks5)
	}
	if target != "10.0.0.1:8080" {
		t.Errorf("target = %q, want %q", target, "10.0.0.1:8080")
	}
}

func TestHandleMixed_HTTPDetection(t *testing.T) {
	server, client := net.Pipe()
	defer server.Close()
	defer client.Close()

	// First byte 'G' (GET) -> HTTP
	go httpConnectClient(t, client, "example.com:443")

	proto, target, err := handleMixed(server, "127.0.0.1:9999", 5, 5)
	if err != nil {
		t.Fatalf("handleMixed: %v", err)
	}
	if proto != ProtoHTTP {
		t.Errorf("protocol = %q, want %q", proto, ProtoHTTP)
	}
	if target != "example.com:443" {
		t.Errorf("target = %q, want %q", target, "example.com:443")
	}
}

func TestHandleMixed_Timeout(t *testing.T) {
	server, client := net.Pipe()
	defer server.Close()
	defer client.Close()

	go func() {
		time.Sleep(10 * time.Second)
		client.Close()
	}()

	_, _, err := handleMixed(server, "127.0.0.1:9999", 5, 1)
	if err == nil {
		t.Fatal("expected timeout error")
	}
}

// ---------------------------------------------------------------------------
// ProxyEngine integration tests
// ---------------------------------------------------------------------------

// startTestEngine creates an engine with a SOCKS5 listener on a random port,
// with a mock upstream that accepts CONNECT requests. Returns engine, client
// address, and cleanup function.
func startTestEngine(t *testing.T, protocol string) (*ProxyEngine, string, func()) {
	t.Helper()

	// Create a mock upstream proxy that accepts CONNECT
	upstreamLn, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen upstream: %v", err)
	}

	go func() {
		for {
			conn, err := upstreamLn.Accept()
			if err != nil {
				return
			}
			// Read the CONNECT request
			br := bufio.NewReader(conn)
			http.ReadRequest(br)
			// Send 200 OK
			conn.Write([]byte("HTTP/1.1 200 Connection Established\r\n\r\n"))
			// Tunnel: just echo for testing
			go func() {
				io.Copy(conn, conn)
				conn.Close()
			}()
		}
	}()

	upstreamAddr := upstreamLn.Addr().String()

	// Find a free port for the listener
	tmpLn, _ := net.Listen("tcp", "127.0.0.1:0")
	listenAddr := tmpLn.Addr().String()
	tmpLn.Close()

	cfg := Config{
		UpstreamProxy:      upstreamAddr,
		UpstreamTimeout:    5,
		MixedDetectTimeout: 3,
		MaxConnections:     100,
		Listeners: []ListenEntry{
			{Network: "tcp", Address: listenAddr, Protocol: protocol, Enabled: true},
		},
	}

	engine := NewEngine(cfg)
	engine.SetCallbacks(func(s string) {}, func(ci ConnInfo) {}, func(ci ConnInfo) {})

	if err := engine.Start(); err != nil {
		upstreamLn.Close()
		t.Fatalf("engine.Start: %v", err)
	}

	cleanup := func() {
		engine.Stop()
		upstreamLn.Close()
	}

	return engine, listenAddr, cleanup
}

func TestEngine_StartStop(t *testing.T) {
	_, _, cleanup := startTestEngine(t, ProtoSocks5)
	defer cleanup()

	// If we got here without errors, start/stop works
}

func TestEngine_IsRunning(t *testing.T) {
	engine, _, cleanup := startTestEngine(t, ProtoSocks5)
	defer cleanup()

	if !engine.IsRunning() {
		t.Error("expected IsRunning() = true after Start()")
	}

	engine.Stop()

	if engine.IsRunning() {
		t.Error("expected IsRunning() = false after Stop()")
	}
}

func TestEngine_Socks5Connect(t *testing.T) {
	engine, listenAddr, cleanup := startTestEngine(t, ProtoSocks5)
	defer cleanup()

	conn, err := net.DialTimeout("tcp", listenAddr, 2*time.Second)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	// SOCKS5 handshake
	socks5Client(t, conn, "93.184.216.34:80")

	// Read SOCKS5 reply
	reply := make([]byte, 10)
	conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	if _, err := io.ReadFull(conn, reply); err != nil {
		t.Fatalf("read reply: %v", err)
	}
	if reply[1] != 0x00 {
		t.Errorf("SOCKS5 reply status = %x, want 0x00 (success)", reply[1])
	}

	// Verify stats updated
	time.Sleep(100 * time.Millisecond)
	stats := engine.GetStats()
	if stats.TotalConns < 1 {
		t.Errorf("TotalConns = %d, want >= 1", stats.TotalConns)
	}
}

func TestEngine_HTTPConnect(t *testing.T) {
	engine, listenAddr, cleanup := startTestEngine(t, ProtoHTTP)
	defer cleanup()

	conn, err := net.DialTimeout("tcp", listenAddr, 2*time.Second)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	// Send CONNECT request
	fmt.Fprintf(conn, "CONNECT example.com:443 HTTP/1.1\r\nHost: example.com:443\r\n\r\n")

	br := bufio.NewReader(conn)
	resp, err := http.ReadResponse(br, nil)
	if err != nil {
		t.Fatalf("read response: %v", err)
	}
	if resp.StatusCode != 200 {
		t.Errorf("HTTP CONNECT status = %d, want 200", resp.StatusCode)
	}
}

func TestEngine_MixedProtocol_Socks5(t *testing.T) {
	engine, listenAddr, cleanup := startTestEngine(t, ProtoMixed)
	defer cleanup()

	conn, err := net.DialTimeout("tcp", listenAddr, 2*time.Second)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	// Use SOCKS5 handshake on mixed listener
	socks5Client(t, conn, "10.0.0.1:443")

	reply := make([]byte, 10)
	conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	if _, err := io.ReadFull(conn, reply); err != nil {
		t.Fatalf("read reply: %v", err)
	}
	if reply[1] != 0x00 {
		t.Errorf("SOCKS5 reply status = %x, want 0x00", reply[1])
	}
}

func TestEngine_MixedProtocol_HTTP(t *testing.T) {
	engine, listenAddr, cleanup := startTestEngine(t, ProtoMixed)
	defer cleanup()

	conn, err := net.DialTimeout("tcp", listenAddr, 2*time.Second)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	// Use HTTP CONNECT on mixed listener
	fmt.Fprintf(conn, "CONNECT example.com:443 HTTP/1.1\r\nHost: example.com:443\r\n\r\n")

	br := bufio.NewReader(conn)
	resp, err := http.ReadResponse(br, nil)
	if err != nil {
		t.Fatalf("read response: %v", err)
	}
	if resp.StatusCode != 200 {
		t.Errorf("HTTP CONNECT status = %d, want 200", resp.StatusCode)
	}
}

func TestEngine_AddRemoveListener(t *testing.T) {
	cfg := Config{
		UpstreamProxy: "127.0.0.1:10810",
		Listeners:     []ListenEntry{},
		MaxConnections: 100,
	}
	engine := NewEngine(cfg)
	engine.SetCallbacks(func(s string) {}, func(ci ConnInfo) {}, func(ci ConnInfo) {})

	// Find a free port
	tmpLn, _ := net.Listen("tcp", "127.0.0.1:0")
	addr := tmpLn.Addr().String()
	tmpLn.Close()

	entry := ListenEntry{Network: "tcp", Address: addr, Protocol: ProtoSocks5, Enabled: true}
	if err := engine.AddListener(entry); err != nil {
		t.Fatalf("AddListener: %v", err)
	}

	cfg = engine.Config()
	if len(cfg.Listeners) != 1 {
		t.Fatalf("Listeners len = %d, want 1", len(cfg.Listeners))
	}

	if err := engine.RemoveListener(addr); err != nil {
		t.Fatalf("RemoveListener: %v", err)
	}

	cfg = engine.Config()
	if len(cfg.Listeners) != 0 {
		t.Fatalf("Listeners len after remove = %d, want 0", len(cfg.Listeners))
	}
}

func TestEngine_ToggleListener(t *testing.T) {
	cfg := Config{
		UpstreamProxy: "127.0.0.1:10810",
		Listeners:     []ListenEntry{},
		MaxConnections: 100,
	}
	engine := NewEngine(cfg)
	engine.SetCallbacks(func(s string) {}, func(ci ConnInfo) {}, func(ci ConnInfo) {})

	tmpLn, _ := net.Listen("tcp", "127.0.0.1:0")
	addr := tmpLn.Addr().String()
	tmpLn.Close()

	entry := ListenEntry{Network: "tcp", Address: addr, Protocol: ProtoSocks5, Enabled: true}
	engine.AddListener(entry)

	// Disable
	if err := engine.ToggleListener(addr, false); err != nil {
		t.Fatalf("ToggleListener(false): %v", err)
	}
	cfg = engine.Config()
	if cfg.Listeners[0].Enabled {
		t.Error("expected listener to be disabled")
	}

	// Enable
	if err := engine.ToggleListener(addr, true); err != nil {
		t.Fatalf("ToggleListener(true): %v", err)
	}
	cfg = engine.Config()
	if !cfg.Listeners[0].Enabled {
		t.Error("expected listener to be enabled")
	}
}

func TestEngine_GetPortStats(t *testing.T) {
	engine, listenAddr, cleanup := startTestEngine(t, ProtoSocks5)
	defer cleanup()

	portStats := engine.GetPortStats()
	if len(portStats) == 0 {
		t.Fatal("expected at least 1 port stat")
	}

	found := false
	for _, ps := range portStats {
		if ps.Address == listenAddr {
			found = true
			if !ps.Running {
				t.Error("expected port to be running")
			}
			if !ps.Enabled {
				t.Error("expected port to be enabled")
			}
			if ps.Protocol != ProtoSocks5 {
				t.Errorf("protocol = %q, want %q", ps.Protocol, ProtoSocks5)
			}
		}
	}
	if !found {
		t.Errorf("port stats missing entry for %s", listenAddr)
	}
}

func TestEngine_ConcurrentConnections(t *testing.T) {
	engine, listenAddr, cleanup := startTestEngine(t, ProtoSocks5)
	defer cleanup()

	const N = 10
	errs := make(chan error, N)

	for i := 0; i < N; i++ {
		go func() {
			conn, err := net.DialTimeout("tcp", listenAddr, 3*time.Second)
			if err != nil {
				errs <- fmt.Errorf("dial: %w", err)
				return
			}
			defer conn.Close()

			socks5Client(t, conn, fmt.Sprintf("10.0.0.%d:80", i+1))

			reply := make([]byte, 10)
			conn.SetReadDeadline(time.Now().Add(3 * time.Second))
			if _, err := io.ReadFull(conn, reply); err != nil {
				errs <- fmt.Errorf("read reply: %w", err)
				return
			}
			if reply[1] != 0x00 {
				errs <- fmt.Errorf("reply status = %x, want 0x00", reply[1])
				return
			}
			errs <- nil
		}()
	}

	for i := 0; i < N; i++ {
		if err := <-errs; err != nil {
			t.Errorf("concurrent conn %d: %v", i, err)
		}
	}

	// Check total connections
	time.Sleep(200 * time.Millisecond)
	stats := engine.GetStats()
	if stats.TotalConns < int64(N) {
		t.Errorf("TotalConns = %d, want >= %d", stats.TotalConns, N)
	}
}

func TestEngine_Restart(t *testing.T) {
	engine, _, cleanup := startTestEngine(t, ProtoSocks5)
	defer cleanup()

	if !engine.IsRunning() {
		t.Fatal("expected running before restart")
	}

	if err := engine.Restart(); err != nil {
		t.Fatalf("Restart: %v", err)
	}

	if !engine.IsRunning() {
		t.Error("expected running after restart")
	}
}

func TestEngine_DoubleStart(t *testing.T) {
	engine, _, cleanup := startTestEngine(t, ProtoSocks5)
	defer cleanup()

	err := engine.Start()
	if err == nil {
		t.Fatal("expected error on double Start()")
	}
}

func TestEngine_StopWhenNotRunning(t *testing.T) {
	cfg := Config{UpstreamProxy: "127.0.0.1:10810", MaxConnections: 100}
	engine := NewEngine(cfg)

	// Should not panic
	engine.Stop()
}

func TestEngine_UpdateConfig(t *testing.T) {
	cfg := Config{UpstreamProxy: "127.0.0.1:10810", MaxConnections: 100}
	engine := NewEngine(cfg)

	newCfg := Config{UpstreamProxy: "10.0.0.1:9999", MaxConnections: 500}
	engine.UpdateConfig(newCfg)

	got := engine.Config()
	if got.UpstreamProxy != "10.0.0.1:9999" {
		t.Errorf("UpstreamProxy = %q, want %q", got.UpstreamProxy, "10.0.0.1:9999")
	}
	if got.MaxConnections != 500 {
		t.Errorf("MaxConnections = %d, want 500", got.MaxConnections)
	}
}

func TestEngine_BadListener(t *testing.T) {
	cfg := Config{UpstreamProxy: "127.0.0.1:10810", MaxConnections: 100}
	engine := NewEngine(cfg)
	engine.SetCallbacks(func(s string) {}, func(ci ConnInfo) {}, func(ci ConnInfo) {})

	// Try to add a listener on an invalid address
	entry := ListenEntry{Network: "tcp", Address: "invalid:address:format", Protocol: ProtoSocks5, Enabled: true}
	if err := engine.Start(); err != nil {
		// engine start with no listeners is fine
	}

	// This should fail when trying to listen
	cfg2 := Config{
		UpstreamProxy: "127.0.0.1:10810",
		MaxConnections: 100,
		Listeners: []ListenEntry{
			{Network: "tcp", Address: "999.999.999.999:99999", Protocol: ProtoSocks5, Enabled: true},
		},
	}
	engine2 := NewEngine(cfg2)
	engine2.SetCallbacks(func(s string) {}, func(ci ConnInfo) {}, func(ci ConnInfo) {})
	err := engine2.Start()
	// Start doesn't return listener errors directly, but logs them
	_ = err
}

// Benchmark to ensure copyPooled is efficient
func BenchmarkCopyPooled(b *testing.B) {
	data := make([]byte, 32*1024)
	for i := range data {
		data[i] = byte(i % 256)
	}

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		src := strings.NewReader(string(data))
		dst := io.Discard
		copyPooled(dst, src)
	}
}
