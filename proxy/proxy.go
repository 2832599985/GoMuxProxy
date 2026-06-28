package proxy

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const (
	ProtoSocks5 = "socks5"
	ProtoHTTP   = "http"
	ProtoMixed  = "mixed"
)

var bufPool = sync.Pool{New: func() any { b := make([]byte, 32*1024); return &b }}

func copyPooled(dst io.Writer, src io.Reader) (int64, error) {
	bp := bufPool.Get().(*[]byte)
	defer bufPool.Put(bp)
	return io.CopyBuffer(dst, src, *bp)
}

type ListenEntry struct {
	Network  string `json:"network"`
	Address  string `json:"address"`
	Protocol string `json:"protocol"` // "socks5", "http", "mixed"
	Enabled  bool   `json:"enabled"`
}

type ConnInfo struct {
	ID         int64
	Source     string
	Target     string
	Protocol   string
	ListenAddr string
	StartTime  time.Time
	BytesUp    int64
	BytesDown  int64
}

type Stats struct {
	TotalConns     int64
	ActiveConns    int64
	TotalBytesUp   int64
	TotalBytesDown int64
}

type PortStats struct {
	Address    string
	Protocol   string
	Running    bool
	Enabled    bool
	ActiveConn int64
}

type Config struct {
	UpstreamProxy      string        `json:"upstream_proxy"`
	Listeners          []ListenEntry `json:"listeners"`
	UpstreamTimeout    int           `json:"upstream_timeout,omitempty"`
	MixedDetectTimeout int           `json:"mixed_detect_timeout,omitempty"`
	MaxConnections     int           `json:"max_connections,omitempty"`
}

type ProxyEngine struct {
	mu             sync.RWMutex
	config         Config
	listeners      map[string]net.Listener // address -> listener, only for enabled entries
	activeConns    sync.Map
	nextConnID     atomic.Int64
	totalConns     atomic.Int64
	totalBytesUp   atomic.Int64
	totalBytesDown atomic.Int64
	running        atomic.Bool
	perPortCount   sync.Map // address -> *atomic.Int64

	onLog        func(string)
	onConnect    func(ConnInfo)
	onDisconnect func(ConnInfo)

	serveWg        sync.WaitGroup
	restartMu      sync.Mutex
	connSemaphore  chan struct{}
}

func NewEngine(cfg Config) *ProxyEngine {
	maxConn := cfg.MaxConnections
	if maxConn <= 0 {
		maxConn = 1000
	}
	return &ProxyEngine{
		config:        cfg,
		listeners:     make(map[string]net.Listener),
		connSemaphore: make(chan struct{}, maxConn),
	}
}

func (e *ProxyEngine) SetCallbacks(onLog func(string), onConnect func(ConnInfo), onDisconnect func(ConnInfo)) {
	e.onLog = onLog
	e.onConnect = onConnect
	e.onDisconnect = onDisconnect
}

func (e *ProxyEngine) Config() Config {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.config
}

func (e *ProxyEngine) UpdateConfig(cfg Config) {
	e.mu.Lock()
	e.config = cfg
	e.mu.Unlock()
}

func (e *ProxyEngine) logf(format string, args ...interface{}) {
	msg := fmt.Sprintf(format, args...)
	log.Println(msg)
	if e.onLog != nil {
		e.onLog(msg)
	}
}

func (e *ProxyEngine) Start() error {
	if e.running.Load() {
		return fmt.Errorf("already running")
	}

	e.mu.Lock()
	cfg := e.config
	e.mu.Unlock()

	e.logf("启动代理引擎，上游: %s", cfg.UpstreamProxy)
	e.running.Store(true)

	for _, entry := range cfg.Listeners {
		if !entry.Enabled {
			e.logf("跳过已禁用的监听 %s", entry.Address)
			continue
		}
		if err := e.startOne(entry); err != nil {
			e.logf("监听 %s 失败: %v", entry.Address, err)
		}
	}

	return nil
}

func (e *ProxyEngine) startOne(entry ListenEntry) error {
	ln, err := net.Listen(entry.Network, entry.Address)
	if err != nil {
		return err
	}
	e.listeners[entry.Address] = ln
	e.serveWg.Add(1)
	go e.serve(ln, entry)
	e.logf("监听 %s 于 %s", protocolLabel(entry.Protocol), entry.Address)
	return nil
}

func (e *ProxyEngine) stopOne(address string) {
	if ln, ok := e.listeners[address]; ok {
		ln.Close()
		delete(e.listeners, address)
	}
}

func (e *ProxyEngine) Stop() {
	if !e.running.Load() {
		return
	}
	e.running.Store(false)

	e.mu.Lock()
	for addr := range e.listeners {
		e.listeners[addr].Close()
	}
	e.listeners = make(map[string]net.Listener)
	e.mu.Unlock()

	e.serveWg.Wait()
	e.logf("代理引擎已停止")
}

func (e *ProxyEngine) AddListener(entry ListenEntry) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	if e.running.Load() && entry.Enabled {
		if err := e.startOne(entry); err != nil {
			return err
		}
	}

	e.config.Listeners = append(e.config.Listeners, entry)
	e.logf("新增监听 %s 于 %s (启用: %v)", protocolLabel(entry.Protocol), entry.Address, entry.Enabled)
	return nil
}

func (e *ProxyEngine) RemoveListener(address string) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	idx := -1
	for i, l := range e.config.Listeners {
		if l.Address == address {
			idx = i
			break
		}
	}
	if idx == -1 {
		return fmt.Errorf("监听 %s 未找到", address)
	}

	e.stopOne(address)
	e.config.Listeners = append(e.config.Listeners[:idx], e.config.Listeners[idx+1:]...)
	e.logf("移除监听 %s", address)
	return nil
}

func (e *ProxyEngine) ToggleListener(address string, enabled bool) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	idx := -1
	for i, l := range e.config.Listeners {
		if l.Address == address {
			idx = i
			break
		}
	}
	if idx == -1 {
		return fmt.Errorf("监听 %s 未找到", address)
	}

	e.config.Listeners[idx].Enabled = enabled

	if enabled {
		if e.running.Load() {
			if err := e.startOne(e.config.Listeners[idx]); err != nil {
				return err
			}
		}
		e.logf("启用监听 %s", address)
	} else {
		e.stopOne(address)
		e.logf("禁用监听 %s", address)
	}
	return nil
}

func (e *ProxyEngine) Restart() error {
	e.restartMu.Lock()
	defer e.restartMu.Unlock()

	e.Stop()
	return e.Start()
}

func (e *ProxyEngine) IsRunning() bool {
	return e.running.Load()
}

func (e *ProxyEngine) GetStats() Stats {
	var active int64
	e.perPortCount.Range(func(_, v any) bool {
		active += v.(*atomic.Int64).Load()
		return true
	})
	return Stats{
		TotalConns:     e.totalConns.Load(),
		ActiveConns:    active,
		TotalBytesUp:   e.totalBytesUp.Load(),
		TotalBytesDown: e.totalBytesDown.Load(),
	}
}

func (e *ProxyEngine) GetPortStats() []PortStats {
	e.mu.RLock()
	defer e.mu.RUnlock()

	var result []PortStats
	for _, entry := range e.config.Listeners {
		_, listening := e.listeners[entry.Address]
		ps := PortStats{
			Address:  entry.Address,
			Protocol: entry.Protocol,
			Running:  listening,
			Enabled:  entry.Enabled,
		}
		if v, ok := e.perPortCount.Load(entry.Address); ok {
			ps.ActiveConn = v.(*atomic.Int64).Load()
		}
		result = append(result, ps)
	}
	return result
}

func (e *ProxyEngine) GetActiveConns() []ConnInfo {
	var conns []ConnInfo
	e.activeConns.Range(func(_, v interface{}) bool {
		ci := v.(*ConnInfo)
		conns = append(conns, *ci)
		return true
	})
	return conns
}

func (e *ProxyEngine) serve(ln net.Listener, entry ListenEntry) {
	defer e.serveWg.Done()
	for {
		conn, err := ln.Accept()
		if err != nil {
			if !e.running.Load() {
				return
			}
			e.logf("[%s] 接受连接错误: %v", entry.Address, err)
			continue
		}
		e.serveWg.Add(1)
		go func() {
			defer e.serveWg.Done()
			e.connSemaphore <- struct{}{}
			e.handleConn(conn, entry)
			<-e.connSemaphore
		}()
	}
}

func (e *ProxyEngine) handleConn(client net.Conn, entry ListenEntry) {
	defer client.Close()

	connID := e.nextConnID.Add(1)
	e.totalConns.Add(1)

	// Per-port atomic count
	ppVal, _ := e.perPortCount.LoadOrStore(entry.Address, &atomic.Int64{})
	ppCounter := ppVal.(*atomic.Int64)
	ppCounter.Add(1)
	defer ppCounter.Add(-1)

	ci := &ConnInfo{
		ID:         connID,
		Source:     client.RemoteAddr().String(),
		ListenAddr: entry.Address,
		StartTime:  time.Now(),
	}

	var target string
	var err error
	var detectedProto string
	var httpBR *bufio.Reader // buffered reader from HTTP CONNECT, may contain client data

	// Snapshot upstream proxy under lock
	e.mu.RLock()
	upstream := e.config.UpstreamProxy
	upstreamTimeout := e.config.UpstreamTimeout
	detectTimeout := e.config.MixedDetectTimeout
	e.mu.RUnlock()

	if upstreamTimeout <= 0 {
		upstreamTimeout = 10
	}
	if detectTimeout <= 0 {
		detectTimeout = 5
	}

	switch entry.Protocol {
	case ProtoSocks5:
		ci.Protocol = "SOCKS5"
		target, err = handleSocks5(client)
		// Reply is sent below after connectViaUpstream succeeds or fails.
	case ProtoHTTP:
		ci.Protocol = "HTTP"
		target, httpBR, err = handleHTTPProxy(client, upstream, upstreamTimeout)
	case ProtoMixed:
		detectedProto, target, err = handleMixed(client, upstream, upstreamTimeout, detectTimeout)
		if detectedProto == ProtoSocks5 {
			ci.Protocol = "SOCKS5"
		} else {
			ci.Protocol = "HTTP"
		}
	default:
		err = fmt.Errorf("未知协议: %s", entry.Protocol)
	}

	if err != nil {
		if entry.Protocol == ProtoSocks5 || (entry.Protocol == ProtoMixed && detectedProto == ProtoSocks5) {
			writeSocks5Reply(client, 0x01) // general failure
		}
		e.logf("[%s] #%d 握手失败: %v", entry.Address, connID, err)
		return
	}

	// Plain HTTP proxy handles its own tunnel internally; nothing more to do.
	if entry.Protocol == ProtoHTTP && httpBR == nil && target == "" {
		return
	}

	ci.Target = target
	e.activeConns.Store(connID, ci)
	if e.onConnect != nil {
		e.onConnect(*ci)
	}
	e.logf("[%s] #%d %s -> %s (%s)", entry.Address, connID, ci.Source, target, ci.Protocol)

	isSocks5 := entry.Protocol == ProtoSocks5 || (entry.Protocol == ProtoMixed && detectedProto == ProtoSocks5)
	upstreamErr := e.connectViaUpstream(client, target, ci, upstream, upstreamTimeout, httpBR)

	if isSocks5 {
		if upstreamErr != nil {
			writeSocks5Reply(client, 0x05) // connection refused
		} else {
			writeSocks5Reply(client, 0x00) // success
		}
	}

	if upstreamErr != nil {
		e.logf("[%s] #%d 隧道错误: %v", entry.Address, connID, upstreamErr)
	}

	e.activeConns.Delete(connID)
	e.totalBytesUp.Add(ci.BytesUp)
	e.totalBytesDown.Add(ci.BytesDown)
	if e.onDisconnect != nil {
		e.onDisconnect(*ci)
	}
	e.logf("[%s] #%d 已断开 (上行:%d 下行:%d)", entry.Address, connID, ci.BytesUp, ci.BytesDown)
}

func (e *ProxyEngine) connectViaUpstream(client net.Conn, target string, ci *ConnInfo, upstreamAddr string, timeoutSec int, httpBR *bufio.Reader) error {
	upstream, err := net.DialTimeout("tcp", upstreamAddr, time.Duration(timeoutSec)*time.Second)
	if err != nil {
		return fmt.Errorf("连接上游 %s: %w", upstreamAddr, err)
	}
	defer upstream.Close()

	var connectReq strings.Builder
	connectReq.WriteString("CONNECT ")
	connectReq.WriteString(target)
	connectReq.WriteString(" HTTP/1.1\r\nHost: ")
	connectReq.WriteString(target)
	connectReq.WriteString("\r\n\r\n")
	if _, err := upstream.Write([]byte(connectReq.String())); err != nil {
		return fmt.Errorf("发送 CONNECT: %w", err)
	}

	br := newBufReader(upstream)
	resp, err := readHTTPResponse(br)
	if err != nil {
		return fmt.Errorf("读取上游响应: %w", err)
	}
	if resp.StatusCode != 200 {
		return fmt.Errorf("上游 CONNECT %s 返回 %d", target, resp.StatusCode)
	}

	// Build client reader: prepend any bytes already buffered from the client
	// (by handleHTTPProxy's bufio.Reader), then the raw connection.
	var clientR io.Reader
	if httpBR != nil {
		clientR = io.MultiReader(httpBR, client)
	} else {
		clientR = client
	}

	// Prepend any bytes buffered from the upstream CONNECT response.
	if br.Buffered() > 0 {
		extra := make([]byte, br.Buffered())
		br.Read(extra)
		clientR = newMultiReader(extra, clientR)
	}

	e.tunnel(clientR, client, upstream, ci)
	return nil
}

func (e *ProxyEngine) tunnel(clientR io.Reader, clientW net.Conn, upstream net.Conn, ci *ConnInfo) {
	done := make(chan struct{}, 2)

	go func() {
		n, _ := copyPooled(upstream, clientR)
		atomic.AddInt64(&ci.BytesUp, n)
		upstream.Close()
		done <- struct{}{}
	}()
	go func() {
		n, _ := copyPooled(clientW, upstream)
		atomic.AddInt64(&ci.BytesDown, n)
		clientW.Close()
		done <- struct{}{}
	}()

	<-done
	<-done
}

func protocolLabel(p string) string {
	switch p {
	case ProtoSocks5:
		return "SOCKS5"
	case ProtoHTTP:
		return "HTTP代理"
	case ProtoMixed:
		return "混合(SOCKS5+HTTP)"
	default:
		return p
	}
}

func SaveConfig(path string, cfg Config) error {
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0600)
}

func LoadConfig(path string) (Config, error) {
	var cfg Config
	data, err := os.ReadFile(path)
	if err != nil {
		return cfg, err
	}
	err = json.Unmarshal(data, &cfg)
	if err != nil {
		return cfg, err
	}
	return cfg, cfg.Validate()
}

func (c Config) Validate() error {
	if _, _, err := net.SplitHostPort(c.UpstreamProxy); err != nil {
		return fmt.Errorf("invalid upstream_proxy %q: %w", c.UpstreamProxy, err)
	}
	seen := map[string]bool{}
	for i, l := range c.Listeners {
		if _, _, err := net.SplitHostPort(l.Address); err != nil {
			return fmt.Errorf("listeners[%d]: invalid address %q: %w", i, l.Address, err)
		}
		if seen[l.Address] {
			return fmt.Errorf("listeners[%d]: duplicate address %s", i, l.Address)
		}
		seen[l.Address] = true
		switch l.Protocol {
		case ProtoSocks5, ProtoHTTP, ProtoMixed:
		default:
			return fmt.Errorf("listeners[%d]: unknown protocol %q", i, l.Protocol)
		}
	}
	return nil
}

// handleMixed 自动检测协议：读取第一个字节判断是 SOCKS5 还是 HTTP
// SOCKS5 第一个字节是 0x05，HTTP 第一个字节是方法名的 ASCII (G/P/C/H 等)
func handleMixed(client net.Conn, upstreamProxy string, upstreamTimeout, detectTimeout int) (protocol string, target string, err error) {
	buf := make([]byte, 1)
	client.SetReadDeadline(time.Now().Add(time.Duration(detectTimeout) * time.Second))
	_, err = io.ReadFull(client, buf)
	client.SetReadDeadline(time.Time{})
	if err != nil {
		return "", "", fmt.Errorf("读取首字节失败: %w", err)
	}

	firstByte := buf[0]

	// 包装连接，把首字节放回去
	protoConn := &mixedConn{
		first: firstByte,
		Conn:  client,
	}

	if firstByte == 0x05 {
		target, err = handleSocks5(protoConn)
		return ProtoSocks5, target, err
	}

	target, _, err = handleHTTPProxy(protoConn, upstreamProxy, upstreamTimeout)
	return ProtoHTTP, target, err
}

type mixedConn struct {
	first    byte
	consumed bool
	net.Conn
}

func (c *mixedConn) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	if !c.consumed {
		p[0] = c.first
		c.consumed = true
		if len(p) == 1 {
			return 1, nil
		}
		n, err := c.Conn.Read(p[1:])
		if n > 0 {
			return n + 1, nil
		}
		return 1, err
	}
	return c.Conn.Read(p)
}
