package gui

import (
	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/widget"
)

type LogView struct {
	entry    *widget.Entry
	filter   *widget.Entry
	allLines []string
	scroll   *container.Scroll
}

func NewLogView() *LogView {
	lv := &LogView{}

	lv.entry = widget.NewMultiLineEntry()
	lv.entry.Disable()
	lv.entry.Wrapping = fyne.TextWrapWord

	lv.filter = widget.NewEntry()
	lv.filter.SetPlaceHolder("筛选日志...")
	lv.filter.OnChanged = func(text string) {
		lv.applyFilter()
	}

	lv.scroll = container.NewVScroll(lv.entry)

	return lv
}

func (lv *LogView) Widget() fyne.CanvasObject {
	clearBtn := widget.NewButton("清空", func() {
		lv.allLines = nil
		lv.entry.SetText("")
	})

	return container.NewBorder(
		container.NewVBox(
			widget.NewLabel("运行日志"),
			container.NewBorder(nil, nil, nil, clearBtn, lv.filter),
		),
		nil, nil, nil,
		lv.scroll,
	)
}

func (lv *LogView) Append(msg string) {
	lv.allLines = append(lv.allLines, msg)

	if len(lv.allLines) > 5000 {
		lv.allLines = lv.allLines[len(lv.allLines)-5000:]
	}

	lv.applyFilter()
}

func (lv *LogView) applyFilter() {
	filter := lv.filter.Text
	var text string
	for _, line := range lv.allLines {
		if filter == "" || containsIgnoreCase(line, filter) {
			text += line + "\n"
		}
	}
	lv.entry.SetText(text)
}

func containsIgnoreCase(s, sub string) bool {
	if sub == "" {
		return true
	}
	sLower := toLower(s)
	subLower := toLower(sub)
	return contains(sLower, subLower)
}

func toLower(s string) string {
	b := make([]byte, len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c >= 'A' && c <= 'Z' {
			c += 'a' - 'A'
		}
		b[i] = c
	}
	return string(b)
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
