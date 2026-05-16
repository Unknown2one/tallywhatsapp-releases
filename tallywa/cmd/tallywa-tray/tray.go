package main

import (
	"log/slog"
	"runtime"

	"fyne.io/systray"
)

// onReady wires the systray menu items and starts a status poller that
// repaints the icon as the service connects / disconnects.
func onReady(srv *dashboardServer, logger *slog.Logger) {
	systray.SetTitle("TallyWhatsApp")
	systray.SetTooltip("TallyWhatsApp · starting…")
	systray.SetIcon(iconForState(stateUnknown))

	mOpen := systray.AddMenuItem("Open dashboard", "Open the TallyWhatsApp dashboard in your browser")
	mStatus := systray.AddMenuItem("Status: starting…", "")
	mStatus.Disable()
	systray.AddSeparator()
	mAutostart := systray.AddMenuItemCheckbox("Start with Windows", "", autostartEnabled())
	systray.AddSeparator()
	mQuit := systray.AddMenuItem("Quit", "Quit the TallyWhatsApp tray")

	go pollStatus(srv, mStatus, logger)

	go func() {
		for {
			select {
			case <-mOpen.ClickedCh:
				if err := openBrowser(srv.URL()); err != nil {
					logger.Warn("open dashboard", "err", err)
				}
			case <-mAutostart.ClickedCh:
				if mAutostart.Checked() {
					if err := disableAutostart(); err != nil {
						logger.Warn("disable autostart", "err", err)
						continue
					}
					mAutostart.Uncheck()
				} else {
					if err := enableAutostart(); err != nil {
						logger.Warn("enable autostart", "err", err)
						continue
					}
					mAutostart.Check()
				}
			case <-mQuit.ClickedCh:
				systray.Quit()
				return
			}
		}
	}()
}

func onExit(logger *slog.Logger) {
	logger.Info("tray exiting")
}

// pollStatus refreshes the menu line and tray icon every few seconds.
// We don't websocket-push to the menu — that's overkill for an icon
// that only changes when WhatsApp connects or drops.
func pollStatus(srv *dashboardServer, item *systray.MenuItem, logger *slog.Logger) {
	last := stateUnknown
	for {
		s := srv.Snapshot()
		state := s.toUIState()
		if state != last {
			systray.SetIcon(iconForState(state))
			systray.SetTooltip("TallyWhatsApp · " + state.tooltip())
			last = state
		}
		item.SetTitle("Status: " + state.label())
		select {
		case <-srv.Done():
			return
		default:
		}
		// Stagger by GOOS — Windows tray flickers if you hammer SetIcon.
		if runtime.GOOS == "windows" {
			sleep(3000)
		} else {
			sleep(2000)
		}
	}
}
