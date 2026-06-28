package gui

import (
	"GoMuxProxy/proxy"
	"fmt"
	"sync"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
)

type App struct {
	fyneApp    fyne.App
	mainWindow fyne.Window
	engine     *proxy.ProxyEngine

	dashboard *Dashboard
	logView   *LogView
	configTab *ConfigTab

	logMu   sync.Mutex
	logText string
}

func NewApp(fyneApp fyne.App, engine *proxy.ProxyEngine) *App {
	a := &App{
		fyneApp: fyneApp,
		engine:  engine,
	}

	// 设置应用图标（影响窗口标题栏和任务栏图标）
	fyneApp.SetIcon(iconResource())

	a.dashboard = NewDashboard(engine)
	a.logView = NewLogView()
	a.configTab = NewConfigTab(engine, a.onConfigChanged)

	engine.SetCallbacks(a.onLog, a.onConnect, a.onDisconnect)

	go func() {
		if err := engine.Start(); err != nil {
			a.onLog(fmt.Sprintf("启动错误: %v", err))
		}
	}()

	return a
}

func (a *App) Run() {
	a.mainWindow = a.fyneApp.NewWindow("GoMuxProxy 代理转发器")
	a.mainWindow.Resize(fyne.NewSize(800, 600))
	a.mainWindow.SetIcon(iconResource())
	a.mainWindow.SetCloseIntercept(func() {
		a.mainWindow.Hide()
	})

	tabs := container.NewAppTabs(
		container.NewTabItem("状态监控", a.dashboard.Widget()),
		container.NewTabItem("运行日志", a.logView.Widget()),
		container.NewTabItem("配置管理", a.configTab.Widget()),
	)
	tabs.SetTabLocation(container.TabLocationTop)

	a.mainWindow.SetContent(tabs)

	setupTray(a.fyneApp, a.mainWindow, func() {
		a.engine.Stop()
		a.fyneApp.Quit()
	})

	a.mainWindow.ShowAndRun()
}

func (a *App) onLog(msg string) {
	a.logMu.Lock()
	a.logText += msg + "\n"
	a.logMu.Unlock()
	fyne.Do(func() {
		a.logView.Append(msg)
	})
}

func (a *App) onConnect(ci proxy.ConnInfo) {
	fyne.Do(func() {
		a.dashboard.Refresh()
	})
}

func (a *App) onDisconnect(ci proxy.ConnInfo) {
	fyne.Do(func() {
		a.dashboard.Refresh()
	})
}

func (a *App) onConfigChanged() {
	if a.engine.IsRunning() {
		if err := a.engine.Restart(); err != nil {
			a.onLog(fmt.Sprintf("重启错误: %v", err))
		}
	}
}
