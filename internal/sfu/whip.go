package sfu

// Ingresso WHIP: OBS (≥ 30) invia il suo video direttamente al programma.
// Il flusso è trattato come un video della regia: arriva nei canali scelti
// dalla console, e in ognuno solo se il caporeparto ha attivato "Video della
// regia".

import (
	"crypto/subtle"
	"errors"
	"fmt"
	"log"
	"time"

	"github.com/pion/rtcp"
	"github.com/pion/webrtc/v4"

	"github.com/battacharlie/charlie26/internal/evento"
)

// ErrWHIPToken: chiave WHIP mancante o sbagliata.
var ErrWHIPToken = errors.New("chiave WHIP non valida")

// WHIPToken restituisce la chiave per OBS, creandola se non esiste (o
// sostituendola se renew è vero).
func (r *Room) WHIPToken(renew bool) string {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.event.WHIPToken == "" || renew {
		r.event.WHIPToken, _ = evento.NewToken()
		r.saveLocked()
	}
	return r.event.WHIPToken
}

// IngestWHIP accetta l'offerta SDP di OBS e restituisce la risposta e l'id
// del flusso (per chiuderlo con DELETE).
func (r *Room) IngestWHIP(token, offer, label string) (answer, id string, err error) {
	r.mu.Lock()
	ok := r.event.WHIPToken != "" && subtle.ConstantTimeCompare([]byte(token), []byte(r.event.WHIPToken)) == 1
	r.mu.Unlock()
	if !ok {
		return "", "", ErrWHIPToken
	}
	pc, err := r.api.NewPeerConnection(webrtc.Configuration{})
	if err != nil {
		return "", "", err
	}
	if label == "" {
		label = "OBS"
	}

	r.mu.Lock()
	r.nextVideo++
	s := &VideoStream{
		ID: fmt.Sprintf("v%d", r.nextVideo), Label: trimLabel(label), ownerName: "OBS",
		fromRegia: true, approved: true, layers: map[string]func(){}, pli: map[string]time.Time{},
		channels: map[string]bool{},
	}
	for _, c := range r.event.Channels {
		s.channels[c.ID] = true // tutti; la regia può restringere dalla console
	}
	s.dests.Store(&[]*vdest{})
	r.videos[s.ID] = s
	r.whip[s.ID] = pc
	r.mu.Unlock()

	pc.OnTrack(func(remote *webrtc.TrackRemote, _ *webrtc.RTPReceiver) {
		if remote.Kind() != webrtc.RTPCodecTypeVideo {
			buf := make([]byte, 1500) // l'audio di OBS non serve all'intercom
			for {
				if _, _, err := remote.Read(buf); err != nil {
					return
				}
			}
		}
		ssrc := uint32(remote.SSRC())
		r.mu.Lock()
		s.mu.Lock()
		s.codec = remote.Codec().RTPCodecCapability
		s.layers[layerHigh] = func() {
			_ = pc.WriteRTCP([]rtcp.Packet{&rtcp.PictureLossIndication{MediaSSRC: ssrc}})
		}
		s.mu.Unlock()
		r.syncLocked()
		r.broadcastVideosLocked()
		r.mu.Unlock()
		log.Printf("WHIP: video %q attivo (%s)", s.Label, remote.Codec().MimeType)
		readVideo(remote, layerHigh, func() *VideoStream { return s })
	})
	pc.OnConnectionStateChange(func(st webrtc.PeerConnectionState) {
		if st == webrtc.PeerConnectionStateFailed || st == webrtc.PeerConnectionStateClosed {
			go r.StopWHIP(s.ID)
		}
	})

	fail := func(err error) (string, string, error) {
		r.StopWHIP(s.ID)
		return "", "", err
	}
	if err := pc.SetRemoteDescription(webrtc.SessionDescription{Type: webrtc.SDPTypeOffer, SDP: offer}); err != nil {
		return fail(fmt.Errorf("offerta SDP non valida: %w", err))
	}
	ans, err := pc.CreateAnswer(nil)
	if err != nil {
		return fail(err)
	}
	gathered := webrtc.GatheringCompletePromise(pc)
	if err := pc.SetLocalDescription(ans); err != nil {
		return fail(err)
	}
	select {
	case <-gathered:
	case <-time.After(2 * time.Second):
	}
	log.Printf("WHIP: collegato %q", s.Label)
	r.mu.Lock()
	r.broadcastVideosLocked()
	r.mu.Unlock()
	return pc.LocalDescription().SDP, s.ID, nil
}

// StopWHIP chiude un ingresso WHIP.
func (r *Room) StopWHIP(id string) bool {
	r.mu.Lock()
	pc := r.whip[id]
	delete(r.whip, id)
	if s := r.videos[id]; s != nil {
		r.stopVideoLocked(s)
	}
	r.mu.Unlock()
	if pc != nil {
		_ = pc.Close()
		return true
	}
	return false
}

// SetVideoChannels: la regia cambia i canali di destinazione di un video.
func (p *Peer) SetVideoChannels(id string, channels []string) error {
	r := p.room
	r.mu.Lock()
	defer r.mu.Unlock()
	s := r.videos[id]
	if s == nil || p.Role != RoleRegia {
		return ErrNotAllowed
	}
	chs := map[string]bool{}
	for _, c := range channels {
		if r.event.Channel(c) != nil {
			chs[c] = true
		}
	}
	if len(chs) == 0 {
		return errors.New("scegli almeno un canale")
	}
	s.channels = chs
	r.syncLocked()
	r.broadcastVideosLocked()
	return nil
}
