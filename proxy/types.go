package proxy

import "time"

const (
	ProtoSocks5 = "socks5"
	ProtoHTTP   = "http"
	ProtoMixed  = "mixed"
)

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
