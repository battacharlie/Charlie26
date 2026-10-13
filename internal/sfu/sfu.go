// Package sfu è il "centralino" WebRTC: riceve l'audio (e in futuro il video)
// di ogni partecipante e lo inoltra agli altri, senza mescolarlo.
//
// Logica intercom (vedi routing.go):
//   - ogni partecipante è membro di uno o più canali;
//   - preme "parla" su uno o più dei suoi canali;
//   - sente chi sta parlando su un canale che lui ascolta.
//
// Per non perdere le prime sillabe, i flussi tra persone che hanno almeno un
// canale in comune sono negoziati in anticipo; premere "parla" apre solo un
// "rubinetto" interno al server, istantaneo.
package sfu

import (
	"errors"
	"fmt"
	"log"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"github.com/pion/ice/v4"
	"github.com/pion/interceptor"
	"github.com/pion/rtcp"
	"github.com/pion/rtp"
	"github.com/pion/sdp/v3"
	"github.com/pion/webrtc/v4"
)

// Config della parte WebRTC.
type Config struct {
	// UDPPort è l'unica porta UDP usata da tutti i flussi audio/video.
	// Una porta sola semplifica molto il firewall di Windows.
	UDPPort int
	// Channels: il primo con Main=true è il canale "Produzione".
	Channels []Channel
}

// Channel è un canale intercom.
type Channel struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Main bool   `json:"main,omitempty"`
}

// Ruoli (Fase 2: dichiarati da chi entra; login e permessi nella Fase 3).
const (
	RoleRegia       = "regia"       // SupAdmin: tutti i canali
	RoleCaporeparto = "caporeparto" // Admin: Produzione + il proprio reparto
	RoleTecnico     = "tecnico"     // User: il canale scelto
)

// Message è un messaggio verso il browser (serializzato in JSON dal server).
type Message struct {
	Type     string     `json:"type"`
	ID       string     `json:"id,omitempty"`
	SDP      string     `json:"sdp,omitempty"`
	Peers    []PeerInfo `json:"peers,omitempty"`
	Channels []Channel  `json:"channels,omitempty"`
	Me       *PeerInfo  `json:"me,omitempty"`
	Error    string     `json:"error,omitempty"`
}

// PeerInfo descrive un partecipante nella lista mostrata ai client.
type PeerInfo struct {
	ID       string   `json:"id"`
	Name     string   `json:"name"`
	Role     string   `json:"role"`
	Channels []string `json:"channels"`        // canali di cui è membro
	Talk     []string `json:"talk"`            // canali su cui sta parlando ora
	Muted    []string `json:"muted,omitempty"` // canali che ha smesso di ascoltare
}

// JoinOptions sono i dati dichiarati da chi entra.
type JoinOptions struct {
	Name    string
	Role    string
	Channel string // reparto scelto (ignorato per la regia)
}

// Room è l'insieme di canali e partecipanti.
type Room struct {
	api *webrtc.API
	mux ice.UDPMux

	mu       sync.Mutex
	channels []Channel
	peers    map[string]*Peer
	tracks   map[string]*forward // flussi in arrivo, per id
	nextID   int
}

// forward è un flusso in arrivo da un partecipante, con una copia
// (dest) per ogni possibile ascoltatore.
type forward struct {
	id    string
	owner *Peer
	kind  webrtc.RTPCodecType
	codec webrtc.RTPCodecCapability
	pli   func() // chiede un fotogramma chiave alla sorgente (solo video)

	dests atomic.Pointer[[]*dest] // letto a ogni pacchetto senza lock
}

// dest è la copia di un flusso verso un singolo ascoltatore.
type dest struct {
	to     *Peer
	local  *webrtc.TrackLocalStaticRTP
	sender *webrtc.RTPSender
	open   atomic.Bool // rubinetto: true se in questo momento deve sentire
	seq    uint16      // numerazione continua dei pacchetti inoltrati (solo goroutine di lettura)
}

// Peer è un partecipante collegato.
type Peer struct {
	ID   string
	Name string
	Role string

	room   *Room
	pc     *webrtc.PeerConnection
	send   func(Message)
	member map[string]bool // canali di cui è membro
	talk   map[string]bool // canali su cui sta parlando
	muted  map[string]bool // canali che non vuole ascoltare

	senders    map[string]*dest // id flusso -> copia verso questo peer
	needsOffer bool             // serve una nuova negoziazione appena finisce quella in corso
	closed     bool
}

// NewRoom prepara il motore WebRTC.
func NewRoom(cfg Config) (*Room, error) {
	if len(cfg.Channels) == 0 {
		cfg.Channels = DefaultChannels()
	}
	if err := validateChannels(cfg.Channels); err != nil {
		return nil, err
	}

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
	return &Room{
		api:      api,
		mux:      mux,
		channels: append([]Channel(nil), cfg.Channels...),
		peers:    map[string]*Peer{},
		tracks:   map[string]*forward{},
	}, nil
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

// Channels restituisce l'elenco dei canali.
func (r *Room) Channels() []Channel {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]Channel(nil), r.channels...)
}

// Join aggiunge un partecipante e avvia la prima negoziazione.
// send viene chiamata per ogni messaggio da inoltrare al browser;
// non deve bloccare a lungo.
func (r *Room) Join(opts JoinOptions, send func(Message)) (*Peer, error) {
	r.mu.Lock()
	member, err := r.membershipLocked(opts.Role, opts.Channel)
	r.mu.Unlock()
	if err != nil {
		return nil, err
	}

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
		Name:    opts.Name,
		Role:    opts.Role,
		room:    r,
		pc:      pc,
		send:    send,
		member:  member,
		talk:    map[string]bool{},
		muted:   map[string]bool{},
		senders: map[string]*dest{},
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

	r.mu.Lock()
	info := p.infoLocked()
	send(Message{Type: "welcome", ID: p.ID, Channels: r.channels, Me: &info})
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

func (r *Room) forwardTrack(p *Peer, remote *webrtc.TrackRemote) {
	f := &forward{
		id:    p.ID + "-" + remote.ID(),
		owner: p,
		kind:  remote.Kind(),
		codec: remote.Codec().RTPCodecCapability,
	}
	f.dests.Store(&[]*dest{})
	if remote.Kind() == webrtc.RTPCodecTypeVideo {
		ssrc := uint32(remote.SSRC())
		f.pli = func() {
			_ = p.pc.WriteRTCP([]rtcp.Packet{&rtcp.PictureLossIndication{MediaSSRC: ssrc}})
		}
	}
	log.Printf("[%s %s] nuovo flusso %s (%s)", p.ID, p.Name, remote.Kind(), remote.Codec().MimeType)

	r.mu.Lock()
	r.tracks[f.id] = f
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
	pkt := &rtp.Packet{}
	for {
		n, _, err := remote.Read(buf)
		if err != nil {
			break
		}
		if err := pkt.Unmarshal(buf[:n]); err != nil {
			continue
		}
		for _, d := range *f.dests.Load() {
			if !d.open.Load() {
				continue
			}
			// Numerazione continua per ogni ascoltatore: i pacchetti saltati
			// mentre il rubinetto era chiuso non sembrano "persi" al browser.
			d.seq++
			out := *pkt
			out.Header.SequenceNumber = d.seq
			_ = d.local.WriteRTP(&out)
		}
	}
	close(stop)

	r.mu.Lock()
	delete(r.tracks, f.id)
	r.syncLocked()
	r.mu.Unlock()
}

// syncLocked allinea per ogni peer i flussi ricevuti a quelli desiderati,
// aggiorna i rubinetti e, se qualcosa è cambiato, rinegozia.
func (r *Room) syncLocked() {
	for _, f := range r.tracks {
		changed := false
		list := *f.dests.Load()
		next := make([]*dest, 0, len(list))
		for _, d := range list {
			if d.to.closed || !subscribed(d.to, f.owner) {
				if !d.to.closed {
					_ = d.to.pc.RemoveTrack(d.sender)
					delete(d.to.senders, f.id)
					d.to.needsOffer = true
				}
				changed = true
				continue
			}
			next = append(next, d)
		}
		for _, p := range r.peers {
			if p.closed || p == f.owner || !subscribed(p, f.owner) {
				continue
			}
			if _, ok := p.senders[f.id]; ok {
				continue
			}
			local, err := webrtc.NewTrackLocalStaticRTP(f.codec, f.id, f.owner.ID)
			if err != nil {
				continue
			}
			sender, err := p.pc.AddTrack(local)
			if err != nil {
				log.Printf("[%s] AddTrack: %v", p.ID, err)
				continue
			}
			d := &dest{to: p, local: local, sender: sender}
			p.senders[f.id] = d
			p.needsOffer = true
			next = append(next, d)
			changed = true
			go readRTCP(sender, f)
		}
		if changed {
			f.dests.Store(&next)
		}
	}
	r.updateGatesLocked()
	for _, p := range r.peers {
		if p.needsOffer && !p.closed {
			p.negotiateLocked()
		}
	}
}

// updateGatesLocked apre o chiude ogni rubinetto in base a chi parla e chi ascolta.
func (r *Room) updateGatesLocked() {
	for _, f := range r.tracks {
		for _, d := range *f.dests.Load() {
			d.open.Store(audible(f.owner, d.to))
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

// SetTalk imposta i canali su cui il partecipante sta parlando.
// I canali di cui non è membro vengono ignorati.
func (p *Peer) SetTalk(channels []string) {
	r := p.room
	r.mu.Lock()
	defer r.mu.Unlock()
	next := map[string]bool{}
	for _, c := range channels {
		if p.member[c] {
			next[c] = true
		}
	}
	if sameSet(next, p.talk) {
		return
	}
	p.talk = next
	r.updateGatesLocked()
	r.broadcastPeersLocked()
}

// SetListen attiva o disattiva l'ascolto di un canale di cui è membro.
func (p *Peer) SetListen(channel string, on bool) {
	r := p.room
	r.mu.Lock()
	defer r.mu.Unlock()
	if !p.member[channel] || p.muted[channel] == !on {
		return
	}
	if on {
		delete(p.muted, channel)
	} else {
		p.muted[channel] = true
	}
	r.updateGatesLocked()
	r.broadcastPeersLocked()
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
	r.syncLocked() // toglie le sue copie dai flussi degli altri
	r.mu.Unlock()

	// Chiudere la connessione fa terminare forwardTrack, che rimuove i suoi flussi.
	_ = p.pc.Close()

	r.mu.Lock()
	r.broadcastPeersLocked()
	r.mu.Unlock()
	log.Printf("[%s %s] uscito", p.ID, p.Name)
}

func (p *Peer) infoLocked() PeerInfo {
	return PeerInfo{
		ID:       p.ID,
		Name:     p.Name,
		Role:     p.Role,
		Channels: p.room.orderedLocked(p.member),
		Talk:     p.room.orderedLocked(p.talk),
		Muted:    p.room.orderedLocked(p.muted),
	}
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
		list = append(list, p.infoLocked())
	}
	sort.Slice(list, func(i, j int) bool {
		if list[i].Name != list[j].Name {
			return list[i].Name < list[j].Name
		}
		return list[i].ID < list[j].ID
	})
	return list
}

// Peers restituisce la lista attuale dei partecipanti.
func (r *Room) Peers() []PeerInfo {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.peerListLocked()
}

var errUnknownChannel = errors.New("canale inesistente")
