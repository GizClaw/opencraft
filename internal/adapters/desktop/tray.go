package desktop

import (
	"fmt"

	"github.com/GizClaw/opencraft/internal/foundation/version"

	"github.com/wailsapp/wails/v3/pkg/application"
)

// desktopTray mirrors the v2 tray surface: disabled version/about items,
// then Show and Quit. Labels follow the persisted desktop language and are
// refreshed whenever the UI reports a language change.
type desktopTray struct {
	tray    *application.SystemTray
	version *application.MenuItem
	about   *application.MenuItem
	show    *application.MenuItem
	quit    *application.MenuItem
	desktop *Desktop
}

// SetupTray builds the system tray. onShow/onQuit are provided by the entry
// point so tray actions go through the same v3 lifecycle as window controls.
func (d *Desktop) SetupTray(
	app *application.App,
	icon []byte,
	onShow func(),
	onQuit func(),
) *desktopTray {
	texts := d.core.Shell.Texts()
	menu := app.NewMenu()
	versionItem := menu.Add(
		fmt.Sprintf(texts.VersionFormat, version.ServiceVersion))
	versionItem.SetEnabled(false)
	versionItem.SetTooltip(texts.VersionTooltip)
	aboutItem := menu.Add(texts.About)
	aboutItem.SetEnabled(false)
	aboutItem.SetTooltip(texts.VersionTooltip)
	menu.AddSeparator()
	showItem := menu.Add(texts.Show)
	showItem.SetTooltip(texts.TrayTooltip)
	showItem.OnClick(func(*application.Context) {
		if onShow != nil {
			onShow()
		}
	})
	menu.AddSeparator()
	quitItem := menu.Add(texts.Quit)
	quitItem.SetTooltip(texts.TrayTooltip)
	quitItem.OnClick(func(*application.Context) {
		if onQuit != nil {
			onQuit()
		}
	})

	tray := app.SystemTray.New()
	tray.SetIcon(icon)
	tray.SetTooltip(texts.TrayTooltip)
	tray.SetMenu(menu)

	t := &desktopTray{
		tray:    tray,
		version: versionItem,
		about:   aboutItem,
		show:    showItem,
		quit:    quitItem,
		desktop: d,
	}
	d.core.Shell.SetLanguageChangedListener(t.refresh)
	return t
}

// refresh copies the current locale into the tray labels.
func (t *desktopTray) refresh() {
	if t == nil || t.desktop == nil {
		return
	}
	texts := t.desktop.core.Shell.Texts()
	t.tray.SetTooltip(texts.TrayTooltip)
	t.version.SetLabel(fmt.Sprintf(texts.VersionFormat, version.ServiceVersion))
	t.version.SetTooltip(texts.VersionTooltip)
	t.about.SetLabel(texts.About)
	t.about.SetTooltip(texts.VersionTooltip)
	t.show.SetLabel(texts.Show)
	t.show.SetTooltip(texts.TrayTooltip)
	t.quit.SetLabel(texts.Quit)
	t.quit.SetTooltip(texts.TrayTooltip)
}
