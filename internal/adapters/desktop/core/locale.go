package core

import (
	"os"
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

// DefaultLanguage prefers the process locale and falls back to English.
func DefaultLanguage() string {
	for _, key := range []string{"LC_ALL", "LC_MESSAGES", "LANG"} {
		if strings.HasPrefix(strings.ToLower(os.Getenv(key)), "zh") {
			return "zh"
		}
	}
	return "en"
}
