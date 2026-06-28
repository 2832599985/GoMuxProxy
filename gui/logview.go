package gui

import (
	"strings"
	"sync"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/widget"
)

type LogView struct {
	entry    *widget.Entry
	filter   *widget.Entry
	allLines []string
	scroll   *container.Scroll
	mu       sync.Mutex
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
	lv.mu.Lock()
	defer lv.mu.Unlock()

	lv.allLines = append(lv.allLines, msg)

	if len(lv.allLines) > 5000 {
		lv.allLines = lv.allLines[len(lv.allLines)-5000:]
	}

	lv.applyFilter()
}

func (lv *LogView) applyFilter() {
	filter := strings.ToLower(lv.filter.Text)
	var b strings.Builder
	for _, line := range lv.allLines {
		if filter == "" || strings.Contains(strings.ToLower(line), filter) {
			b.WriteString(line)
			b.WriteByte('\n')
		}
	}
	lv.entry.SetText(b.String())
}
