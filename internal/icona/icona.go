// Package icona fornisce l'icona di CHARLIE26 (walkie-talkie), usata
// nell'area di notifica e nell'eseguibile Windows.
package icona

import (
	"bytes"
	_ "embed"
	"encoding/binary"
	"image"
	"image/png"

	"golang.org/x/image/draw"
)

//go:embed icona.png
var iconPNG []byte

// App restituisce l'icona alla dimensione richiesta (lato in pixel).
func App(size int) image.Image {
	src, err := png.Decode(bytes.NewReader(iconPNG))
	if err != nil {
		return image.NewNRGBA(image.Rect(0, 0, size, size))
	}
	dst := image.NewNRGBA(image.Rect(0, 0, size, size))
	draw.CatmullRom.Scale(dst, dst.Bounds(), src, src.Bounds(), draw.Over, nil)
	return dst
}

// ICO restituisce l'icona in formato .ico (con un'immagine PNG dentro,
// come accettato da Windows Vista in poi).
func ICO(size int) []byte {
	var p bytes.Buffer
	_ = png.Encode(&p, App(size))
	var b bytes.Buffer
	_ = binary.Write(&b, binary.LittleEndian, [3]uint16{0, 1, 1}) // intestazione: icona, 1 immagine
	dim := byte(size)
	if size >= 256 {
		dim = 0
	}
	b.Write([]byte{dim, dim, 0, 0})
	_ = binary.Write(&b, binary.LittleEndian, [2]uint16{1, 32})
	_ = binary.Write(&b, binary.LittleEndian, [2]uint32{uint32(p.Len()), 22})
	b.Write(p.Bytes())
	return b.Bytes()
}
