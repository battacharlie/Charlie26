//go:build !windows

package main

import (
	"fmt"
	"os"
	"os/signal"
	"syscall"
)

const desktopHint = "  Chiudi questa finestra (o premi Ctrl+C) per spegnere."

func showError(msg string) {
	fmt.Fprintln(os.Stderr, "\n  ERRORE: "+msg+"\n")
}

func runDesktop(d desktop) {
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	select {
	case <-stop:
	case <-d.quit:
	}
}
