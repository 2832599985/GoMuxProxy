package proxy

import (
	"fmt"
	"log"
	"net"
	"sync"
	"sync/atomic"
)

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

	serveWg       sync.WaitGroup
	restartMu     sync.Mutex
	connSemaphore chan struct{}
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
