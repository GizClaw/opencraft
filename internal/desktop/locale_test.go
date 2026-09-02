package desktop

import "testing"

func TestNormalizeDesktopLanguage(t *testing.T) {
	tests := map[string]string{
		"zh":       "zh",
		"zh-CN":    "zh",
		"ZH_HANS":  "zh",
		"en":       "en",
		"en-US":    "en",
		"fr":       "en",
		"":         "en",
		"  EN-gb ": "en",
	}
	for in, want := range tests {
		if got := normalizeDesktopLanguage(in); got != want {
			t.Errorf("normalizeDesktopLanguage(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestDesktopTextsFor(t *testing.T) {
	if got := desktopTextsFor("zh").quitDialogConfirm; got != "继续退出" {
		t.Fatalf("zh confirm = %q, want 继续退出", got)
	}
	if got := desktopTextsFor("en-US").quitDialogConfirm; got != "Continue" {
		t.Fatalf("en confirm = %q, want Continue", got)
	}
	if got := desktopTextsFor("fr").quitDialogConfirm; got != "Continue" {
		t.Fatalf("unsupported fallback confirm = %q, want Continue", got)
	}
}
