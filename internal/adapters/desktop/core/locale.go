package core

import (
	"strings"
)

// DesktopTexts is the zh/en copy for native desktop surfaces.
type DesktopTexts struct {
	TrayTooltip       string
	VersionFormat     string
	VersionTooltip    string
	About             string
	Show              string
	Quit              string
	QuitDialogTitle   string
	QuitDialogMessage string
	QuitDialogConfirm string
	QuitDialogCancel  string
	NotifyDone        string
	NotifyFailed      string
	NotifyCancelled   string
	NotifyInterrupted string
	NotifyInteract    string
	// Menu is the native menu bar's copy (macOS). The tray stays flat
	// because it is four items; the menu bar needs the grouping.
	Menu MenuTexts
}

// MenuTexts is the menu bar's copy. Every title and every item the app
// owns is localized here; the few *Formats take the product name, which
// the launcher may have qualified ("OpenCraft (dev)").
type MenuTexts struct {
	// Menu titles.
	File, Edit, View, Chat, Window, Help string
	// Application menu.
	AboutFormat, HideFormat, QuitFormat string
	HideOthers, ShowAll                 string
	Settings                            string
	// File menu.
	NewChat, CloseWindow string
	// Edit menu: the platform's items, relabelled.
	Undo, Redo, Cut, Copy, Paste, Delete, SelectAll string
	// View menu.
	CommandPalette, FilesPanel, GitPanel, CycleTheme string
	ResetZoom, ZoomIn, ZoomOut, FullScreen, DevTools string
	// Chat menu.
	FocusComposer, CopyLastReply, StopReply string
	PrevSession, NextSession                string
	// SessionSlot labels a session-slot row ("Session %d"): the digit is
	// the row's number in the sidebar, which is why the copy carries it.
	SessionSlot                   string
	PrevWorkspace, NextWorkspace  string
	KeyboardShortcuts, Repository string
	// Window menu.
	Minimise, Zoom, BringAllToFront string
}

var desktopLocales = map[string]DesktopTexts{
	"zh": {
		TrayTooltip:       "OpenCraft",
		VersionFormat:     "OpenCraft v%s",
		VersionTooltip:    "应用版本",
		About:             "本地优先的工作伙伴 · 基于 flowcraft",
		Show:              "打开 OpenCraft",
		Quit:              "退出",
		QuitDialogTitle:   "退出 OpenCraft",
		QuitDialogMessage: "退出后，OpenCraft 的定时任务将不再执行。\n确定要退出吗？",
		QuitDialogConfirm: "继续退出",
		QuitDialogCancel:  "取消",
		NotifyDone:        "任务完成",
		NotifyFailed:      "任务失败",
		NotifyCancelled:   "任务已取消",
		NotifyInterrupted: "任务已中断",
		NotifyInteract:    "需要你的输入",
		Menu: MenuTexts{
			File: "文件", Edit: "编辑", View: "显示", Chat: "对话",
			Window: "窗口", Help: "帮助",
			AboutFormat: "关于 %s", HideFormat: "隐藏 %s",
			QuitFormat: "退出 %s",
			HideOthers: "隐藏其他", ShowAll: "全部显示",
			Settings: "设置…",
			NewChat:  "新建对话", CloseWindow: "关闭窗口",
			Undo: "撤销", Redo: "重做", Cut: "剪切", Copy: "拷贝",
			Paste: "粘贴", Delete: "删除", SelectAll: "全选",
			CommandPalette: "命令面板", FilesPanel: "文件面板",
			GitPanel: "Git 面板", CycleTheme: "切换主题",
			ResetZoom: "实际大小", ZoomIn: "放大", ZoomOut: "缩小",
			FullScreen: "进入全屏幕", DevTools: "开发者工具",
			FocusComposer: "聚焦输入框", CopyLastReply: "复制最后一条回复",
			StopReply:   "停止当前回复",
			PrevSession: "上一个会话", NextSession: "下一个会话",
			SessionSlot:   "会话 %d",
			PrevWorkspace: "上一个工作区", NextWorkspace: "下一个工作区",
			KeyboardShortcuts: "键盘快捷键",
			Repository:        "OpenCraft 项目主页",
			Minimise:          "最小化", Zoom: "缩放",
			BringAllToFront: "前置全部窗口",
		},
	},
	"en": {
		TrayTooltip:       "OpenCraft",
		VersionFormat:     "OpenCraft v%s",
		VersionTooltip:    "Application version",
		About:             "A local-first work partner built on flowcraft",
		Show:              "Show OpenCraft",
		Quit:              "Quit",
		QuitDialogTitle:   "Quit OpenCraft?",
		QuitDialogMessage: "Scheduled tasks will stop running when OpenCraft exits.\nAre you sure you want to quit?",
		QuitDialogConfirm: "Continue",
		QuitDialogCancel:  "Cancel",
		NotifyDone:        "Task finished",
		NotifyFailed:      "Task failed",
		NotifyCancelled:   "Task cancelled",
		NotifyInterrupted: "Task interrupted",
		NotifyInteract:    "Input needed",
		Menu: MenuTexts{
			File: "File", Edit: "Edit", View: "View", Chat: "Chat",
			Window: "Window", Help: "Help",
			AboutFormat: "About %s", HideFormat: "Hide %s",
			QuitFormat: "Quit %s",
			HideOthers: "Hide Others", ShowAll: "Show All",
			Settings: "Settings…",
			NewChat:  "New Chat", CloseWindow: "Close Window",
			Undo: "Undo", Redo: "Redo", Cut: "Cut", Copy: "Copy",
			Paste: "Paste", Delete: "Delete", SelectAll: "Select All",
			CommandPalette: "Command Palette", FilesPanel: "File Panel",
			GitPanel: "Git Panel", CycleTheme: "Cycle Theme",
			ResetZoom: "Actual Size", ZoomIn: "Zoom In", ZoomOut: "Zoom Out",
			FullScreen: "Enter Full Screen", DevTools: "Developer Tools",
			FocusComposer: "Focus Composer", CopyLastReply: "Copy Last Reply",
			StopReply:   "Stop Reply",
			PrevSession: "Previous Session", NextSession: "Next Session",
			SessionSlot:   "Session %d",
			PrevWorkspace: "Previous Workspace", NextWorkspace: "Next Workspace",
			KeyboardShortcuts: "Keyboard Shortcuts",
			Repository:        "OpenCraft on GitHub",
			Minimise:          "Minimize", Zoom: "Zoom",
			BringAllToFront: "Bring All to Front",
		},
	},
}

// NormalizeLanguage maps a browser/stored locale to zh/en.
func NormalizeLanguage(language string) string {
	language = strings.ToLower(strings.TrimSpace(language))
	if strings.HasPrefix(language, "zh") {
		return "zh"
	}
	return "en"
}

// TextsFor returns native copy for a language.
func TextsFor(language string) DesktopTexts {
	return desktopLocales[NormalizeLanguage(language)]
}
