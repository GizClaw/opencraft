package desktop

import (
	"os"
	"strings"
)

// desktopTexts is the zh/en copy used by native desktop surfaces that
// cannot read the frontend's i18next resources: the tray/menu-bar menu
// and the native exit confirmation dialog.
type desktopTexts struct {
	trayTooltip       string
	versionFormat     string
	versionTooltip    string
	about             string
	aboutTooltip      string
	show              string
	showTooltip       string
	quit              string
	quitTooltip       string
	quitDialogTitle   string
	quitDialogMessage string
	quitDialogConfirm string
	quitDialogCancel  string
}

// desktopLocales holds the two supported desktop languages. They mirror
// the frontend's supportedLngs (zh/en); anything else maps to English.
var desktopLocales = map[string]desktopTexts{
	"zh": {
		trayTooltip:       "OpenCraft",
		versionFormat:     "OpenCraft v%s",
		versionTooltip:    "应用版本",
		about:             "本地优先的工作伙伴 · 基于 flowcraft",
		aboutTooltip:      "关于 OpenCraft",
		show:              "打开 OpenCraft",
		showTooltip:       "显示 OpenCraft 主窗口",
		quit:              "退出",
		quitTooltip:       "退出 OpenCraft",
		quitDialogTitle:   "退出 OpenCraft",
		quitDialogMessage: "退出后，OpenCraft 的定时任务将不再执行。\n确定要退出吗？",
		quitDialogConfirm: "继续退出",
		quitDialogCancel:  "取消",
	},
	"en": {
		trayTooltip:       "OpenCraft",
		versionFormat:     "OpenCraft v%s",
		versionTooltip:    "Application version",
		about:             "A local-first work partner built on flowcraft",
		aboutTooltip:      "About OpenCraft",
		show:              "Show OpenCraft",
		showTooltip:       "Show the OpenCraft window",
		quit:              "Quit",
		quitTooltip:       "Quit OpenCraft",
		quitDialogTitle:   "Quit OpenCraft?",
		quitDialogMessage: "Scheduled tasks will stop running when OpenCraft exits.\nAre you sure you want to quit?",
		quitDialogConfirm: "Continue",
		quitDialogCancel:  "Cancel",
	},
}

// normalizeDesktopLanguage maps a browser or stored locale to the
// desktop's supported zh/en set.
func normalizeDesktopLanguage(language string) string {
	language = strings.ToLower(strings.TrimSpace(language))
	if strings.HasPrefix(language, "zh") {
		return "zh"
	}
	return "en"
}

// desktopTextsFor returns the copy for a language, falling back to
// English for unsupported values.
func desktopTextsFor(language string) desktopTexts {
	return desktopLocales[normalizeDesktopLanguage(language)]
}

// defaultDesktopLanguage is used before the frontend reports its
// detected language. It prefers the process locale and falls back to
// English (the frontend's fallback language).
func defaultDesktopLanguage() string {
	for _, key := range []string{"LC_ALL", "LC_MESSAGES", "LANG"} {
		if strings.HasPrefix(strings.ToLower(os.Getenv(key)), "zh") {
			return "zh"
		}
	}
	return "en"
}
