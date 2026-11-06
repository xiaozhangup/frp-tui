package ui

import (
	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

type shortcut struct {
	key   tcell.Key
	label string
	run   func()
}

func keyLabel(key, label string) string {
	return key + " " + label
}

func (s *screen) globalActions() []shortcut {
	return []shortcut{
		{tcell.KeyF1, "帮助", s.help},
		{tcell.KeyF2, "设置", s.settings},
		{tcell.KeyF5, "刷新", func() { s.submit(func() (string, error) { return "", nil }, nil) }},
		{tcell.KeyF10, "关闭界面", s.quit},
		{tcell.KeyF12, "全部退出", s.quitAll},
	}
}

func (s *screen) connectionActions() []shortcut {
	return []shortcut{
		{tcell.KeyF4, "编辑", func() { s.editConnection(false) }},
		{tcell.KeyF6, "启动", func() { s.action("start") }},
		{tcell.KeyF7, "停止", func() { s.action("stop") }},
		{tcell.KeyF8, "重启", func() { s.action("restart") }},
		{tcell.KeyF9, "删除", s.deleteConnection},
	}
}

func (s *screen) proxyActions() []shortcut {
	return []shortcut{
		{tcell.KeyF3, "添加", func() { s.editProxy(true) }},
		{tcell.KeyF4, "编辑", func() { s.editProxy(false) }},
		{tcell.KeyF9, "删除", s.deleteProxy},
	}
}

func (s *screen) newConnectionAction() shortcut {
	return shortcut{tcell.KeyF3, "新建连接", func() { s.editConnection(true) }}
}

func (s *screen) handleShortcut(event *tcell.EventKey) bool {
	if event.Key() == tcell.KeyRune && event.Modifiers() == tcell.ModAlt {
		switch event.Rune() {
		case '1':
			s.selectTab("proxies")
			return true
		case '2':
			s.selectTab("logs")
			return true
		}
	}
	if event.Modifiers() != tcell.ModNone {
		return false
	}
	if event.Key() == tcell.KeyF11 {
		s.follow.SetChecked(!s.follow.IsChecked())
		return true
	}
	actions := s.globalActions()
	if s.workspacePane.HasFocus() {
		if s.activeTab == "proxies" {
			actions = append(actions, s.proxyActions()...)
		} else if event.Key() == tcell.KeyF3 || event.Key() == tcell.KeyF4 || event.Key() == tcell.KeyF9 {
			return true
		}
	}
	actions = append(actions, s.newConnectionAction())
	actions = append(actions, s.connectionActions()...)
	for _, a := range actions {
		if event.Key() == a.key {
			a.run()
			return true
		}
	}
	return false
}

func (s *screen) help() {
	if s.busy {
		return
	}
	s.dialogReturn = s.app.GetFocus()
	modal := compactModal("F2 设置  F5 刷新\nF10 关闭界面，保留连接\nF12 全部退出\n停止 frpc、守护进程和界面\nF3 新建  F4 编辑  F9 删除\n操作当前聚焦的连接或代理\nF6 启动  F7 停止  F8 重启\nAlt+1 代理  Alt+2 日志\nF11 自动滚动\nTab / Shift+Tab 切换焦点\nEnter 执行选中按钮\nF2 保存或确认，Esc 取消\nCtrl+N 新建  Ctrl+E 编辑\nCtrl+Q 关闭界面").AddButtons([]string{keyLabel("Esc", "返回")})
	modal.SetDoneFunc(func(int, string) { s.closeDialog() })
	s.dialog = modal
	s.dialogSubmit = nil
	s.pages.AddPage("dialog", modal, true, true)
	s.app.SetFocus(modal)
}

func (s *screen) formKeys(form *tview.Form) {
	save := form.GetButton(0)
	save.SetLabel(keyLabel("F2", save.GetLabel()))
	cancel := form.GetButton(1)
	cancel.SetLabel(keyLabel("Esc", cancel.GetLabel()))
	s.dialogSubmit = func() {
		save.InputHandler()(tcell.NewEventKey(tcell.KeyEnter, 0, tcell.ModNone), func(p tview.Primitive) { s.app.SetFocus(p) })
	}
}
