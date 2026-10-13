package sfu

import (
	"bytes"
	"encoding/base64"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"testing"
)

func dataURL(mime string, b []byte) string {
	return "data:" + mime + ";base64," + base64.StdEncoding.EncodeToString(b)
}

func TestParseAvatar(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 160, 160))
	img.Set(5, 5, color.RGBA{255, 0, 0, 255})
	var jb, pb bytes.Buffer
	_ = jpeg.Encode(&jb, img, &jpeg.Options{Quality: 80})
	_ = png.Encode(&pb, img)

	if _, mime, err := parseAvatar(dataURL("image/jpeg", jb.Bytes())); err != nil || mime != "image/jpeg" {
		t.Fatalf("JPEG valido rifiutato: %v", err)
	}
	if _, _, err := parseAvatar(dataURL("image/png", pb.Bytes())); err != nil {
		t.Fatalf("PNG valido rifiutato: %v", err)
	}
	big := image.NewRGBA(image.Rect(0, 0, 1000, 10))
	var bb bytes.Buffer
	_ = png.Encode(&bb, big)
	for name, u := range map[string]string{
		"SVG (può contenere script)": dataURL("image/svg+xml", []byte("<svg onload=alert(1)>")),
		"non immagine":               dataURL("image/jpeg", []byte("ciao, non sono un jpeg")),
		"PNG dichiarato come JPEG":   dataURL("image/jpeg", pb.Bytes()),
		"troppo grande in pixel":     dataURL("image/png", bb.Bytes()),
		"troppo pesante":             dataURL("image/jpeg", make([]byte, 60<<10)),
		"base64 rotto":               "data:image/png;base64,@@@",
	} {
		if _, _, err := parseAvatar(u); err == nil {
			t.Errorf("%s: doveva essere rifiutato", name)
		}
	}
}
