package icona

import (
	"bytes"
	"testing"
)

func TestIcon(t *testing.T) {
	img := App(32)
	if img.Bounds().Dx() != 32 {
		t.Fatal("dimensione sbagliata")
	}
	// Il centro dell'icona non è trasparente, gli angoli sì.
	if _, _, _, a := img.At(16, 16).RGBA(); a == 0 {
		t.Fatal("centro trasparente: icona non caricata?")
	}
	if _, _, _, a := img.At(0, 0).RGBA(); a != 0 {
		t.Fatal("l'angolo dovrebbe essere trasparente")
	}
	if ico := ICO(32); !bytes.HasPrefix(ico, []byte{0, 0, 1, 0, 1, 0}) {
		t.Fatal("intestazione ICO errata")
	}
}
