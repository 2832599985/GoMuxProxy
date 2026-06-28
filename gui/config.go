package gui

import (
	"GoMuxProxy/proxy"
	"fmt"
	"net"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/widget"
)

type ConfigTab struct {
	engine   *proxy.ProxyEngine
	onChange func()
	window   fyne.Window

	upstreamEntry *widget.Entry
	listenerList  *widget.List
	listeners     []proxy.ListenEntry

	selected int
}

func NewConfigTab(engine *proxy.ProxyEngine, onChange func()) *ConfigTab {
	ct := &ConfigTab{
		engine:   engine,
		onChange: onChange,
		selected: -1,
	}

	cfg := engine.Config()
	ct.listeners = append(ct.listeners, cfg.Listeners...)

	ct.upstreamEntry = widget.NewEntry()
	ct.upstreamEntry.SetText(cfg.UpstreamProxy)

	ct.listenerList = widget.NewList(
		func() int { return len(ct.listeners) },
		func() fyne.CanvasObject {
			return widget.NewLabel("127.0.0.1:1081 混合(SOCKS5/HTTP)")
		},
		func(id widget.ListItemID, obj fyne.CanvasObject) {
			l := ct.listeners[id]
			status := "●"
			if !l.Enabled {
				status = "○"
			}
			obj.(*widget.Label).SetText(fmt.Sprintf("%s %s  (%s)", status, l.Address, protocolLabelCN(l.Protocol)))
		},
	)
	ct.listenerList.OnSelected = func(id widget.ListItemID) {
		ct.selected = id
	}

	return ct
}

func (ct *ConfigTab) SetWindow(w fyne.Window) {
	ct.window = w
}

func (ct *ConfigTab) getWindow() fyne.Window {
	if ct.window != nil {
		return ct.window
	}
	return fyne.CurrentApp().Driver().AllWindows()[0]
}

// refreshFromEngine syncs the local listeners slice from the engine config.
func (ct *ConfigTab) refreshFromEngine() {
	cfg := ct.engine.Config()
	ct.listeners = append(ct.listeners[:0], cfg.Listeners...)
	ct.selected = -1
	ct.listenerList.Refresh()
}

func (ct *ConfigTab) Widget() fyne.CanvasObject {
	upstreamSection := container.NewVBox(
		widget.NewLabel("上游代理地址"),
		ct.upstreamEntry,
	)

	addBtn := widget.NewButton("添加监听", ct.showAddDialog)
	editBtn := widget.NewButton("编辑选中", ct.showEditDialog)
	delBtn := widget.NewButton("删除选中", func() {
		if ct.selected < 0 || ct.selected >= len(ct.listeners) {
			dialog.ShowInformation("提示", "请先选择一个监听项", ct.getWindow())
			return
		}
		addr := ct.listeners[ct.selected].Address
		ct.engine.RemoveListener(addr)
		ct.listeners = append(ct.listeners[:ct.selected], ct.listeners[ct.selected+1:]...)
		ct.selected = -1
		ct.listenerList.Refresh()
	})
	enableBtn := widget.NewButton("启用选中", func() {
		if ct.selected < 0 || ct.selected >= len(ct.listeners) {
			dialog.ShowInformation("提示", "请先选择一个监听项", ct.getWindow())
			return
		}
		addr := ct.listeners[ct.selected].Address
		ct.engine.ToggleListener(addr, true)
		ct.listeners[ct.selected].Enabled = true
		ct.listenerList.Refresh()
	})
	disableBtn := widget.NewButton("禁用选中", func() {
		if ct.selected < 0 || ct.selected >= len(ct.listeners) {
			dialog.ShowInformation("提示", "请先选择一个监听项", ct.getWindow())
			return
		}
		addr := ct.listeners[ct.selected].Address
		ct.engine.ToggleListener(addr, false)
		ct.listeners[ct.selected].Enabled = false
		ct.listenerList.Refresh()
	})

	saveBtn := widget.NewButton("保存配置", func() {
		// Apply upstream address from entry before saving
		cfg := ct.engine.Config()
		cfg.UpstreamProxy = ct.upstreamEntry.Text
		ct.engine.UpdateConfig(cfg)
		if ct.onChange != nil {
			ct.onChange()
		}

		if err := cfg.Validate(); err != nil {
			dialog.ShowError(err, ct.getWindow())
			return
		}
		if err := proxy.SaveConfig("config.json", cfg); err != nil {
			dialog.ShowError(err, ct.getWindow())
			return
		}
		dialog.ShowInformation("保存成功", "配置已保存到 config.json", ct.getWindow())
	})

	loadBtn := widget.NewButton("加载配置", func() {
		cfg, err := proxy.LoadConfig("config.json")
		if err != nil {
			dialog.ShowError(err, ct.getWindow())
			return
		}
		ct.engine.UpdateConfig(cfg)
		ct.refreshFromEngine()
		ct.upstreamEntry.SetText(cfg.UpstreamProxy)
		if ct.onChange != nil {
			ct.onChange()
		}
	})

	toolbar := container.NewHBox(addBtn, editBtn, delBtn, widget.NewSeparator(), enableBtn, disableBtn, widget.NewSeparator(), saveBtn, loadBtn)
	listSection := container.NewBorder(
		widget.NewLabel("监听端口列表 (● 启用 / ○ 禁用)"),
		toolbar, nil, nil,
		ct.listenerList,
	)

	return container.NewVBox(
		upstreamSection,
		widget.NewSeparator(),
		listSection,
	)
}

// isLoopbackHost checks whether a host string resolves to a loopback address.
func isLoopbackHost(host string) bool {
	if host == "" || host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return false
	}
	return ip.IsLoopback()
}

// extractHost strips the port from an "host:port" address.
func extractHost(addr string) string {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		// No port or malformed; try treating the whole string as host.
		return addr
	}
	return host
}

// showLoopbackWarningIfNeeded returns true and shows a confirm dialog if the
// address is non-loopback. The onConfirm callback is called only when the user
// confirms. Returns false when the address is loopback (caller should proceed
// directly).
func (ct *ConfigTab) showLoopbackWarningIfNeeded(addr string, onConfirm func()) bool {
	host := extractHost(addr)
	if isLoopbackHost(host) {
		return false
	}
	w := ct.getWindow()
	dialog.ShowConfirm("警告", "绑定到非本地地址将暴露代理到网络，确定继续？", func(ok bool) {
		if ok {
			onConfirm()
		}
	}, w)
	return true
}

func (ct *ConfigTab) showAddDialog() {
	addrEntry := widget.NewEntry()
	addrEntry.SetText("127.0.0.1:1084")

	protoSelect := widget.NewSelect([]string{
		"混合(SOCKS5/HTTP)",
		"SOCKS5",
		"HTTP 代理",
	}, nil)
	protoSelect.SetSelected("混合(SOCKS5/HTTP)")

	form := widget.NewForm(
		widget.NewFormItem("监听地址", addrEntry),
		widget.NewFormItem("协议类型", protoSelect),
	)

	w := ct.getWindow()
	dialog.ShowCustomConfirm("添加监听端口", "添加", "取消", form, func(ok bool) {
		if !ok {
			return
		}
		addr := strings.TrimSpace(addrEntry.Text)
		if addr == "" {
			dialog.ShowError(fmt.Errorf("地址不能为空"), w)
			return
		}

		proto := protocolFromLabel(protoSelect.Selected)
		entry := proxy.ListenEntry{
			Network:  "tcp",
			Address:  addr,
			Protocol: proto,
			Enabled:  true,
		}

		addEntry := func() {
			if err := ct.engine.AddListener(entry); err != nil {
				dialog.ShowError(err, w)
				return
			}
			ct.listeners = append(ct.listeners, entry)
			ct.listenerList.Refresh()
		}

		if ct.showLoopbackWarningIfNeeded(addr, addEntry) {
			return // warning dialog will call addEntry if confirmed
		}
		addEntry()
	}, w)
}

func (ct *ConfigTab) showEditDialog() {
	if ct.selected < 0 || ct.selected >= len(ct.listeners) {
		dialog.ShowInformation("提示", "请先选择一个监听项", ct.getWindow())
		return
	}

	sel := ct.listeners[ct.selected]

	addrEntry := widget.NewEntry()
	addrEntry.SetText(sel.Address)

	protoSelect := widget.NewSelect([]string{
		"混合(SOCKS5/HTTP)",
		"SOCKS5",
		"HTTP 代理",
	}, nil)
	protoSelect.SetSelected(protocolLabelCN(sel.Protocol))

	form := widget.NewForm(
		widget.NewFormItem("监听地址", addrEntry),
		widget.NewFormItem("协议类型", protoSelect),
	)

	w := ct.getWindow()
	dialog.ShowCustomConfirm("编辑监听端口", "保存", "取消", form, func(ok bool) {
		if !ok {
			return
		}
		newAddr := strings.TrimSpace(addrEntry.Text)
		if newAddr == "" {
			dialog.ShowError(fmt.Errorf("地址不能为空"), w)
			return
		}

		proto := protocolFromLabel(protoSelect.Selected)
		newEntry := proxy.ListenEntry{
			Network:  sel.Network,
			Address:  newAddr,
			Protocol: proto,
			Enabled:  sel.Enabled,
		}

		applyEdit := func() {
			// Remove old listener, ignore error if already stopped.
			ct.engine.RemoveListener(sel.Address)

			// Re-add with new settings (enabled state preserved).
			if err := ct.engine.AddListener(newEntry); err != nil {
				dialog.ShowError(err, w)
				return
			}
			ct.listeners[ct.selected] = newEntry
			ct.listenerList.Refresh()
		}

		if ct.showLoopbackWarningIfNeeded(newAddr, applyEdit) {
			return // warning dialog will call applyEdit if confirmed
		}
		applyEdit()
	}, w)
}

func protocolLabelCN(p string) string {
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

func protocolFromLabel(label string) string {
	switch label {
	case "SOCKS5":
		return "socks5"
	case "HTTP 代理":
		return "http"
	case "混合(SOCKS5/HTTP)":
		return "mixed"
	default:
		return label
	}
}
