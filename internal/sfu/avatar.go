package sfu

import (
	"bytes"
	"encoding/base64"
	"errors"
	"image"
	_ "image/jpeg" // formati accettati per le foto profilo
	_ "image/png"
	"strings"
)

// Avatar predefiniti: i quattro mostriciattoli in web/avatars.
var presetAvatars = map[string]bool{"m1": true, "m2": true, "m3": true, "m4": true}

const (
	maxAvatarBytes = 48 << 10 // la foto viene già rimpicciolita dal telefono
	maxAvatarSide  = 512
)

// parseAvatar controlla una foto inviata come data URL e restituisce i byte
// e il tipo MIME. Accetta solo JPEG e PNG piccoli e davvero decodificabili.
func parseAvatar(dataURL string) ([]byte, string, error) {
	var mime string
	switch {
	case strings.HasPrefix(dataURL, "data:image/jpeg;base64,"):
		mime = "image/jpeg"
	case strings.HasPrefix(dataURL, "data:image/png;base64,"):
		mime = "image/png"
	default:
		return nil, "", errors.New("formato immagine non supportato (servono JPEG o PNG)")
	}
	raw, err := base64.StdEncoding.DecodeString(dataURL[strings.Index(dataURL, ",")+1:])
	if err != nil {
		return nil, "", errors.New("immagine non leggibile")
	}
	if len(raw) > maxAvatarBytes {
		return nil, "", errors.New("immagine troppo grande")
	}
	cfg, format, err := image.DecodeConfig(bytes.NewReader(raw))
	if err != nil || "image/"+format != mime {
		return nil, "", errors.New("il file non è un'immagine valida")
	}
	if cfg.Width > maxAvatarSide || cfg.Height > maxAvatarSide || cfg.Width == 0 || cfg.Height == 0 {
		return nil, "", errors.New("immagine troppo grande")
	}
	return raw, mime, nil
}

// SetAvatar imposta l'avatar: un mostriciattolo predefinito ("m1"…"m4")
// oppure una foto (data URL).
func (p *Peer) SetAvatar(preset, dataURL string) error {
	var img []byte
	var mime string
	if dataURL != "" {
		var err error
		if img, mime, err = parseAvatar(dataURL); err != nil {
			return err
		}
		preset = "foto"
	} else if !presetAvatars[preset] {
		return errors.New("avatar sconosciuto")
	}
	r := p.room
	r.mu.Lock()
	defer r.mu.Unlock()
	p.avatar, p.avatarImg, p.avatarMime = preset, img, mime
	p.avatarVer++
	r.broadcastPeersLocked()
	return nil
}

// AvatarImage restituisce la foto profilo di un partecipante collegato.
func (r *Room) AvatarImage(peerID string) ([]byte, string, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	p := r.peers[peerID]
	if p == nil || p.avatarImg == nil {
		return nil, "", false
	}
	return p.avatarImg, p.avatarMime, true
}
