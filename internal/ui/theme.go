package ui

import (
	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

var (
	accentColor        = tcell.NewHexColor(0x5f9ed1)
	selectionColor     = tcell.NewHexColor(0x253b50)
	controlTextColor   = tcell.NewHexColor(0xf1f5f9)
	actionStyle        = tcell.StyleDefault.Background(tcell.NewHexColor(0x355574)).Foreground(controlTextColor)
	focusedActionStyle = tcell.StyleDefault.Background(tcell.NewHexColor(0x88c8f7)).Foreground(tcell.NewHexColor(0x102337)).Bold(true)
)

func applyTheme() {
	// Default colors preserve the user's terminal palette, including transparency.
	tview.Styles.PrimitiveBackgroundColor = tcell.ColorDefault
	tview.Styles.PrimaryTextColor = tcell.ColorDefault
	tview.Styles.SecondaryTextColor = tcell.ColorDefault
	tview.Styles.BorderColor = tcell.ColorDefault
	tview.Styles.TitleColor = tcell.ColorDefault
	// These colors are used only by inputs and selected controls.
	tview.Styles.ContrastBackgroundColor = selectionColor
	tview.Styles.MoreContrastBackgroundColor = selectionColor
	tview.Borders.HorizontalFocus = tview.Borders.Horizontal
	tview.Borders.VerticalFocus = tview.Borders.Vertical
	tview.Borders.TopLeftFocus = tview.Borders.TopLeft
	tview.Borders.TopRightFocus = tview.Borders.TopRight
	tview.Borders.BottomLeftFocus = tview.Borders.BottomLeft
	tview.Borders.BottomRightFocus = tview.Borders.BottomRight
}

func paneFocus(box *tview.Box, focused bool) {
	if focused {
		box.SetBorderColor(accentColor).SetBorderAttributes(tcell.AttrBold)
	} else {
		box.SetBorderColor(tcell.ColorDefault).SetBorderAttributes(tcell.AttrDim)
	}
}

func compactForm() *tview.Form {
	f := tview.NewForm().SetItemPadding(0).
		SetFieldBackgroundColor(selectionColor).SetFieldTextColor(controlTextColor).
		SetButtonStyle(actionStyle).SetButtonActivatedStyle(focusedActionStyle)
	f.SetBorderColor(accentColor)
	return f
}

func compactModal(message string) *tview.Modal {
	m := tview.NewModal().SetText(tview.Escape(message)).
		SetBackgroundColor(tcell.ColorDefault).
		SetButtonStyle(actionStyle).SetButtonActivatedStyle(focusedActionStyle)
	m.Box.SetBackgroundColor(tcell.ColorDefault).SetBorderColor(accentColor)
	return m
}
