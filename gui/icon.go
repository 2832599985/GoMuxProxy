package gui

import (
	_ "embed"

	"fyne.io/fyne/v2"
)

//go:embed icon_256.png
var iconData256 []byte

func iconResource() *fyne.StaticResource {
	return &fyne.StaticResource{
		StaticName:    "icon.png",
		StaticContent: iconData256,
	}
}
