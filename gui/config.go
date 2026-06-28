package gui

import (
	"GoMuxProxy/proxy"
	"fmt"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/widget"
)

type ConfigTab struct {
	engine   *proxy.ProxyEngine
	onChange func()

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

func (ct *ConfigTab) Widget() fyne.CanvasObject {
	upstreamSection := container.NewVBox(
		widget.NewLabel("上游代理地址"),
		ct.upstreamEntry,
		widget.NewButton("应用上游地址", func() {
			cfg := ct.engine.Config()
			cfg.UpstreamProxy = ct.upstreamEntry.Text
			ct.engine.UpdateConfig(cfg)
			if ct.onChange != nil {
				ct.onChange()
			}
		}),
	)

	addBtn := widget.NewButton("添加监听", ct.showAddDialog)
	delBtn := widget.NewButton("删除选中", func() {
		if ct.selected < 0 || ct.selected >= len(ct.listeners) {
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
			return
		}
		addr := ct.listeners[ct.selected].Address
		ct.engine.ToggleListener(addr, true)
		ct.listeners[ct.selected].Enabled = true
		ct.listenerList.Refresh()
	})
	disableBtn := widget.NewButton("禁用选中", func() {
		if ct.selected < 0 || ct.selected >= len(ct.listeners) {
			return
		}
		addr := ct.listeners[ct.selected].Address
		ct.engine.ToggleListener(addr, false)
		ct.listeners[ct.selected].Enabled = false
		ct.listenerList.Refresh()
	})

	saveBtn := widget.NewButton("保存配置", func() {
		cfg := ct.engine.Config()
		if err := proxy.SaveConfig("config.json", cfg); err != nil {
			dialog.ShowError(err, fyne.CurrentApp().Driver().AllWindows()[0])
			return
		}
		dialog.ShowInformation("保存成功", "配置已保存到 config.json", fyne.CurrentApp().Driver().AllWindows()[0])
	})

	loadBtn := widget.NewButton("加载配置", func() {
		cfg, err := proxy.LoadConfig("config.json")
		if err != nil {
			dialog.ShowError(err, fyne.CurrentApp().Driver().AllWindows()[0])
			return
		}
		ct.engine.UpdateConfig(cfg)
		ct.listeners = append(ct.listeners[:0], cfg.Listeners...)
		ct.upstreamEntry.SetText(cfg.UpstreamProxy)
		ct.listenerList.Refresh()
		if ct.onChange != nil {
			ct.onChange()
		}
	})

	toolbar := container.NewHBox(addBtn, delBtn, widget.NewSeparator(), enableBtn, disableBtn, widget.NewSeparator(), saveBtn, loadBtn)
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

	w := fyne.CurrentApp().Driver().AllWindows()[0]
	dialog.ShowCustomConfirm("添加监听端口", "添加", "取消", form, func(ok bool) {
		if !ok {
			return
		}
		proto := protocolFromLabel(protoSelect.Selected)
		entry := proxy.ListenEntry{
			Network:  "tcp",
			Address:  addrEntry.Text,
			Protocol: proto,
			Enabled:  true,
		}
		if err := ct.engine.AddListener(entry); err != nil {
			dialog.ShowError(err, w)
			return
		}
		ct.listeners = append(ct.listeners, entry)
		ct.listenerList.Refresh()
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
