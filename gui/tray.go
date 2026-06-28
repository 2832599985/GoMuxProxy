package gui

import (
	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/driver/desktop"
)

func setupTray(app fyne.App, window fyne.Window, onQuit func()) {
	if desk, ok := app.(desktop.App); ok {
		menu := fyne.NewMenu("GoMuxProxy",
			fyne.NewMenuItem("显示主窗口", func() {
				window.Show()
				window.RequestFocus()
			}),
			fyne.NewMenuItemSeparator(),
			fyne.NewMenuItem("退出程序", onQuit),
		)
		desk.SetSystemTrayMenu(menu)
		desk.SetSystemTrayIcon(iconResource())
	}
}
