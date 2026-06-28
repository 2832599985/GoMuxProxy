package gui

import (
	"GoMuxProxy/proxy"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/widget"
)

const defaultRefreshInterval = 1 * time.Second

type Dashboard struct {
	engine          *proxy.ProxyEngine
	refreshInterval time.Duration

	totalLabel     *widget.Label
	activeLabel    *widget.Label
	upSpeedLabel   *widget.Label
	downSpeedLabel *widget.Label
	upTotalLabel   *widget.Label
	downTotalLabel *widget.Label
	connTable      *widget.Table
	portCards      *fyne.Container

	// Cached port card widgets — rebuilt only when port count changes.
	portCardWidgets []portCardCache

	mu            sync.Mutex
	lastBytesUp   int64
	lastBytesDown int64
	lastCheck     time.Time

	conns  []proxy.ConnInfo
	stopCh chan struct{}
}

type portCardCache struct {
	card          *widget.Card
	titleLabel    *widget.Label
	subtitleLabel *widget.Label
	contentLabel  *widget.Label
}

var refreshPending atomic.Bool

func NewDashboard(engine *proxy.ProxyEngine, stopCh chan struct{}) *Dashboard {
	return NewDashboardWithInterval(engine, stopCh, defaultRefreshInterval)
}

func NewDashboardWithInterval(engine *proxy.ProxyEngine, stopCh chan struct{}, interval time.Duration) *Dashboard {
	d := &Dashboard{
		engine:          engine,
		refreshInterval: interval,
		lastCheck:       time.Now(),
		stopCh:          stopCh,
	}

	d.totalLabel = widget.NewLabel("累计连接: 0")
	d.activeLabel = widget.NewLabel("活跃连接: 0")
	d.upSpeedLabel = widget.NewLabel("↑ 0 B/s")
	d.downSpeedLabel = widget.NewLabel("↓ 0 B/s")
	d.upTotalLabel = widget.NewLabel("↑ 累计: 0 B")
	d.downTotalLabel = widget.NewLabel("↓ 累计: 0 B")

	d.portCards = container.NewHBox()

	headers := []string{"#", "来源", "目标", "协议", "持续时间"}
	d.connTable = widget.NewTable(
		func() (int, int) {
			d.mu.Lock()
			n := len(d.conns)
			d.mu.Unlock()
			return n + 1, 5
		},
		func() fyne.CanvasObject {
			return widget.NewLabel("------------")
		},
		func(id widget.TableCellID, obj fyne.CanvasObject) {
			label := obj.(*widget.Label)
			if id.Row == 0 {
				label.SetText(headers[id.Col])
				label.TextStyle = fyne.TextStyle{Bold: true}
				return
			}
			d.mu.Lock()
			if id.Row-1 >= len(d.conns) {
				d.mu.Unlock()
				return
			}
			ci := d.conns[id.Row-1]
			d.mu.Unlock()
			label.TextStyle = fyne.TextStyle{}
			switch id.Col {
			case 0:
				label.SetText(fmt.Sprintf("%d", ci.ID))
			case 1:
				label.SetText(ci.Source)
			case 2:
				label.SetText(ci.Target)
			case 3:
				label.SetText(ci.Protocol)
			case 4:
				label.SetText(formatDuration(time.Since(ci.StartTime)))
			}
		},
	)
	d.connTable.SetColumnWidth(0, 50)
	d.connTable.SetColumnWidth(1, 180)
	d.connTable.SetColumnWidth(2, 200)
	d.connTable.SetColumnWidth(3, 100)
	d.connTable.SetColumnWidth(4, 100)

	go d.autoRefresh()

	return d
}

func (d *Dashboard) Widget() fyne.CanvasObject {
	statsRow := container.NewGridWithColumns(4,
		container.NewVBox(d.totalLabel, d.activeLabel),
		container.NewVBox(d.upSpeedLabel, d.downSpeedLabel),
		container.NewVBox(d.upTotalLabel),
		container.NewVBox(d.downTotalLabel),
	)

	portSection := container.NewVBox(
		widget.NewLabel("监听端口"),
		d.portCards,
		widget.NewSeparator(),
	)

	return container.NewBorder(
		container.NewVBox(statsRow, portSection),
		nil, nil, nil,
		d.connTable,
	)
}

func (d *Dashboard) Refresh() {
	d.mu.Lock()
	d.conns = d.engine.GetActiveConns()
	d.mu.Unlock()
	d.connTable.Refresh()
}

// scheduleRefresh debounces connection events: at most one refresh per 500ms.
func (d *Dashboard) scheduleRefresh() {
	if refreshPending.CompareAndSwap(false, true) {
		time.AfterFunc(500*time.Millisecond, func() {
			refreshPending.Store(false)
			fyne.Do(func() { d.Refresh() })
		})
	}
}

// OnConnect handles connect events with debouncing.
func (d *Dashboard) OnConnect(ci proxy.ConnInfo) {
	d.scheduleRefresh()
}

// OnDisconnect handles disconnect events with debouncing.
func (d *Dashboard) OnDisconnect(ci proxy.ConnInfo) {
	d.scheduleRefresh()
}

func (d *Dashboard) autoRefresh() {
	ticker := time.NewTicker(d.refreshInterval)
	defer ticker.Stop()

	for {
		select {
		case <-d.stopCh:
			return
		case <-ticker.C:
			d.updateStats()
		}
	}
}

func (d *Dashboard) updateStats() {
	stats := d.engine.GetStats()
	now := time.Now()

	d.mu.Lock()
	elapsed := now.Sub(d.lastCheck).Seconds()
	var upSpeed, downSpeed float64
	if elapsed > 0 {
		upSpeed = float64(stats.TotalBytesUp-d.lastBytesUp) / elapsed
		downSpeed = float64(stats.TotalBytesDown-d.lastBytesDown) / elapsed
	}
	d.lastBytesUp = stats.TotalBytesUp
	d.lastBytesDown = stats.TotalBytesDown
	d.lastCheck = now
	d.mu.Unlock()

	portStats := d.engine.GetPortStats()

	fyne.Do(func() {
		d.totalLabel.SetText(fmt.Sprintf("累计连接: %d", stats.TotalConns))
		d.activeLabel.SetText(fmt.Sprintf("活跃连接: %d", stats.ActiveConns))
		d.upSpeedLabel.SetText(fmt.Sprintf("↑ %s/s", formatBytes(upSpeed)))
		d.downSpeedLabel.SetText(fmt.Sprintf("↓ %s/s", formatBytes(downSpeed)))
		d.upTotalLabel.SetText(fmt.Sprintf("↑ 累计: %s", formatBytesI(stats.TotalBytesUp)))
		d.downTotalLabel.SetText(fmt.Sprintf("↓ 累计: %s", formatBytesI(stats.TotalBytesDown)))

		d.updatePortCards(portStats)

		d.mu.Lock()
		d.conns = d.engine.GetActiveConns()
		d.mu.Unlock()
		d.connTable.Refresh()
	})
}

func (d *Dashboard) updatePortCards(portStats []proxy.PortStats) {
	// Create cards on first call (or if port count changes); just update text otherwise.
	if len(d.portCardWidgets) != len(portStats) {
		d.portCardWidgets = make([]portCardCache, len(portStats))
		d.portCards.Objects = nil
		for i := range portStats {
			cc := portCardCache{
				titleLabel:    widget.NewLabel(""),
				subtitleLabel: widget.NewLabel(""),
				contentLabel:  widget.NewLabel(""),
			}
			cc.card = widget.NewCard("", "",
				container.NewVBox(cc.titleLabel, cc.subtitleLabel, cc.contentLabel))
			cc.card.Resize(fyne.NewSize(150, 80))
			d.portCardWidgets[i] = cc
			d.portCards.Objects = append(d.portCards.Objects, cc.card)
		}
	}
	for i, ps := range portStats {
		cc := &d.portCardWidgets[i]

		var statusPrefix string
		if !ps.Enabled {
			statusPrefix = "[OFF] ○"
		} else if !ps.Running {
			statusPrefix = "[ERR] ◐"
		} else {
			statusPrefix = "[ON] ●"
		}
		cc.titleLabel.SetText(fmt.Sprintf("%s %s", statusPrefix, ps.Address))
		cc.subtitleLabel.SetText(protocolLabel(ps.Protocol))
		cc.contentLabel.SetText(fmt.Sprintf("连接数: %d", ps.ActiveConn))
	}
	d.portCards.Refresh()
}

func protocolLabel(p string) string {
	switch p {
	case "socks5":
		return "SOCKS5"
	case "http":
		return "HTTP 代理"
	case "mixed":
		return "混合(SOCKS5/HTTP)"
	default:
		return p
	}
}

func formatBytes(b float64) string {
	if b < 1024 {
		return fmt.Sprintf("%.0f B", b)
	} else if b < 1024*1024 {
		return fmt.Sprintf("%.1f KB", b/1024)
	} else if b < 1024*1024*1024 {
		return fmt.Sprintf("%.1f MB", b/1024/1024)
	}
	return fmt.Sprintf("%.2f GB", b/1024/1024/1024)
}

func formatBytesI(b int64) string {
	return formatBytes(float64(b))
}

func formatDuration(d time.Duration) string {
	if d < time.Minute {
		return fmt.Sprintf("%.0f 秒", d.Seconds())
	}
	if d < time.Hour {
		return fmt.Sprintf("%.0f 分 %.0f 秒", d.Minutes(), d.Seconds()-60*d.Minutes())
	}
	return fmt.Sprintf("%.0f 时 %.0f 分", d.Hours(), d.Minutes()-60*d.Hours())
}
