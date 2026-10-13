package server

import (
	"encoding/base64"
	"errors"

	"github.com/battacharlie/charlie26/internal/evento"
	"github.com/battacharlie/charlie26/internal/immagini"
)

// lookChange è una modifica dell'aspetto: dell'evento (ID vuoto) o di un canale.
type lookChange struct {
	ID         string `json:"id"`
	Icon       string `json:"icon"`     // "m1"…"m4", "" = nessuna, "keep" = invariata
	IconData   string `json:"iconData"` // nuova icona caricata (data URL)
	Color      string `json:"color"`
	ImageData  string `json:"imageData"` // nuova immagine di sfondo (data URL)
	ClearImage bool   `json:"clearImage"`
}

// applyLookChange salva le immagini nuove e aggiorna l'evento.
func (s *Server) applyLookChange(c lookChange) error {
	var bg, icon string
	var err error
	if c.ImageData != "" {
		if bg, err = s.cfg.Images.Save(c.ImageData, immagini.MaxBackgroundBytes, immagini.MaxBackgroundSide); err != nil {
			return err
		}
	}
	if c.IconData != "" {
		if icon, err = s.cfg.Images.Save(c.IconData, immagini.MaxIconBytes, immagini.MaxIconSide); err != nil {
			return err
		}
	}
	return s.cfg.Room.UpdateEvent(func(e *evento.Event) error {
		look := &e.Look
		var ch *evento.Channel
		if c.ID != "" {
			if ch = e.Channel(c.ID); ch == nil {
				return errors.New("canale inesistente")
			}
			look = &ch.Look
			switch {
			case icon != "":
				ch.Icon = icon
			case c.Icon != "keep":
				ch.Icon = c.Icon
			}
		}
		look.Color = c.Color
		switch {
		case bg != "":
			look.Image = bg
		case c.ClearImage:
			look.Image = ""
		}
		return nil
	})
}

// embedImages aggiunge al file esportato le immagini usate dall'evento.
func (s *Server) embedImages(f *evento.ExportFile) {
	for _, name := range f.Evento.Images() {
		b, mime, err := s.cfg.Images.Read(name)
		if err != nil {
			continue
		}
		if f.Immagini == nil {
			f.Immagini = map[string]string{}
		}
		f.Immagini[name] = "data:" + mime + ";base64," + base64.StdEncoding.EncodeToString(b)
	}
}

// storeImages salva le immagini di un file importato (controllandole come
// quelle caricate a mano) e toglie dall'evento i riferimenti a immagini mancanti.
func (s *Server) storeImages(f *evento.ExportFile, e *evento.Event) {
	for _, data := range f.Immagini {
		_, _ = s.cfg.Images.Save(data, immagini.MaxBackgroundBytes, immagini.MaxBackgroundSide)
	}
	fix := func(n *string) {
		if *n != "" && !(*n == "m1" || *n == "m2" || *n == "m3" || *n == "m4") && !s.cfg.Images.Exists(*n) {
			*n = ""
		}
	}
	fix(&e.Look.Image)
	for i := range e.Channels {
		fix(&e.Channels[i].Look.Image)
		fix(&e.Channels[i].Icon)
	}
}
