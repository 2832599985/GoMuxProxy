package gui

import (
	"GoMuxProxy/proxy"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
)

type App struct {
	fyneApp    fyne.App
	mainWindow fyne.Window
	engine     *proxy.ProxyEngine

	dashboard *Dashboard
	logView   *LogView
	configTab *ConfigTab

	stopCh chan struct{}
}

func NewApp(fyneApp fyne.App, engine *proxy.ProxyEngine) *App {
	a := &App{
		fyneApp: fyneApp,
		engine:  engine,
		stopCh:  make(chan struct{}),
	}

	// 设置应用图标（影响窗口标题栏和任务栏图标）
	fyneApp.SetIcon(iconResource())

	a.dashboard = NewDashboard(engine, a.stopCh)
	a.logView = NewLogView()
	a.configTab = NewConfigTab(engine, a.onConfigChanged)

	engine.SetCallbacks(a.onLog, a.onConnect, a.onDisconnect)

	go func() {
		if err := engine.Start(); err != nil {
			fyne.Do(func() {
				dialog.ShowError(err, a.mainWindow)
			})
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
	a.configTab.SetWindow(a.mainWindow)

	setupTray(a.fyneApp, a.mainWindow, func() {
		a.Close()
		a.fyneApp.Quit()
	})

	a.mainWindow.ShowAndRun()
}

func (a *App) onLog(msg string) {
	fyne.Do(func() {
		a.logView.Append(msg)
	})
}

func (a *App) onConnect(ci proxy.ConnInfo) {
	a.dashboard.OnConnect(ci)
}

func (a *App) onDisconnect(ci proxy.ConnInfo) {
	a.dashboard.OnDisconnect(ci)
}

func (a *App) onConfigChanged() {
	if a.engine.IsRunning() {
		if err := a.engine.Restart(); err != nil {
			fyne.Do(func() {
				dialog.ShowError(err, a.mainWindow)
			})
		}
	}
}

// Close signals the dashboard autoRefresh goroutine to stop and stops the engine.
func (a *App) Close() {
	close(a.stopCh)
	a.engine.Stop()
}
