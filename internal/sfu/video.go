package sfu

// Video: più flussi contemporanei, ognuno destinato a uno o più canali.
//
// Chi invia dal browser manda due "corsie" dello stesso video: alta (h) e
// bassa (l, massimo 15 fps). Ogni spettatore sceglie la qualità; il server
// inoltra la corsia giusta e passa da una all'altra solo su un fotogramma
// completo (keyframe), così l'immagine non si rompe.
//
// Permessi:
//   - regia: invia ovunque, subito;
//   - caporeparto: subito nei canali di cui è responsabile;
//   - altrimenti serve il consenso di un responsabile di quel canale (o della
//     regia), che nel frattempo vede l'anteprima per decidere.

import (
	"errors"
	"fmt"
	"log"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"github.com/pion/rtcp"
	"github.com/pion/rtp"
	"github.com/pion/rtp/codecs"
	"github.com/pion/webrtc/v4"
)

const (
	layerHigh     = "h"
	layerLow      = "l"
	qualityOff    = "off"
	maxVideosEach = 4 // flussi contemporanei per partecipante
)

// VideoInfo è la parte pubblica di un flusso video.
type VideoInfo struct {
	ID        string   `json:"id"`
	Label     string   `json:"label"`
	Owner     string   `json:"owner,omitempty"` // id del partecipante (vuoto = ingresso WHIP)
	OwnerName string   `json:"ownerName"`
	Channels  []string `json:"channels"`
	Approved  bool     `json:"approved"`
	FromRegia bool     `json:"fromRegia,omitempty"`
	Layers    []string `json:"layers"`
}

// VideoSlot dice al browser che invia quale linea video usare per quale flusso.
type VideoSlot struct {
	Mid    string `json:"mid"`
	Layer  string `json:"layer"`
	Stream string `json:"stream"` // vuoto = linea libera (non inviare nulla)
	Ref    string `json:"ref,omitempty"`
}

// VideoStream è un flusso video in arrivo.
type VideoStream struct {
	ID        string
	Label     string
	ref       string // riferimento scelto dal browser che invia
	owner     *Peer  // nil per l'ingresso WHIP
	ownerName string
	fromRegia bool
	channels  map[string]bool
	approved  bool
	ended     atomic.Bool
	// codec e layers si scrivono con room.mu e mu entrambi bloccati,
	// quindi si leggono con uno solo dei due.
	codec  webrtc.RTPCodecCapability
	layers map[string]func() // corsia -> richiesta di keyframe alla sorgente

	dests atomic.Pointer[[]*vdest]
	mu    sync.Mutex // stato di inoltro delle copie (usato dalle goroutine di lettura)
	pli   map[string]time.Time
}

// vdest è la copia di un flusso verso uno spettatore.
type vdest struct {
	to     *Peer
	local  *webrtc.TrackLocalStaticRTP
	sender *webrtc.RTPSender
	want   atomic.Value // corsia desiderata: "h", "l" o "off"

	// protetti da VideoStream.mu
	cur     string // corsia che sta ricevendo ora ("" = in attesa di keyframe)
	started bool
	seq     uint16
	tsOff   uint32
	lastTS  uint32
}

// vpair sono le due linee (alta e bassa) con cui un browser invia un video.
// Restano allocate e vengono riusate per il flusso successivo.
type vpair struct {
	tr     map[string]*webrtc.RTPTransceiver // corsia -> linea
	stream atomic.Pointer[VideoStream]
	codec  webrtc.RTPCodecCapability
}

// ---------- Avvio, consenso, stop ----------

// StartVideo: il partecipante vuole inviare un video nei canali indicati.
// ref è un riferimento scelto dal browser per riconoscere la risposta.
func (p *Peer) StartVideo(ref, label string, channels []string) (*VideoInfo, error) {
	r := p.room
	r.mu.Lock()
	defer r.mu.Unlock()
	if p.closed {
		return nil, ErrNotAllowed
	}
	chs := map[string]bool{}
	for _, c := range channels {
		if p.member[c] {
			chs[c] = true
		}
	}
	if len(chs) == 0 {
		return nil, errors.New("scegli almeno un canale di cui fai parte")
	}
	n := 0
	for _, s := range r.videos {
		if s.owner == p {
			n++
		}
	}
	if n >= maxVideosEach {
		return nil, fmt.Errorf("puoi inviare al massimo %d video insieme", maxVideosEach)
	}
	approved := true
	for c := range chs {
		if !p.admin[c] {
			approved = false // serve il consenso di un responsabile
		}
	}
	if label == "" {
		label = "Video di " + p.Name
	}
	r.nextVideo++
	s := &VideoStream{
		ID: fmt.Sprintf("v%d", r.nextVideo), Label: trimLabel(label), ref: ref,
		owner: p, ownerName: p.Name, fromRegia: p.Role == RoleRegia,
		channels: chs, approved: approved, layers: map[string]func(){}, pli: map[string]time.Time{},
	}
	s.dests.Store(&[]*vdest{})

	pair := p.freePairLocked()
	if pair == nil {
		var err error
		if pair, err = p.newPairLocked(); err != nil {
			return nil, err
		}
	}
	pair.stream.Store(s)
	if pair.codec.MimeType != "" {
		s.setSource(pair, p)
	}
	r.videos[s.ID] = s
	if approved {
		log.Printf("[%s] video %q in %v", p.Label, s.Label, r.orderedLocked(chs))
	} else {
		log.Printf("[%s] chiede di inviare il video %q in %v: in attesa di consenso", p.Label, s.Label, r.orderedLocked(chs))
	}
	p.needsOffer = true
	r.syncLocked()
	r.broadcastVideosLocked()
	info := s.infoLocked(r)
	return &info, nil
}

// ApproveVideo: un responsabile del canale (o la regia) accetta o rifiuta.
func (p *Peer) ApproveVideo(id string, ok bool) error {
	r := p.room
	r.mu.Lock()
	defer r.mu.Unlock()
	s := r.videos[id]
	if s == nil || s.approved || !canManageVideo(p, s) {
		return ErrNotAllowed
	}
	if !ok {
		if s.owner != nil {
			s.owner.send(Message{Type: "videoDenied", Error: p.Name + " non ha approvato il video."})
		}
		r.stopVideoLocked(s)
		return nil
	}
	s.approved = true
	log.Printf("[%s] approva il video %q", p.Label, s.Label)
	r.syncLocked()
	r.broadcastVideosLocked()
	return nil
}

// StopVideo: ferma un video (chi lo invia, un responsabile del canale o la regia).
func (p *Peer) StopVideo(id string) error {
	r := p.room
	r.mu.Lock()
	defer r.mu.Unlock()
	s := r.videos[id]
	if s == nil || (s.owner != p && !canManageVideo(p, s)) {
		return ErrNotAllowed
	}
	if s.owner != nil && s.owner != p {
		s.owner.send(Message{Type: "videoDenied", Error: p.Name + " ha fermato il tuo video."})
	}
	r.stopVideoLocked(s)
	return nil
}

// SetVideoQuality: qualità predefinita dei video ricevuti ("h", "l", "off").
// Con stream non vuoto vale solo per quel video.
func (p *Peer) SetVideoQuality(stream, q string) {
	if q != layerHigh && q != layerLow && q != qualityOff {
		return
	}
	r := p.room
	r.mu.Lock()
	defer r.mu.Unlock()
	if stream == "" {
		p.videoQ = q
	} else {
		p.videoWant[stream] = q
	}
	for _, s := range r.videos {
		for _, d := range *s.dests.Load() {
			if d.to == p {
				d.want.Store(p.wantFor(s.ID))
			}
		}
	}
}

func (p *Peer) wantFor(stream string) string {
	if q, ok := p.videoWant[stream]; ok {
		return q
	}
	return p.videoQ
}

// canManageVideo: p può approvare o fermare il video (regia o responsabile
// di almeno un canale di destinazione). Il video della regia lo ferma solo la
// regia: il caporeparto decide solo se arriva nel suo canale.
func canManageVideo(p *Peer, s *VideoStream) bool {
	if p.Role == RoleRegia {
		return true
	}
	if s.fromRegia {
		return false
	}
	for c := range s.channels {
		if p.admin[c] {
			return true
		}
	}
	return false
}

func (r *Room) stopVideoLocked(s *VideoStream) {
	if s.ended.Load() {
		return
	}
	r.dropVideoLocked(s)
	if s.owner != nil {
		for _, pair := range s.owner.vpairs {
			if pair.stream.Load() == s {
				pair.stream.Store(nil)
				s.owner.needsOffer = true
			}
		}
	}
	log.Printf("video %q fermato", s.Label)
	r.syncLocked()
	r.broadcastVideosLocked()
}

// dropVideoLocked chiude un flusso e toglie le sue copie agli spettatori.
func (r *Room) dropVideoLocked(s *VideoStream) {
	s.ended.Store(true)
	delete(r.videos, s.ID)
	for _, d := range *s.dests.Load() {
		if !d.to.closed {
			_ = d.to.pc.RemoveTrack(d.sender)
			d.to.needsOffer = true
		}
	}
	s.dests.Store(&[]*vdest{})
}

// ---------- Linee di invio del browser ----------

func (p *Peer) freePairLocked() *vpair {
	for _, pair := range p.vpairs {
		if pair.stream.Load() == nil {
			return pair
		}
	}
	return nil
}

func (p *Peer) newPairLocked() (*vpair, error) {
	pair := &vpair{tr: map[string]*webrtc.RTPTransceiver{}}
	for _, layer := range []string{layerHigh, layerLow} {
		tr, err := p.pc.AddTransceiverFromKind(webrtc.RTPCodecTypeVideo,
			webrtc.RTPTransceiverInit{Direction: webrtc.RTPTransceiverDirectionRecvonly})
		if err != nil {
			return nil, err
		}
		pair.tr[layer] = tr
	}
	p.vpairs = append(p.vpairs, pair)
	return pair, nil
}

// videoSlotsLocked: corrispondenza linea -> flusso da mandare al browser
// insieme all'offerta SDP.
func (p *Peer) videoSlotsLocked() []VideoSlot {
	var out []VideoSlot
	for _, pair := range p.vpairs {
		s := pair.stream.Load()
		for _, layer := range []string{layerHigh, layerLow} {
			slot := VideoSlot{Mid: pair.tr[layer].Mid(), Layer: layer}
			if s != nil {
				slot.Stream, slot.Ref = s.ID, s.ref
			}
			out = append(out, slot)
		}
	}
	return out
}

// setSource registra codec e corsie della linea che invia il flusso
// (con room.mu bloccato).
func (s *VideoStream) setSource(pair *vpair, p *Peer) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.codec = pair.codec
	for l, tr := range pair.tr {
		s.layers[l] = pliFunc(p, tr)
	}
}

func pliFunc(p *Peer, tr *webrtc.RTPTransceiver) func() {
	return func() {
		if rcv := tr.Receiver(); rcv != nil && rcv.Track() != nil {
			_ = p.pc.WriteRTCP([]rtcp.Packet{&rtcp.PictureLossIndication{MediaSSRC: uint32(rcv.Track().SSRC())}})
		}
	}
}

// forwardVideoLayer legge una linea video del browser per tutta la durata
// del collegamento e inoltra i pacchetti al flusso assegnato in quel momento.
func (r *Room) forwardVideoLayer(p *Peer, remote *webrtc.TrackRemote, recv *webrtc.RTPReceiver) {
	var pair *vpair
	var layer string
	r.mu.Lock()
	for _, pr := range p.vpairs {
		for l, tr := range pr.tr {
			if tr.Receiver() == recv {
				pair, layer = pr, l
			}
		}
	}
	if pair != nil {
		pair.codec = remote.Codec().RTPCodecCapability
		if s := pair.stream.Load(); s != nil && s.codec.MimeType == "" {
			s.setSource(pair, p)
			r.syncLocked()
		}
	}
	r.mu.Unlock()
	if pair == nil {
		log.Printf("[%s] video inatteso, ignorato", p.Label)
		return
	}
	log.Printf("[%s] linea video %s attiva (%s)", p.Label, layer, remote.Codec().MimeType)
	readVideo(remote, layer, func() *VideoStream { return pair.stream.Load() })
}

// readVideo legge i pacchetti di una corsia e li consegna al flusso corrente.
func readVideo(remote *webrtc.TrackRemote, layer string, current func() *VideoStream) {
	buf := make([]byte, 1500)
	pkt := &rtp.Packet{}
	for {
		n, _, err := remote.Read(buf)
		if err != nil {
			return
		}
		s := current()
		if s == nil || s.ended.Load() {
			continue
		}
		if err := pkt.Unmarshal(buf[:n]); err != nil {
			continue
		}
		s.writeLayer(layer, pkt)
	}
}

// ---------- Inoltro con scelta della corsia ----------

func (s *VideoStream) writeLayer(layer string, pkt *rtp.Packet) {
	dests := *s.dests.Load()
	if len(dests) == 0 {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, d := range dests {
		want, _ := d.want.Load().(string)
		if want == qualityOff {
			d.cur = ""
			continue
		}
		if _, ok := s.layers[want]; !ok {
			want = layerHigh // es. ingresso WHIP: c'è solo la corsia alta
		}
		if d.cur != layer {
			if want != layer {
				continue // continua a ricevere l'altra corsia finché questa non è pronta
			}
			if !isKeyframeStart(s.codec.MimeType, pkt.Payload) {
				s.requestKeyframeLocked(layer)
				continue
			}
			if d.started {
				// Tempi continui: il browser non deve accorgersi del cambio di corsia.
				d.tsOff = d.lastTS + 3000 - pkt.Timestamp
			}
			d.cur, d.started = layer, true
		}
		d.seq++
		out := *pkt
		out.Header.SequenceNumber = d.seq
		out.Header.Timestamp = pkt.Timestamp + d.tsOff
		d.lastTS = out.Header.Timestamp
		_ = d.local.WriteRTP(&out)
	}
}

// requestKeyframeLocked chiede un fotogramma completo alla sorgente (al
// massimo due volte al secondo per corsia).
func (s *VideoStream) requestKeyframeLocked(layer string) {
	if time.Since(s.pli[layer]) < 500*time.Millisecond {
		return
	}
	s.pli[layer] = time.Now()
	if f := s.layers[layer]; f != nil {
		go f()
	}
}

// isKeyframeStart: il pacchetto apre un fotogramma completo?
func isKeyframeStart(mime string, payload []byte) bool {
	switch mime {
	case webrtc.MimeTypeH264:
		return h264Keyframe(payload)
	case webrtc.MimeTypeVP8:
		var vp8 codecs.VP8Packet
		data, err := vp8.Unmarshal(payload)
		return err == nil && vp8.S == 1 && vp8.PID == 0 && len(data) > 0 && data[0]&0x01 == 0
	}
	return true // codec sconosciuto: inoltra comunque
}

func h264Keyframe(p []byte) bool {
	if len(p) < 2 {
		return false
	}
	switch nal := p[0] & 0x1F; nal {
	case 5, 7: // IDR o SPS
		return true
	case 24: // STAP-A: più unità in un pacchetto
		for off := 1; off+2 < len(p); {
			size := int(p[off])<<8 | int(p[off+1])
			if off+2 >= len(p) {
				break
			}
			if t := p[off+2] & 0x1F; t == 5 || t == 7 {
				return true
			}
			off += 2 + size
		}
	case 28: // FU-A: inizio di un IDR spezzato
		return p[1]&0x80 != 0 && p[1]&0x1F == 5
	}
	return false
}

// ---------- Chi vede cosa ----------

// seesVideo: il partecipante deve ricevere questo flusso?
func (r *Room) seesVideo(p *Peer, s *VideoStream) bool {
	if p == s.owner || p.closed || s.ended.Load() {
		return false
	}
	if p.Role == RoleRegia {
		return true // la regia vede tutto (anteprime e richieste)
	}
	if !s.approved {
		return canManageVideo(p, s) // chi deve decidere vede l'anteprima
	}
	for c := range s.channels {
		if !p.member[c] {
			continue
		}
		if s.fromRegia {
			// Il caporeparto decide se il video della regia arriva nel suo canale.
			if ch := r.event.Channel(c); ch == nil || !ch.Video {
				continue
			}
		}
		return true
	}
	return false
}

// syncVideoLocked allinea le copie video di ogni flusso agli spettatori.
func (r *Room) syncVideoLocked() {
	for _, s := range r.videos {
		list := *s.dests.Load()
		next := make([]*vdest, 0, len(list))
		changed := false
		have := map[*Peer]bool{}
		for _, d := range list {
			if d.to.closed || !r.seesVideo(d.to, s) {
				if !d.to.closed {
					_ = d.to.pc.RemoveTrack(d.sender)
					d.to.needsOffer = true
				}
				changed = true
				continue
			}
			have[d.to] = true
			next = append(next, d)
		}
		if s.codec.MimeType != "" {
			for _, p := range r.peers {
				if have[p] || !r.seesVideo(p, s) {
					continue
				}
				local, err := webrtc.NewTrackLocalStaticRTP(s.codec, "video-"+s.ID, "v:"+s.ID)
				if err != nil {
					continue
				}
				// Linea dedicata in sola uscita: AddTrack potrebbe riusare una
				// linea con cui questo browser sta inviando il suo video.
				tr, err := p.pc.AddTransceiverFromTrack(local, webrtc.RTPTransceiverInit{Direction: webrtc.RTPTransceiverDirectionSendonly})
				if err != nil {
					log.Printf("[%s] linea video: %v", p.Label, err)
					continue
				}
				sender := tr.Sender()
				d := &vdest{to: p, local: local, sender: sender}
				d.want.Store(p.wantFor(s.ID))
				next = append(next, d)
				p.needsOffer = true
				changed = true
				go readVideoRTCP(sender, s, d)
			}
		}
		if changed {
			s.dests.Store(&next)
		}
	}
}

// readVideoRTCP: se lo spettatore perde l'immagine chiede un keyframe.
func readVideoRTCP(sender *webrtc.RTPSender, s *VideoStream, d *vdest) {
	for {
		pkts, _, err := sender.ReadRTCP()
		if err != nil {
			return
		}
		for _, pkt := range pkts {
			switch pkt.(type) {
			case *rtcp.PictureLossIndication, *rtcp.FullIntraRequest:
				s.mu.Lock()
				layer := d.cur
				if layer == "" {
					layer = layerHigh
				}
				s.requestKeyframeLocked(layer)
				s.mu.Unlock()
			}
		}
	}
}

func (s *VideoStream) infoLocked(r *Room) VideoInfo {
	info := VideoInfo{ID: s.ID, Label: s.Label, OwnerName: s.ownerName, Channels: r.orderedLocked(s.channels), Approved: s.approved, FromRegia: s.fromRegia}
	if s.owner != nil {
		info.Owner = s.owner.ID
	}
	for _, l := range []string{layerHigh, layerLow} {
		if _, ok := s.layers[l]; ok {
			info.Layers = append(info.Layers, l)
		}
	}
	return info
}

func (r *Room) videoListLocked() []VideoInfo {
	out := make([]VideoInfo, 0, len(r.videos))
	for _, s := range r.videos {
		out = append(out, s.infoLocked(r))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

func (r *Room) broadcastVideosLocked() {
	list := r.videoListLocked()
	for _, p := range r.peers {
		p.send(Message{Type: "videos", Videos: list})
	}
}

// Videos restituisce l'elenco dei flussi video.
func (r *Room) Videos() []VideoInfo {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.videoListLocked()
}

func trimLabel(s string) string {
	rs := []rune(s)
	if len(rs) > 32 {
		rs = rs[:32]
	}
	return string(rs)
}
