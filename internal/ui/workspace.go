package ui

import (
	"fmt"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

func (s *screen) buildWorkspace(proxyBar, logControls *tview.Flex) *tview.Flex {
	s.proxyPane = tview.NewFlex().SetDirection(tview.FlexRow).
		AddItem(s.proxies, 0, 1, true).
		AddItem(proxyBar, 1, 0, false)
	s.logPane = tview.NewFlex().SetDirection(tview.FlexRow).
		AddItem(s.logs, 0, 1, true).
		AddItem(logControls, 1, 0, false)
	s.workspacePages = tview.NewPages().
		AddPage("proxies", s.proxyPane, true, true).
		AddPage("logs", s.logPane, true, false)
	s.activeTab = "proxies"
	s.proxyTab = tview.NewButton("").SetSelectedFunc(func() { s.selectTab("proxies") })
	s.logTab = tview.NewButton("").SetSelectedFunc(func() { s.selectTab("logs") })
	s.proxyTab.SetActivatedStyle(focusedActionStyle)
	s.logTab.SetActivatedStyle(focusedActionStyle)
	tabs := tview.NewFlex().
		AddItem(s.proxyTab, 22, 0, false).
		AddItem(nil, 1, 0, false).
		AddItem(s.logTab, 18, 0, false).
		AddItem(nil, 0, 1, false)
	s.workspacePane = newPane(" 代理与日志 ").
		AddItem(tabs, 1, 0, false).
		AddItem(s.workspacePages, 0, 1, true)
	s.updateWorkspaceTabs(0)
	return s.workspacePane
}

func (s *screen) selectTab(name string) {
	s.activeTab = name
	s.workspacePages.SwitchToPage(name)
	count := 0
	if c, ok := s.current(); ok {
		count = len(c.Proxies)
	}
	s.updateWorkspaceTabs(count)
	if name == "logs" {
		s.app.SetFocus(s.logs)
	} else {
		s.app.SetFocus(s.proxies)
	}
}

func (s *screen) updateWorkspaceTabs(count int) {
	s.proxyTab.SetLabel(fmt.Sprintf("Alt+1 代理 (%d)", count)).SetStyle(actionStyle)
	s.logTab.SetLabel("Alt+2 日志").SetStyle(actionStyle)
	activeStyle := tcell.StyleDefault.Background(accentColor).
		Foreground(tcell.NewHexColor(0x102337)).Bold(true)
	if s.activeTab == "logs" {
		s.logTab.SetStyle(activeStyle)
	} else {
		s.proxyTab.SetStyle(activeStyle)
	}
}
