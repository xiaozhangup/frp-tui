package ui

import (
	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

func (s *screen) buildFooter() *tview.Flex {
	s.footer = tview.NewTextView().SetDynamicColors(true).SetWrap(false).
		SetText("正在连接守护进程…")
	actions := tview.NewFlex()
	s.globalFocus = nil
	for i, action := range s.globalActions() {
		if i > 0 {
			actions.AddItem(nil, 1, 0, false)
		}
		button := tview.NewButton(keyLabel(tcell.KeyNames[action.key], action.label)).
			SetSelectedFunc(action.run).
			SetStyle(actionStyle).
			SetActivatedStyle(focusedActionStyle)
		actions.AddItem(button, 0, 1, i == 0)
		s.globalFocus = append(s.globalFocus, button)
	}
	pages := tview.NewPages().
		AddPage("actions", actions, true, false).
		AddPage("message", s.footer, true, true)
	return tview.NewFlex().AddItem(pages, 0, 1, true)
}

func (s *screen) updateFooter(footer *tview.Flex) {
	pages := footer.GetItem(0).(*tview.Pages)
	page := "actions"
	if s.footer.GetText(false) != "" {
		page = "message"
	}
	if current, _ := pages.GetFrontPage(); current != page {
		pages.SwitchToPage(page)
	}
}
