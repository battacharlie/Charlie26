//go:build windows

package main

import (
	"os/exec"

	"fyne.io/systray"
	"golang.org/x/sys/windows"
)

// Su Windows il programma non mostra la finestra nera: vive come icona
// nell'area di notifica, vicino all'orologio.
const desktopHint = "  Il programma resta attivo come icona vicino all'orologio (area di notifica)."

func showError(msg string) {
	text, _ := windows.UTF16PtrFromString(msg)
	title, _ := windows.UTF16PtrFromString("Intercom regia")
	const mbOK, mbIconError, mbTopMost = 0x0, 0x10, 0x40000
	_, _ = windows.MessageBox(0, text, title, mbOK|mbIconError|mbTopMost)
}

func runDesktop(d desktop) {
	systray.Run(func() {
		systray.SetIcon(iconICO(32))
		systray.SetTitle("Intercom regia")
		systray.SetTooltip("Intercom regia – tecnici: " + d.appURL)
		openConsole := systray.AddMenuItem("Apri console di regia", "Apre la console nel browser")
		openFolder := systray.AddMenuItem("Apri cartella dati", "Evento, certificati e registro")
		systray.AddSeparator()
		quit := systray.AddMenuItem("Esci (spegne l'intercom)", "Scollega tutti i tecnici e chiude il programma")
		// Clic sinistro sull'icona: apre la console. Clic destro: menu.
		systray.SetOnTapped(func() { openBrowser(d.consoleURL) })
		go func() {
			for {
				select {
				case <-openConsole.ClickedCh:
					openBrowser(d.consoleURL)
				case <-openFolder.ClickedCh:
					_ = exec.Command("explorer", d.dataDir).Start()
				case <-quit.ClickedCh:
					systray.Quit()
					return
				case <-d.quit:
					systray.Quit()
					return
				}
			}
		}()
	}, func() {})
}
