package ui

import (
	"errors"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"frp-tui/internal/client"
	"frp-tui/internal/instance"
	"frp-tui/internal/model"
	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

type operation struct {
	work    func() (string, error)
	success func(string)
}

type screen struct {
	app            *tview.Application
	pages          *tview.Pages
	client         *client.Client
	reconnect      func() error
	shutdown       func() error
	exitErr        error
	list           *tview.List
	details        *tview.TextView
	proxies        *tview.Table
	logs           *tview.TextView
	footer         *tview.TextView
	follow         *tview.Checkbox
	connectionPane *tview.Flex
	detailPane     *tview.Flex
	proxyPane      *tview.Flex
	logPane        *tview.Flex
	workspacePane  *tview.Flex
	workspacePages *tview.Pages
	proxyTab       *tview.Button
	logTab         *tview.Button
	activeTab      string
	snapshot       model.Snapshot
	selected       string
	logID          string
	logLines       []string
	logSeq         uint64
	followLogs     bool
	updating       bool
	busy           bool
	closing        bool
	dialog         tview.Primitive
	dialogReturn   tview.Primitive
	dialogSubmit   func()
	focusables     []tview.Primitive
	proxyFocus     []tview.Primitive
	logFocus       []tview.Primitive
	globalFocus    []tview.Primitive
	selection      atomic.Value
	jobs           chan operation
	done           chan struct{}
	stopped        chan struct{}
	stopOnce       sync.Once
	resize         func(int, int)
}

// Run opens the terminal client. All mutable widget state stays on the UI loop.
func Run(c *client.Client, reconnect, shutdown func() error) error {
	s := &screen{client: c, reconnect: reconnect, shutdown: shutdown, app: tview.NewApplication(), jobs: make(chan operation, 1), done: make(chan struct{}), stopped: make(chan struct{}), followLogs: true}
	s.selection.Store("")
	s.build()
	var start sync.Once
	s.app.SetBeforeDrawFunc(func(screen tcell.Screen) bool {
		width, height := screen.Size()
		s.resize(width, height)
		for _, pane := range []*tview.Flex{s.connectionPane, s.detailPane, s.workspacePane} {
			paneFocus(pane.Box, pane.HasFocus())
		}
		start.Do(func() { go s.worker() })
		return false
	})
	err := s.app.Run()
	s.stopOnce.Do(func() { close(s.done) })
	if err != nil {
		return err
	}
	return s.exitErr
}

func (s *screen) build() {
	applyTheme()
	s.pages = tview.NewPages()
	s.list = tview.NewList().ShowSecondaryText(false).SetHighlightFullLine(true)
	s.list.SetSelectedBackgroundColor(selectionColor).SetSelectedTextColor(controlTextColor)
	s.list.SetChangedFunc(func(i int, _ string, _ string, _ rune) {
		if s.updating || i < 0 || i >= len(s.snapshot.Config.Connections) {
			return
		}
		s.selected = s.snapshot.Config.Connections[i].ID
		s.selection.Store(s.selected)
		s.clearLogs()
		s.renderDetail()
	})
	s.details = tview.NewTextView().SetDynamicColors(true).SetWrap(false)
	s.proxies = tview.NewTable().SetSelectable(true, false).SetFixed(1, 0)
	s.proxies.SetSelectedStyle(tcell.StyleDefault.Background(selectionColor).Foreground(controlTextColor))
	s.proxies.SetSelectedFunc(func(row, _ int) {
		if row > 0 {
			s.editProxy(false)
		}
	})
	s.logs = tview.NewTextView().SetScrollable(true).SetWrap(false)
	s.logs.SetMouseCapture(func(a tview.MouseAction, e *tcell.EventMouse) (tview.MouseAction, *tcell.EventMouse) {
		if a == tview.MouseScrollUp {
			s.followLogs = false
			s.follow.SetChecked(false)
		}
		return a, e
	})
	s.logs.SetInputCapture(func(e *tcell.EventKey) *tcell.EventKey {
		if e.Key() == tcell.KeyPgUp || e.Key() == tcell.KeyUp || e.Key() == tcell.KeyHome {
			s.followLogs = false
			s.follow.SetChecked(false)
		}
		return e
	})
	s.follow = tview.NewCheckbox().SetLabel("F11 自动滚动 ").SetChecked(true).SetChangedFunc(func(v bool) {
		s.followLogs = v
		if v {
			s.logs.ScrollToEnd()
		}
	})
	s.follow.SetFieldTextColor(controlTextColor).SetFieldBackgroundColor(selectionColor).SetActivatedStyle(focusedActionStyle)
	footer := s.buildFooter()
	s.focusables = nil
	newConnection := s.buttons([]shortcut{s.newConnectionAction()})
	listFocus := append([]tview.Primitive(nil), s.focusables...)
	s.focusables = nil
	connectionBar := s.buttons(s.connectionActions())
	connectionFocus := append([]tview.Primitive(nil), s.focusables...)
	s.focusables = nil
	proxyBar := s.buttons(s.proxyActions())
	proxyFocus := append([]tview.Primitive(nil), s.focusables...)
	logHint := tview.NewTextView().SetDynamicColors(true).SetText("[::d]滚轮浏览   ← → 横向滚动[-:-:-]")
	logControls := tview.NewFlex().AddItem(s.follow, 18, 0, false).AddItem(logHint, 0, 1, false)
	s.connectionPane = newPane(" 连接 ").AddItem(s.list, 0, 1, true).AddItem(newConnection, 1, 0, false)
	s.detailPane = newPane(" 连接详情 ").AddItem(s.details, 0, 1, false).AddItem(connectionBar, 1, 0, true)
	s.buildWorkspace(proxyBar, logControls)
	right := tview.NewFlex().SetDirection(tview.FlexRow).AddItem(s.detailPane, 6, 0, false).AddItem(s.workspacePane, 0, 1, true)
	body := tview.NewFlex().AddItem(s.connectionPane, 24, 0, true).AddItem(nil, 1, 0, false).AddItem(right, 0, 1, false)
	main := tview.NewFlex().SetDirection(tview.FlexRow).AddItem(body, 0, 1, true).AddItem(footer, 1, 0, false)
	s.resize = func(width, _ int) {
		s.updateFooter(footer)
		if width < 96 {
			body.ResizeItem(s.connectionPane, 20, 0)
		} else {
			body.ResizeItem(s.connectionPane, 24, 0)
		}
	}
	s.focusables = []tview.Primitive{s.list}
	s.focusables = append(s.focusables, listFocus...)
	s.focusables = append(s.focusables, connectionFocus...)
	s.focusables = append(s.focusables, s.proxyTab, s.logTab)
	s.proxyFocus = append([]tview.Primitive{s.proxies}, proxyFocus...)
	s.logFocus = []tview.Primitive{s.logs, s.follow}
	s.pages.AddPage("main", main, true, true)
	s.app.SetRoot(s.pages, true).EnableMouse(true).EnablePaste(true).SetFocus(s.list)
	s.app.SetInputCapture(func(e *tcell.EventKey) *tcell.EventKey {
		if e.Key() == tcell.KeyCtrlC || e.Key() == tcell.KeyCtrlQ {
			s.quit()
			return nil
		}
		if s.closing {
			return nil
		}
		if s.dialog != nil {
			if e.Key() == tcell.KeyF2 && s.dialogSubmit != nil && !s.busy {
				s.dialogSubmit()
				return nil
			}
			return e
		}
		if e.Key() == tcell.KeyF10 {
			s.quit()
			return nil
		}
		if !s.busy && s.handleShortcut(e) {
			return nil
		}
		switch e.Key() {
		case tcell.KeyCtrlN:
			s.editConnection(true)
			return nil
		case tcell.KeyCtrlE:
			s.editConnection(false)
			return nil
		case tcell.KeyTab, tcell.KeyBacktab:
			order := append([]tview.Primitive(nil), s.focusables...)
			if s.activeTab == "proxies" {
				order = append(order, s.proxyFocus...)
			} else {
				order = append(order, s.logFocus...)
			}
			if s.footer.GetText(false) == "" {
				order = append(order, s.globalFocus...)
			}
			at := 0
			for i, p := range order {
				if p.HasFocus() {
					at = i
					break
				}
			}
			if e.Key() == tcell.KeyTab {
				at = (at + 1) % len(order)
			} else {
				at = (at + len(order) - 1) % len(order)
			}
			s.app.SetFocus(order[at])
			return nil
		}
		return e
	})
	s.renderDetail()
}

func newPane(title string) *tview.Flex {
	pane := tview.NewFlex().SetDirection(tview.FlexRow)
	pane.SetBorder(true).SetTitle(title).SetTitleAlign(tview.AlignLeft)
	return pane
}

func (s *screen) buttons(actions []shortcut) *tview.Flex {
	row := tview.NewFlex()
	for i, action := range actions {
		label := keyLabel(tcell.KeyNames[action.key], action.label)
		button := tview.NewButton(label).SetSelectedFunc(action.run)
		button.SetStyle(actionStyle).SetActivatedStyle(focusedActionStyle)
		row.AddItem(button, tview.TaggedStringWidth(label)+2, 0, false)
		if i < len(actions)-1 {
			row.AddItem(tview.NewBox(), 1, 0, false)
		}
		s.focusables = append(s.focusables, button)
	}
	row.AddItem(nil, 0, 1, false)
	return row
}

func (s *screen) quit() {
	s.exit(false)
}

func (s *screen) quitAll() {
	s.exit(true)
}

func (s *screen) exit(all bool) {
	if s.closing {
		return
	}
	s.closing = true
	s.busy = true
	if all {
		s.footer.SetText("正在停止所有 frpc 和守护进程…")
	} else {
		s.footer.SetText("正在关闭界面…")
	}
	s.stopOnce.Do(func() { close(s.done) })
	// Keep the event loop alive until the worker has finished its last queued update.
	go func() {
		<-s.stopped
		var err error
		if all {
			err = s.shutdown()
		}
		s.app.QueueUpdateDraw(func() {
			if err != nil {
				s.exitErr = fmt.Errorf("全部退出失败：%w", err)
			}
			s.app.Stop()
		})
	}()
}

func (s *screen) worker() {
	defer close(s.stopped)
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	var logID string
	var after uint64
	refresh := func() {
		reconnected := false
		snapshot, err := s.client.State()
		if err != nil {
			select {
			case <-s.done:
				return
			default:
			}
			if startErr := s.reconnect(); errors.Is(startErr, instance.ErrShutdown) {
				s.app.QueueUpdateDraw(s.quit)
				return
			} else if startErr != nil {
				err = startErr
			} else {
				snapshot, err = s.client.State()
				reconnected = err == nil
				logID = ""
				after = 0
			}
		}
		select {
		case <-s.done:
			return
		default:
		}
		if err != nil {
			s.app.QueueUpdateDraw(func() {
				s.footer.SetText("[red]连接失败：" + tview.Escape(err.Error()) + "[-]")
			})
			return
		}
		id := s.selection.Load().(string)
		if id == "" && len(snapshot.Config.Connections) > 0 {
			id = snapshot.Config.Connections[0].ID
		}
		if id != logID {
			logID = id
			after = 0
		}
		var logs model.Logs
		var logErr error
		if id != "" {
			logs, logErr = s.client.Logs(id, after)
			if logErr == nil && logs.LastSeq < after {
				after = 0
				logs, logErr = s.client.Logs(id, 0)
			}
		}
		select {
		case <-s.done:
			return
		default:
		}
		s.app.QueueUpdateDraw(func() {
			if reconnected {
				s.clearLogs()
			}
			s.renderSnapshot(snapshot)
			if logErr == nil && id == s.selected {
				s.renderLogs(id, logs)
			}
		})
		if logErr == nil {
			after = logs.LastSeq
		}
	}
	refresh()
	for {
		select {
		case <-s.done:
			return
		case job := <-s.jobs:
			result, err := job.work()
			select {
			case <-s.done:
				return
			default:
			}
			s.app.QueueUpdateDraw(func() {
				s.busy = false
				if err != nil {
					s.showError(err.Error() + "\n如配置已被其他窗口修改，请取消编辑后重新打开。")
					return
				}
				s.closeDialog()
				if job.success != nil {
					job.success(result)
				}
				s.footer.SetText("[green]操作完成[-]")
			})
			refresh()
		case <-ticker.C:
			refresh()
		}
	}
}

func (s *screen) submit(work func() (string, error), success func(string)) {
	if s.busy || s.closing {
		return
	}
	s.busy = true
	s.footer.SetText("正在处理…")
	s.jobs <- operation{work: work, success: success}
}

func (s *screen) renderSnapshot(snapshot model.Snapshot) {
	s.snapshot = snapshot
	s.updating = true
	s.list.Clear()
	index := -1
	for i, c := range snapshot.Config.Connections {
		status := s.status(c.ID)
		s.list.AddItem(fmt.Sprintf("[%s]●[-] %s", stateColor(status.State), tview.Escape(c.Name)), "", 0, nil)
		if c.ID == s.selected {
			index = i
		}
	}
	if index < 0 && len(snapshot.Config.Connections) > 0 {
		index = 0
		s.selected = snapshot.Config.Connections[0].ID
		s.selection.Store(s.selected)
		s.clearLogs()
	}
	if index >= 0 {
		s.list.SetCurrentItem(index)
	} else {
		s.selected = ""
		s.selection.Store("")
		s.clearLogs()
	}
	s.updating = false
	s.connectionPane.SetTitle(fmt.Sprintf(" 连接 %d ", len(snapshot.Config.Connections)))
	s.renderDetail()
	if !s.busy && !s.closing && s.dialog == nil {
		s.footer.SetText("")
	}
}

func (s *screen) current() (model.Connection, bool) {
	for _, c := range s.snapshot.Config.Connections {
		if c.ID == s.selected {
			c.Proxies = append([]model.Proxy(nil), c.Proxies...)
			return c, true
		}
	}
	return model.Connection{}, false
}

func (s *screen) status(id string) model.Status {
	for _, st := range s.snapshot.Statuses {
		if st.ID == id {
			return st
		}
	}
	return model.Status{ID: id, State: "stopped"}
}

func stateName(state string) string {
	switch state {
	case "starting":
		return "启动中"
	case "running":
		return "运行中"
	case "retrying":
		return "等待重试"
	case "error":
		return "配置错误"
	default:
		return "已停止"
	}
}

func stateColor(state string) string {
	switch state {
	case "running":
		return "#45885a"
	case "starting", "retrying":
		return "#a17b28"
	case "error":
		return "#bf5f5f"
	default:
		return "#697584"
	}
}

func (s *screen) renderDetail() {
	c, ok := s.current()
	row, _ := s.proxies.GetSelection()
	s.proxies.Clear()
	for i, title := range []string{"名称", "类型", "本地地址", "远端端口"} {
		s.proxies.SetCell(0, i, tview.NewTableCell(title).SetTextColor(tview.Styles.SecondaryTextColor).SetSelectable(false))
	}
	if !ok {
		s.updateWorkspaceTabs(0)
		s.details.SetText("尚未添加连接\n[::d]点击左侧“F3 新建连接”开始配置。[-:-:-]")
		return
	}
	s.updateWorkspaceTabs(len(c.Proxies))
	st := s.status(c.ID)
	tls := "关闭"
	if c.TLS {
		tls = "开启"
	}
	text := fmt.Sprintf("[::b]%s[-:-:-]  [%s]%s[-]\n[::d]%s:%d   TLS %s[-:-:-]", tview.Escape(c.Name), stateColor(st.State), stateName(st.State), tview.Escape(c.ServerAddr), c.ServerPort, tls)
	if st.Error != "" {
		text += "\n[red]" + tview.Escape(st.Error) + "[-]"
	} else if st.RetryAt != "" {
		text += "\n下次重试：" + tview.Escape(st.RetryAt)
	}
	s.details.SetText(text)
	for i, p := range c.Proxies {
		for col, value := range []string{p.Name, strings.ToUpper(p.Type), fmt.Sprintf("%s:%d", p.LocalIP, p.LocalPort), strconv.Itoa(p.RemotePort)} {
			cell := tview.NewTableCell(tview.Escape(value)).SetTextColor(tcell.ColorDefault)
			if col == 0 || col == 2 {
				cell.SetExpansion(1)
			}
			s.proxies.SetCell(i+1, col, cell)
		}
	}
	if len(c.Proxies) > 0 {
		if row < 1 {
			row = 1
		}
		if row > len(c.Proxies) {
			row = len(c.Proxies)
		}
		s.proxies.Select(row, 0)
	}
}

func (s *screen) clearLogs() { s.logID = ""; s.logSeq = 0; s.logLines = nil; s.logs.Clear() }

func (s *screen) renderLogs(id string, logs model.Logs) {
	if s.logID != id || logs.LastSeq < s.logSeq {
		s.clearLogs()
		s.logID = id
	}
	for _, entry := range logs.Entries {
		if entry.Seq > s.logSeq {
			s.logLines = append(s.logLines, logText(entry))
		}
	}
	if len(s.logLines) > 1000 {
		s.logLines = s.logLines[len(s.logLines)-1000:]
	}
	if logs.LastSeq != s.logSeq {
		row, col := s.logs.GetScrollOffset()
		s.logs.SetText(strings.Join(s.logLines, "\n"))
		if s.followLogs {
			s.logs.ScrollToEnd()
		} else {
			s.logs.ScrollTo(row, col)
		}
	}
	s.logSeq = logs.LastSeq
}

func logText(entry model.LogEntry) string {
	// frpc already writes a timestamp. Keep its time without repeating the date.
	if len(entry.Text) >= 24 {
		if _, err := time.Parse("2006-01-02 15:04:05.000", entry.Text[:23]); err == nil {
			return entry.Text[11:]
		}
	}
	stamp, err := time.Parse(time.RFC3339, entry.Time)
	if err != nil {
		return entry.Time + " " + entry.Text
	}
	return stamp.Format("15:04:05") + " " + entry.Text
}

func (s *screen) openDialog(p tview.Primitive, width, height int) {
	s.dialogReturn = s.app.GetFocus()
	s.dialogSubmit = nil
	if form, ok := p.(*tview.Form); ok {
		s.formKeys(form)
	}
	s.dialog = p
	centered := tview.NewFlex().AddItem(nil, 0, 1, false).AddItem(p, width, 0, true).AddItem(nil, 0, 1, false)
	box := tview.NewFlex().SetDirection(tview.FlexRow).AddItem(nil, 0, 1, false).AddItem(centered, height, 0, true).AddItem(nil, 0, 1, false)
	s.pages.AddPage("dialog", box, true, true)
	s.app.SetFocus(p)
}

func (s *screen) closeDialog() {
	if s.dialog == nil {
		return
	}
	s.pages.RemovePage("notice")
	s.pages.RemovePage("dialog")
	s.dialog = nil
	s.dialogSubmit = nil
	if s.dialogReturn != nil {
		s.app.SetFocus(s.dialogReturn)
		s.dialogReturn = nil
	}
}

func (s *screen) showError(message string) {
	s.footer.SetText("[red]操作失败[-]")
	previous := s.dialog
	previousSubmit := s.dialogSubmit
	previousFocus := s.app.GetFocus()
	modal := compactModal(message).AddButtons([]string{keyLabel("Esc", "返回")})
	modal.SetDoneFunc(func(_ int, _ string) {
		s.pages.RemovePage("notice")
		s.dialog = previous
		s.dialogSubmit = previousSubmit
		s.app.SetFocus(previousFocus)
	})
	s.dialog = modal
	s.dialogSubmit = nil
	s.pages.AddPage("notice", modal, true, true)
	s.app.SetFocus(modal)
}

func (s *screen) confirm(message string, action func()) {
	s.dialogReturn = s.app.GetFocus()
	modal := compactModal(message).AddButtons([]string{keyLabel("Esc", "取消"), keyLabel("F2", "确认")})
	modal.SetDoneFunc(func(i int, _ string) {
		if s.busy {
			return
		}
		if i == 1 {
			action()
		} else {
			s.closeDialog()
		}
	})
	s.dialog = modal
	s.dialogSubmit = action
	s.pages.AddPage("dialog", modal, true, true)
	s.app.SetFocus(modal)
}

func (s *screen) editConnection(create bool) {
	if s.busy {
		return
	}
	c := model.Connection{ServerPort: 7000, TLS: true}
	if !create {
		var ok bool
		c, ok = s.current()
		if !ok {
			return
		}
	}
	form := compactForm()
	form.SetBorder(true)
	title := " 编辑连接 "
	if create {
		title = " 新建连接 "
	}
	form.SetTitle(title)
	form.AddInputField("名称", c.Name, 40, nil, nil)
	form.AddInputField("服务端地址", c.ServerAddr, 40, nil, nil)
	form.AddInputField("服务端端口", strconv.Itoa(c.ServerPort), 8, tview.InputFieldInteger, nil)
	form.AddPasswordField("Token", c.Token, 40, '•', nil)
	form.AddCheckbox("启用 TLS", c.TLS, nil)
	label := "保存"
	if c.DesiredRunning {
		label = "保存并重启"
	}
	form.AddButton(label, func() {
		draft := c
		draft.Name = strings.TrimSpace(form.GetFormItem(0).(*tview.InputField).GetText())
		draft.ServerAddr = strings.TrimSpace(form.GetFormItem(1).(*tview.InputField).GetText())
		port, err := parsePort(form.GetFormItem(2).(*tview.InputField).GetText())
		if err != nil {
			s.showError("服务端端口：" + err.Error())
			return
		}
		draft.ServerPort = port
		draft.Token = form.GetFormItem(3).(*tview.InputField).GetText()
		draft.TLS = form.GetFormItem(4).(*tview.Checkbox).IsChecked()
		if draft.Name == "" || draft.ServerAddr == "" {
			s.showError("请填写名称和服务端地址。")
			return
		}
		s.submit(func() (string, error) {
			var saved model.Connection
			var err error
			if create {
				saved, err = s.client.Create(draft)
			} else {
				saved, err = s.client.Update(draft)
			}
			return saved.ID, err
		}, func(id string) { s.selected = id; s.selection.Store(id); s.clearLogs() })
	})
	form.AddButton("取消", func() {
		if !s.busy {
			s.closeDialog()
		}
	}).SetCancelFunc(func() {
		if !s.busy {
			s.closeDialog()
		}
	})
	if c.DesiredRunning {
		form.SetTitle(" 编辑连接：保存后连接会短暂中断 ")
	}
	s.openDialog(form, 62, 11)
}

func parsePort(value string) (int, error) {
	v, err := strconv.Atoi(value)
	if err != nil || v < 1 || v > 65535 {
		return 0, fmt.Errorf("请输入 1–65535 之间的整数")
	}
	return v, nil
}

func (s *screen) editProxy(create bool) {
	if s.busy {
		return
	}
	c, ok := s.current()
	if !ok {
		return
	}
	row, _ := s.proxies.GetSelection()
	index := row - 1
	p := model.Proxy{Type: "tcp", LocalIP: "127.0.0.1"}
	if !create {
		if index < 0 || index >= len(c.Proxies) {
			return
		}
		p = c.Proxies[index]
	}
	form := compactForm()
	form.SetBorder(true)
	title := " 编辑代理 "
	if create {
		title = " 添加代理 "
	}
	if c.DesiredRunning {
		title += "保存后连接会短暂中断 "
	}
	form.SetTitle(title)
	form.AddInputField("名称", p.Name, 40, nil, nil)
	selectedType := 0
	if p.Type == "udp" {
		selectedType = 1
	}
	form.AddDropDown("类型", []string{"TCP", "UDP"}, selectedType, nil)
	form.GetFormItem(1).(*tview.DropDown).
		SetListStyles(actionStyle, focusedActionStyle).
		SetFocusedStyle(focusedActionStyle).SetPrefixStyle(focusedActionStyle)
	form.AddInputField("本地地址", p.LocalIP, 40, nil, nil)
	local, remote := "", ""
	if p.LocalPort != 0 {
		local = strconv.Itoa(p.LocalPort)
	}
	if p.RemotePort != 0 {
		remote = strconv.Itoa(p.RemotePort)
	}
	form.AddInputField("本地端口", local, 8, tview.InputFieldInteger, nil)
	form.AddInputField("远端端口", remote, 8, tview.InputFieldInteger, nil)
	label := "保存"
	if c.DesiredRunning {
		label = "保存并重启"
	}
	form.AddButton(label, func() {
		draft := p
		draft.Name = strings.TrimSpace(form.GetFormItem(0).(*tview.InputField).GetText())
		_, typ := form.GetFormItem(1).(*tview.DropDown).GetCurrentOption()
		draft.Type = strings.ToLower(typ)
		draft.LocalIP = strings.TrimSpace(form.GetFormItem(2).(*tview.InputField).GetText())
		var err error
		draft.LocalPort, err = parsePort(form.GetFormItem(3).(*tview.InputField).GetText())
		if err != nil {
			s.showError("本地端口：" + err.Error())
			return
		}
		draft.RemotePort, err = parsePort(form.GetFormItem(4).(*tview.InputField).GetText())
		if err != nil {
			s.showError("远端端口：" + err.Error())
			return
		}
		if draft.Name == "" || draft.LocalIP == "" {
			s.showError("请填写名称和本地地址。")
			return
		}
		updated := c
		updated.Proxies = append([]model.Proxy(nil), c.Proxies...)
		if create {
			updated.Proxies = append(updated.Proxies, draft)
		} else {
			updated.Proxies[index] = draft
		}
		s.submit(func() (string, error) { _, err := s.client.Update(updated); return "", err }, nil)
	})
	form.AddButton("取消", func() {
		if !s.busy {
			s.closeDialog()
		}
	}).SetCancelFunc(func() {
		if !s.busy {
			s.closeDialog()
		}
	})
	s.openDialog(form, 62, 11)
}

func (s *screen) deleteConnection() {
	if s.busy {
		return
	}
	c, ok := s.current()
	if !ok {
		return
	}
	message := fmt.Sprintf("删除连接“%s”及其全部代理？", c.Name)
	if c.DesiredRunning {
		message += "\n该连接的 frpc 进程将停止。"
	}
	s.confirm(message, func() {
		s.submit(func() (string, error) { return "", s.client.Delete(c.ID, c.Revision) }, func(string) { s.selected = ""; s.selection.Store(""); s.clearLogs() })
	})
}

func (s *screen) deleteProxy() {
	if s.busy {
		return
	}
	c, ok := s.current()
	if !ok {
		return
	}
	row, _ := s.proxies.GetSelection()
	index := row - 1
	if index < 0 || index >= len(c.Proxies) {
		return
	}
	message := fmt.Sprintf("删除代理“%s”？", c.Proxies[index].Name)
	if c.DesiredRunning {
		message += "\n保存后重启该连接，现有代理会短暂中断。"
	}
	s.confirm(message, func() {
		c.Proxies = append(c.Proxies[:index], c.Proxies[index+1:]...)
		s.submit(func() (string, error) { _, err := s.client.Update(c); return "", err }, nil)
	})
}

func (s *screen) action(action string) {
	if s.busy {
		return
	}
	c, ok := s.current()
	if !ok {
		return
	}
	s.submit(func() (string, error) { _, err := s.client.Action(c.ID, action, c.Revision); return "", err }, nil)
}

func (s *screen) settings() {
	if s.busy {
		return
	}
	cfg := s.snapshot.Config
	form := compactForm()
	form.SetBorder(true).SetTitle(" 设置 ")
	form.AddInputField("frpc 绝对路径", cfg.FrpcPath, 40, nil, nil)
	form.AddButton("保存", func() {
		path := strings.TrimSpace(form.GetFormItem(0).(*tview.InputField).GetText())
		if !filepath.IsAbs(path) {
			s.showError("请填写 frpc 可执行文件的绝对路径。")
			return
		}
		s.submit(func() (string, error) {
			_, err := s.client.Settings(model.Settings{Revision: cfg.Revision, FrpcPath: path})
			return "", err
		}, nil)
	})
	form.AddButton("取消", func() {
		if !s.busy {
			s.closeDialog()
		}
	}).SetCancelFunc(func() {
		if !s.busy {
			s.closeDialog()
		}
	})
	s.openDialog(form, 64, 7)
}
