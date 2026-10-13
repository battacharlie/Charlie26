// Package sfu è il "centralino" WebRTC: riceve l'audio (e in futuro il video)
// di ogni partecipante e lo inoltra agli altri, senza mescolarlo.
//
// Fase 1: un solo canale, tutti sentono tutti. La logica dei canali multipli
// (Fase 2) si innesta in Room.wants, che decide chi riceve il flusso di chi.
package sfu

import (
	"errors"
	"fmt"
	"io"
	"log"
	"sort"
	"sync"
	"time"

	"github.com/pion/ice/v4"
	"github.com/pion/interceptor"
	"github.com/pion/rtcp"
	"github.com/pion/sdp/v3"
	"github.com/pion/webrtc/v4"
)

// Config della parte WebRTC.
type Config struct {
	// UDPPort è l'unica porta UDP usata da tutti i flussi audio/video.
	// Una porta sola semplifica molto il firewall di Windows.
	UDPPort int
}

// Message è un messaggio verso il browser (serializzato in JSON dal server).
type Message struct {
	Type  string     `json:"type"`
	ID    string     `json:"id,omitempty"`
	SDP   string     `json:"sdp,omitempty"`
	Peers []PeerInfo `json:"peers,omitempty"`
	Error string     `json:"error,omitempty"`
}

// PeerInfo descrive un partecipante nella lista mostrata ai client.
type PeerInfo struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Mic  bool   `json:"mic"`
}

// Room è un gruppo di partecipanti che si scambiano flussi.
type Room struct {
	api *webrtc.API
	mux ice.UDPMux

	mu     sync.Mutex
	peers  map[string]*Peer
	tracks map[string]*forward // flussi in arrivo, per id
	nextID int
}

type forward struct {
	owner *Peer
	local *webrtc.TrackLocalStaticRTP
	kind  webrtc.RTPCodecType
	pli   func() // chiede un fotogramma chiave alla sorgente (solo video)
}

// Peer è un partecipante collegato.
type Peer struct {
	ID   string
	Name string

	room    *Room
	pc      *webrtc.PeerConnection
	send    func(Message)
	mic     bool
	senders map[string]*webrtc.RTPSender // id flusso -> sender verso questo peer

	needsOffer bool // serve una nuova negoziazione appena finisce quella in corso
	closed     bool
}

// NewRoom prepara il motore WebRTC.
func NewRoom(cfg Config) (*Room, error) {
	me := &webrtc.MediaEngine{}
	if err := me.RegisterDefaultCodecs(); err != nil {
		return nil, err
	}
	// Livello audio dentro ogni pacchetto: permette ai browser di mostrare
	// "sta parlando" senza decodificare nulla in più.
	if err := me.RegisterHeaderExtension(webrtc.RTPHeaderExtensionCapability{URI: sdp.AudioLevelURI}, webrtc.RTPCodecTypeAudio); err != nil {
		return nil, err
	}
	ir := &interceptor.Registry{}
	if err := webrtc.RegisterDefaultInterceptors(me, ir); err != nil {
		return nil, err
	}

	se := webrtc.SettingEngine{}
	se.SetNetworkTypes([]webrtc.NetworkType{webrtc.NetworkTypeUDP4})
	// Solo candidati "host" in LAN: nessun server STUN/TURN, nessun internet.
	mux, err := ice.NewMultiUDPMuxFromPort(cfg.UDPPort,
		ice.UDPMuxFromPortWithLoopback(), // la console sulla stessa macchina usa 127.0.0.1
		ice.UDPMuxFromPortWithNetworks(ice.NetworkTypeUDP4),
	)
	if err != nil {
		return nil, fmt.Errorf("porta UDP %d non disponibile: %w", cfg.UDPPort, err)
	}
	se.SetICEUDPMux(mux)
	se.SetIncludeLoopbackCandidate(true)

	api := webrtc.NewAPI(webrtc.WithMediaEngine(me), webrtc.WithInterceptorRegistry(ir), webrtc.WithSettingEngine(se))
	return &Room{api: api, mux: mux, peers: map[string]*Peer{}, tracks: map[string]*forward{}}, nil
}

// Close libera la porta UDP.
func (r *Room) Close() error {
	r.mu.Lock()
	peers := make([]*Peer, 0, len(r.peers))
	for _, p := range r.peers {
		peers = append(peers, p)
	}
	r.mu.Unlock()
	for _, p := range peers {
		p.Close()
	}
	return r.mux.Close()
}

// Join aggiunge un partecipante e avvia la prima negoziazione.
// send viene chiamata per ogni messaggio da inoltrare al browser;
// non deve bloccare a lungo.
func (r *Room) Join(name string, send func(Message)) (*Peer, error) {
	pc, err := r.api.NewPeerConnection(webrtc.Configuration{})
	if err != nil {
		return nil, err
	}
	// Il browser ci manda il suo microfono su questa linea audio.
	if _, err := pc.AddTransceiverFromKind(webrtc.RTPCodecTypeAudio,
		webrtc.RTPTransceiverInit{Direction: webrtc.RTPTransceiverDirectionRecvonly}); err != nil {
		pc.Close()
		return nil, err
	}

	r.mu.Lock()
	r.nextID++
	p := &Peer{
		ID:      fmt.Sprintf("p%d", r.nextID),
		Name:    name,
		room:    r,
		pc:      pc,
		send:    send,
		senders: map[string]*webrtc.RTPSender{},
	}
	r.peers[p.ID] = p
	r.mu.Unlock()

	pc.OnTrack(func(remote *webrtc.TrackRemote, _ *webrtc.RTPReceiver) { r.forwardTrack(p, remote) })
	pc.OnConnectionStateChange(func(s webrtc.PeerConnectionState) {
		log.Printf("[%s %s] connessione: %s", p.ID, p.Name, s)
		if s == webrtc.PeerConnectionStateFailed || s == webrtc.PeerConnectionStateClosed {
			go p.Close()
		}
	})

	send(Message{Type: "welcome", ID: p.ID})
	r.mu.Lock()
	r.syncLocked()
	if p.pc.LocalDescription() == nil {
		// Nessun flusso da ricevere ancora: serve comunque una prima offerta
		// perché il browser possa iniziare a inviare il microfono.
		p.negotiateLocked()
	}
	r.broadcastPeersLocked()
	r.mu.Unlock()
	return p, nil
}

// wants dice se il peer "to" deve ricevere il flusso di "from".
// Fase 1: un unico canale, tutti ricevono tutti tranne se stessi.
func (r *Room) wants(to, from *Peer) bool { return to != from }

func (r *Room) forwardTrack(p *Peer, remote *webrtc.TrackRemote) {
	id := p.ID + "-" + remote.ID()
	local, err := webrtc.NewTrackLocalStaticRTP(remote.Codec().RTPCodecCapability, id, p.ID)
	if err != nil {
		log.Printf("[%s] traccia non inoltrabile: %v", p.ID, err)
		return
	}
	f := &forward{owner: p, local: local, kind: remote.Kind()}
	if remote.Kind() == webrtc.RTPCodecTypeVideo {
		ssrc := uint32(remote.SSRC())
		f.pli = func() {
			_ = p.pc.WriteRTCP([]rtcp.Packet{&rtcp.PictureLossIndication{MediaSSRC: ssrc}})
		}
	}
	log.Printf("[%s %s] nuovo flusso %s (%s)", p.ID, p.Name, remote.Kind(), remote.Codec().MimeType)

	r.mu.Lock()
	r.tracks[id] = f
	r.syncLocked()
	r.mu.Unlock()

	stop := make(chan struct{})
	if f.pli != nil {
		// Video: un fotogramma chiave ogni 2 s così chi si collega vede subito l'immagine.
		go func() {
			t := time.NewTicker(2 * time.Second)
			defer t.Stop()
			for {
				select {
				case <-stop:
					return
				case <-t.C:
					f.pli()
				}
			}
		}()
	}

	buf := make([]byte, 1500)
	for {
		n, _, err := remote.Read(buf)
		if err != nil {
			break
		}
		// Write riscrive SSRC e payload type per ogni destinatario.
		if _, err := local.Write(buf[:n]); err != nil && !errors.Is(err, io.ErrClosedPipe) {
			break
		}
	}
	close(stop)

	r.mu.Lock()
	delete(r.tracks, id)
	r.syncLocked()
	r.mu.Unlock()
}

// syncLocked allinea per ogni peer i flussi ricevuti a quelli desiderati
// e, se qualcosa è cambiato, avvia una nuova negoziazione.
func (r *Room) syncLocked() {
	for _, p := range r.peers {
		if p.closed {
			continue
		}
		changed := false
		for id, sender := range p.senders {
			f, ok := r.tracks[id]
			if !ok || !r.wants(p, f.owner) {
				_ = p.pc.RemoveTrack(sender)
				delete(p.senders, id)
				changed = true
			}
		}
		for id, f := range r.tracks {
			if _, ok := p.senders[id]; ok || !r.wants(p, f.owner) {
				continue
			}
			sender, err := p.pc.AddTrack(f.local)
			if err != nil {
				log.Printf("[%s] AddTrack: %v", p.ID, err)
				continue
			}
			p.senders[id] = sender
			changed = true
			go readRTCP(sender, f)
		}
		if changed {
			p.negotiateLocked()
		}
	}
}

// readRTCP consuma i pacchetti di controllo del destinatario e inoltra
// le richieste di fotogramma chiave alla sorgente video.
func readRTCP(sender *webrtc.RTPSender, f *forward) {
	for {
		pkts, _, err := sender.ReadRTCP()
		if err != nil {
			return
		}
		if f.pli == nil {
			continue
		}
		for _, pkt := range pkts {
			switch pkt.(type) {
			case *rtcp.PictureLossIndication, *rtcp.FullIntraRequest:
				f.pli()
			}
		}
	}
}

// negotiateLocked invia un'offerta SDP al browser. Se una negoziazione è
// già in corso, la ripete appena arriva la risposta.
func (p *Peer) negotiateLocked() {
	if p.pc.SignalingState() != webrtc.SignalingStateStable {
		p.needsOffer = true
		return
	}
	p.needsOffer = false
	offer, err := p.pc.CreateOffer(nil)
	if err != nil {
		log.Printf("[%s] CreateOffer: %v", p.ID, err)
		return
	}
	gathered := webrtc.GatheringCompletePromise(p.pc)
	if err := p.pc.SetLocalDescription(offer); err != nil {
		log.Printf("[%s] SetLocalDescription: %v", p.ID, err)
		return
	}
	// Solo candidati locali: la raccolta dura pochi millisecondi.
	select {
	case <-gathered:
	case <-time.After(2 * time.Second):
	}
	p.send(Message{Type: "offer", SDP: p.pc.LocalDescription().SDP})
}

// HandleAnswer applica la risposta SDP del browser.
func (p *Peer) HandleAnswer(sdp string) error {
	r := p.room
	r.mu.Lock()
	defer r.mu.Unlock()
	if p.closed {
		return nil
	}
	if err := p.pc.SetRemoteDescription(webrtc.SessionDescription{Type: webrtc.SDPTypeAnswer, SDP: sdp}); err != nil {
		return err
	}
	if p.needsOffer {
		p.negotiateLocked()
	}
	return nil
}

// AddCandidate aggiunge un candidato ICE inviato dal browser.
func (p *Peer) AddCandidate(c webrtc.ICECandidateInit) error {
	return p.pc.AddICECandidate(c)
}

// SetMic aggiorna lo stato del microfono (per l'indicatore "sta parlando").
func (p *Peer) SetMic(on bool) {
	r := p.room
	r.mu.Lock()
	defer r.mu.Unlock()
	if p.mic != on {
		p.mic = on
		r.broadcastPeersLocked()
	}
}

// Close scollega il partecipante. Si può chiamare più volte.
func (p *Peer) Close() {
	r := p.room
	r.mu.Lock()
	if p.closed {
		r.mu.Unlock()
		return
	}
	p.closed = true
	delete(r.peers, p.ID)
	r.mu.Unlock()

	// Chiudere la connessione fa terminare forwardTrack, che rimuove i flussi.
	_ = p.pc.Close()

	r.mu.Lock()
	r.broadcastPeersLocked()
	r.mu.Unlock()
	log.Printf("[%s %s] uscito", p.ID, p.Name)
}

func (r *Room) broadcastPeersLocked() {
	list := r.peerListLocked()
	for _, p := range r.peers {
		p.send(Message{Type: "peers", Peers: list})
	}
}

func (r *Room) peerListLocked() []PeerInfo {
	list := make([]PeerInfo, 0, len(r.peers))
	for _, p := range r.peers {
		list = append(list, PeerInfo{ID: p.ID, Name: p.Name, Mic: p.mic})
	}
	sort.Slice(list, func(i, j int) bool { return list[i].Name < list[j].Name })
	return list
}

// Peers restituisce la lista attuale dei partecipanti.
func (r *Room) Peers() []PeerInfo {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.peerListLocked()
}
