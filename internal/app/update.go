package app

import (
	"context"
	"time"

	"github.com/egoist/mygo/ui"

	"myfiles/cmd/myfiles/locales"
)

// Updates are looked for as the program starts, on another thread, which
// is not in its way, and then every few hours for as long as it runs: a
// file manager stays open for days.
const (
	updateEvery = 6 * time.Hour
	// updateLimit is how long a check and its download may take.
	updateLimit = 15 * time.Minute
)

// watchUpdates looks for newer versions from now on.
func (a *App) watchUpdates() {
	a.desk.post(a.checkUpdate)
	go func() {
		for range time.Tick(updateEvery) {
			a.desk.post(a.checkUpdate)
		}
	}()
}

// checkUpdate looks for a newer version, on the main thread, unless the
// settings say not to or one is there already: found, it is downloaded
// and put in place of this one on another thread, with nothing shown
// until it is ready. The program goes on as it is; the button that then
// shows starts it again, as the new version.
func (a *App) checkUpdate() {
	if !a.set.AutoUpdate || a.updating || a.updateReady != "" {
		return
	}
	a.updating = true
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), updateLimit)
		defer cancel()
		// What goes wrong, as being offline, is tried again at the next
		// check: nothing of it shows.
		version, install, err := a.desk.update(ctx)
		if err == nil && version != "" {
			err = install(ctx)
		}
		a.desk.post(func() {
			a.updating = false
			if err == nil && version != "" {
				a.updateReady = version
				a.invalidate()
			}
		})
	}()
}

// updateButton shows, once a newer version is in place, the button that
// starts the program again with it.
func (w *Window) updateButton(c *ui.Context) {
	a := w.app
	if a.updateReady == "" {
		return
	}
	th := c.Theme()
	b := ui.ButtonBase(c.Key("restart")).Height(22).Padding(0, 9, 0, 7).Gap(5).Radius(11).Shrink(0).Center().
		Background(th.Accent).Tooltip(locales.UpdateRestartTip(a.updateReady)).TabStop(false)
	b.Draw(func(p *ui.Painter, r ui.Rect) {
		if b.Hovered() {
			p.Fill(r, th.AccentText.Alpha(0.14), 11)
		}
	})
	b.Children(func() {
		ui.Icon(c, icoUpdate).FontSize(13).TextColor(th.AccentText)
		ui.Text(c, locales.UpdateRestart()).SingleLine().FontWeight(600).TextColor(th.AccentText)
	})
	if b.Clicked() {
		// The tabs come back: the session is written as the windows close.
		for _, win := range a.windows {
			win.save()
		}
		a.desk.relaunch()
	}
}
