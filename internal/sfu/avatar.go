package sfu

import (
	"errors"

	"github.com/battacharlie/charlie26/internal/immagini"
)

// Avatar predefiniti: i quattro mostriciattoli in web/avatars.
var presetAvatars = map[string]bool{"m1": true, "m2": true, "m3": true, "m4": true}

const (
	maxAvatarBytes = 48 << 10 // la foto viene già rimpicciolita dal telefono
	maxAvatarSide  = 512
)

// parseAvatar controlla una foto inviata come data URL (solo JPEG e PNG
// piccoli e davvero decodificabili).
func parseAvatar(dataURL string) ([]byte, string, error) {
	raw, ext, err := immagini.Parse(dataURL, maxAvatarBytes, maxAvatarSide)
	if err != nil {
		return nil, "", err
	}
	return raw, map[string]string{"jpg": "image/jpeg", "png": "image/png"}[ext], nil
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
