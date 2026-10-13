// Package icona disegna l'icona del programma (microfono bianco su tondo
// rosso), usata nell'area di notifica e nell'eseguibile Windows.
package icona

import (
	"bytes"
	"encoding/binary"
	"image"
	"image/color"
	"image/png"
	"math"
)

// App disegna l'icona del programma: un microfono bianco su un tondo rosso.
func App(size int) image.Image {
	img := image.NewNRGBA(image.Rect(0, 0, size, size))
	s := float64(size)
	red := color.NRGBA{0xe5, 0x37, 0x2b, 0xff}
	white := color.NRGBA{0xff, 0xff, 0xff, 0xff}
	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			fx, fy := (float64(x)+.5)/s, (float64(y)+.5)/s
			// cerchio di sfondo
			if math.Hypot(fx-.5, fy-.5) > .48 {
				continue
			}
			c := red
			// capsula del microfono
			if fx > .38 && fx < .62 && fy > .16 && fy < .58 && (fy > .28 && fy < .46 || math.Hypot(fx-.5, fy-.28) < .12 || math.Hypot(fx-.5, fy-.46) < .12) {
				c = white
			}
			// archetto e asta
			d := math.Hypot(fx-.5, fy-.46)
			if fy > .46 && d > .17 && d < .23 {
				c = white
			}
			if fx > .47 && fx < .53 && fy > .66 && fy < .78 || fx > .36 && fx < .64 && fy > .76 && fy < .81 {
				c = white
			}
			img.SetNRGBA(x, y, c)
		}
	}
	return img
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
