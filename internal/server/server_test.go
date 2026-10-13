package server

import (
	"bytes"
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"image"
	"image/jpeg"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/pion/rtcp"
	"github.com/pion/rtp"
	"github.com/pion/webrtc/v4"
	"github.com/pion/webrtc/v4/pkg/media"

	"github.com/battacharlie/charlie26/internal/certs"
	"github.com/battacharlie/charlie26/internal/evento"
	"github.com/battacharlie/charlie26/internal/immagini"
	"github.com/battacharlie/charlie26/internal/netinfo"
	"github.com/battacharlie/charlie26/internal/sfu"
	"github.com/battacharlie/charlie26/web"
)

func freeUDPPort(t *testing.T) int {
	c, err := net.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	return c.LocalAddr().(*net.UDPAddr).Port
}

func startServer(t *testing.T, ev *evento.Event) (*httptest.Server, *sfu.Room) {
	t.Helper()
	if ev == nil {
		ev = evento.New("Produzione", []string{"Video", "Audio", "Luci"})
	}
	room, err := sfu.NewRoom(sfu.Config{UDPPort: freeUDPPort(t), Event: ev})
	if err != nil {
		t.Fatal(err)
	}
	local, err := certs.EnsureLocal(t.TempDir(), []string{"127.0.0.1"})
	if err != nil {
		t.Fatal(err)
	}
	srv := New(Config{Host: "127.0.0.1", HTTPSPort: 8443, HTTPPort: 8080, CertMode: "locale", CAPEM: local.CAPEM, Web: web.FS, Room: room})
	ts := httptest.NewUnstartedServer(srv.HTTPSHandler())
	ts.TLS = TLSConfig(local.Server)
	ts.StartTLS()
	t.Cleanup(func() { ts.Close(); room.Close() })
	return ts, room
}

// fakePhone simula un telefono: si collega via WebSocket, invia audio Opus
// in continuazione e conta i pacchetti ricevuti da ciascun partecipante.
type fakePhone struct {
	t     *testing.T
	name  string
	ws    *websocket.Conn
	pc    *webrtc.PeerConnection
	wmu   sync.Mutex
	audio *webrtc.TrackLocalStaticSample
	id    string

	mu      sync.Mutex
	packets map[string]*atomic.Int64 // id mittente -> pacchetti ricevuti
	welcome chan string
	token   string           // gettone da caporeparto ricevuto all'ingresso (libero accesso)
	msgs    chan sfu.Message // altri messaggi dal server (mute, kicked, me…)
	videos  []sfu.VideoInfo
	vsend   *fakeVideo // video che questo telefono vuole inviare
}

// login ottiene un gettone di sessione (modalità credenziali).
func login(t *testing.T, ts *httptest.Server, user, pass string) string {
	t.Helper()
	resp, err := ts.Client().Post(ts.URL+"/api/login", "application/json", strings.NewReader(`{"username":"`+user+`","password":"`+pass+`"}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var out struct{ Session, Error string }
	_ = json.NewDecoder(resp.Body).Decode(&out)
	if out.Session == "" {
		t.Fatalf("login %s: %s", user, out.Error)
	}
	return out.Session
}

func newFakePhone(t *testing.T, ts *httptest.Server, name string, join map[string]any) *fakePhone {
	t.Helper()
	dialer := websocket.Dialer{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}
	ws, _, err := dialer.Dial("wss"+strings.TrimPrefix(ts.URL, "https")+"/ws", nil)
	if err != nil {
		t.Fatal(err)
	}
	se := webrtc.SettingEngine{}
	se.SetIncludeLoopbackCandidate(true)
	se.SetNetworkTypes([]webrtc.NetworkType{webrtc.NetworkTypeUDP4})
	api := webrtc.NewAPI(webrtc.WithSettingEngine(se))
	pc, err := api.NewPeerConnection(webrtc.Configuration{})
	if err != nil {
		t.Fatal(err)
	}
	audio, err := webrtc.NewTrackLocalStaticSample(webrtc.RTPCodecCapability{MimeType: webrtc.MimeTypeOpus, ClockRate: 48000, Channels: 2}, "mic", name)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pc.AddTrack(audio); err != nil {
		t.Fatal(err)
	}
	f := &fakePhone{t: t, name: name, ws: ws, pc: pc, audio: audio, packets: map[string]*atomic.Int64{}, welcome: make(chan string, 1), msgs: make(chan sfu.Message, 64)}
	pc.OnICECandidate(func(c *webrtc.ICECandidate) {
		if c != nil {
			f.write(map[string]any{"type": "candidate", "candidate": c.ToJSON()})
		}
	})
	pc.OnTrack(func(tr *webrtc.TrackRemote, _ *webrtc.RTPReceiver) {
		buf := make([]byte, 1500)
		if tr.Kind() == webrtc.RTPCodecTypeVideo {
			// Video: conto i pacchetti per corsia, riconoscibile dal segno
			// 'H' o 'L' che il telefono finto mette nei fotogrammi.
			pkt := &rtp.Packet{}
			for {
				n, _, err := tr.Read(buf)
				if err != nil {
					return
				}
				if pkt.Unmarshal(buf[:n]) == nil && len(pkt.Payload) > 1 && pkt.Payload[0]&0x1F == 1 {
					f.counter(tr.StreamID() + "/" + string(pkt.Payload[1])).Add(1)
				}
			}
		}
		ctr := f.counter(tr.StreamID())
		for {
			if _, _, err := tr.Read(buf); err != nil {
				return
			}
			ctr.Add(1)
		}
	})
	t.Cleanup(func() { ws.Close(); pc.Close() })

	go f.loop()
	join["type"] = "join"
	join["name"] = name
	f.write(join)
	select {
	case f.id = <-f.welcome:
	case <-time.After(5 * time.Second):
		t.Fatalf("%s: nessun benvenuto dal server", name)
	}
	go f.speak()
	return f
}

func (f *fakePhone) counter(from string) *atomic.Int64 {
	f.mu.Lock()
	defer f.mu.Unlock()
	c, ok := f.packets[from]
	if !ok {
		c = &atomic.Int64{}
		f.packets[from] = c
	}
	return c
}

func (f *fakePhone) write(v any) {
	f.wmu.Lock()
	defer f.wmu.Unlock()
	_ = f.ws.WriteJSON(v)
}

func (f *fakePhone) talk(channels ...string) {
	f.write(map[string]any{"type": "talk", "channels": channels})
}

func (f *fakePhone) loop() {
	for {
		var m sfu.Message
		if err := f.ws.ReadJSON(&m); err != nil {
			return
		}
		switch m.Type {
		case "welcome":
			f.token = m.Token
			f.welcome <- m.ID
		case "error":
			f.t.Logf("%s: errore dal server: %s", f.name, m.Error)
			f.msgs <- m
		case "mute", "kicked", "me", "ownerToken", "ownerRevoked", "videoStarted", "videoError", "videoDenied":
			f.msgs <- m
		case "videos":
			f.mu.Lock()
			f.videos = m.Videos
			f.mu.Unlock()
		case "offer":
			if f.pc.ConnectionState() == webrtc.PeerConnectionStateClosed {
				return
			}
			if err := f.pc.SetRemoteDescription(webrtc.SessionDescription{Type: webrtc.SDPTypeOffer, SDP: m.SDP}); err != nil {
				f.t.Logf("SetRemoteDescription: %v", err)
				return
			}
			f.attachVideo(m.Video)
			ans, err := f.pc.CreateAnswer(nil)
			if err != nil {
				f.t.Logf("CreateAnswer: %v", err)
				return
			}
			if err := f.pc.SetLocalDescription(ans); err != nil {
				f.t.Logf("SetLocalDescription: %v", err)
				return
			}
			f.write(map[string]any{"type": "answer", "sdp": ans.SDP})
		}
	}
}

// fakeVideo è un video finto in due corsie: ogni fotogramma porta il segno
// 'H' (alta) o 'L' (bassa); un keyframe ogni secondo o quando viene chiesto.
type fakeVideo struct {
	ref      string
	tracks   map[string]*webrtc.TrackLocalStaticSample
	attached bool
	keyframe map[string]*atomic.Bool
	stop     chan struct{}
}

func (f *fakePhone) startVideo(label string, channels ...string) {
	v := &fakeVideo{ref: "r" + label, tracks: map[string]*webrtc.TrackLocalStaticSample{}, keyframe: map[string]*atomic.Bool{}, stop: make(chan struct{})}
	for _, l := range []string{"H", "L"} {
		tr, err := webrtc.NewTrackLocalStaticSample(webrtc.RTPCodecCapability{MimeType: webrtc.MimeTypeH264, ClockRate: 90000, SDPFmtpLine: "level-asymmetry-allowed=1;packetization-mode=1;profile-level-id=42e01f"}, "cam-"+l, "cam")
		if err != nil {
			f.t.Fatal(err)
		}
		v.tracks[l] = tr
		v.keyframe[l] = &atomic.Bool{}
		v.keyframe[l].Store(true)
	}
	f.mu.Lock()
	f.vsend = v
	f.mu.Unlock()
	f.write(map[string]any{"type": "videoStart", "ref": v.ref, "label": label, "channels": channels})
}

// attachVideo: all'offerta con le linee video assegnate, aggancia le tracce.
func (f *fakePhone) attachVideo(slots []sfu.VideoSlot) {
	f.mu.Lock()
	v := f.vsend
	f.mu.Unlock()
	if v == nil || v.attached {
		return
	}
	for _, sl := range slots {
		if sl.Ref == v.ref {
			v.attached = true
		}
	}
	if !v.attached {
		return
	}
	// Come il browser: ogni corsia va sulla linea (mid) indicata dal server.
	for _, sl := range slots {
		if sl.Ref != v.ref {
			continue
		}
		l := map[string]string{"h": "H", "l": "L"}[sl.Layer]
		var tr *webrtc.RTPTransceiver
		for _, t := range f.pc.GetTransceivers() {
			if t.Mid() == sl.Mid {
				tr = t
			}
		}
		if tr == nil {
			f.t.Logf("linea video %s non trovata", sl.Mid)
			return
		}
		// Il mittente va creato dalla connessione (che conosce i codec concordati):
		// lo creo con una linea d'appoggio e lo sposto su quella indicata.
		tmp, err := f.pc.AddTransceiverFromTrack(v.tracks[l], webrtc.RTPTransceiverInit{Direction: webrtc.RTPTransceiverDirectionSendonly})
		if err != nil {
			f.t.Logf("AddTransceiverFromTrack: %v", err)
			return
		}
		sender := tmp.Sender()
		if err := tr.SetSender(sender, v.tracks[l]); err != nil {
			f.t.Logf("SetSender: %v", err)
			return
		}
		kf := v.keyframe[l]
		go func() { // il server chiede un keyframe: lo mando subito
			for {
				pkts, _, err := sender.ReadRTCP()
				if err != nil {
					return
				}
				for _, p := range pkts {
					if _, ok := p.(*rtcp.PictureLossIndication); ok {
						kf.Store(true)
					}
				}
			}
		}()
	}
	go func() {
		t := time.NewTicker(33 * time.Millisecond)
		defer t.Stop()
		for i := 0; ; i++ {
			select {
			case <-v.stop:
				return
			case <-t.C:
			}
			if f.pc.ConnectionState() == webrtc.PeerConnectionStateClosed {
				return
			}
			for l, tr := range v.tracks {
				var frame []byte
				if v.keyframe[l].Swap(false) || i%30 == 0 {
					frame = []byte{0, 0, 0, 1, 0x67, 0x42, 0xe0, 0x1f, 0, 0, 0, 1, 0x68, 0xce, 0x3c, 0x80, 0, 0, 0, 1, 0x65, l[0], 1, 2, 3}
				} else {
					frame = []byte{0, 0, 0, 1, 0x41, l[0], 9, 9, 9}
				}
				_ = tr.WriteSample(media.Sample{Data: frame, Duration: 33 * time.Millisecond})
			}
		}
	}()
}

// videoFrom: pacchetti video ricevuti per flusso e corsia in una finestra di tempo.
func videoFrom(f *fakePhone, window time.Duration) map[string]int64 {
	before := snapshot(f)
	time.Sleep(window)
	after := snapshot(f)
	out := map[string]int64{}
	for k, v := range after {
		if strings.HasPrefix(k, "v:") && v-before[k] > 5 {
			out[k] = v - before[k]
		}
	}
	return out
}

// expectVideo attende che il telefono riceva esattamente queste corsie
// (es. "v:v1/L"); senza argomenti = nessun video.
func expectVideo(t *testing.T, f *fakePhone, want ...string) {
	t.Helper()
	var got map[string]int64
	for i := 0; i < 15; i++ {
		got = videoFrom(f, 500*time.Millisecond)
		ok := len(got) == len(want)
		for _, w := range want {
			if _, has := got[w]; !has {
				ok = false
			}
		}
		if ok {
			return
		}
	}
	t.Fatalf("%s: video ricevuto %v, atteso %v", f.name, got, want)
}

// speak manda pacchetti Opus da 20 ms (contenuto finto: al server non importa).
func (f *fakePhone) speak() {
	frame := make([]byte, 60)
	t := time.NewTicker(20 * time.Millisecond)
	defer t.Stop()
	for range t.C {
		if f.pc.ConnectionState() == webrtc.PeerConnectionStateClosed {
			return
		}
		if err := f.audio.WriteSample(media.Sample{Data: frame, Duration: 20 * time.Millisecond}); err != nil {
			return
		}
	}
}

// hearing misura, in una finestra di tempo, da chi ogni telefono riceve audio.
// Restituisce per ogni telefono l'insieme dei nomi che ha sentito.
func hearing(phones []*fakePhone, window time.Duration) map[string]map[string]bool {
	byID := map[string]string{}
	for _, p := range phones {
		byID[p.id] = p.name
	}
	before := map[*fakePhone]map[string]int64{}
	for _, p := range phones {
		before[p] = snapshot(p)
	}
	time.Sleep(window)
	out := map[string]map[string]bool{}
	for _, p := range phones {
		out[p.name] = map[string]bool{}
		after := snapshot(p)
		for from, n := range after {
			// a 50 pacchetti/s, in 600 ms ne arrivano ~30: 10 è una soglia sicura
			if n-before[p][from] >= 10 {
				out[p.name][byID[from]] = true
			}
		}
	}
	return out
}

func snapshot(p *fakePhone) map[string]int64 {
	p.mu.Lock()
	defer p.mu.Unlock()
	m := map[string]int64{}
	for k, v := range p.packets {
		m[k] = v.Load()
	}
	return m
}

// expectHearing verifica, con qualche tentativo per dare tempo alle
// negoziazioni, che ognuno senta esattamente chi deve sentire.
func expectHearing(t *testing.T, phones []*fakePhone, want map[string][]string) {
	t.Helper()
	var got map[string]map[string]bool
	for attempt := 0; attempt < 15; attempt++ {
		got = hearing(phones, 600*time.Millisecond)
		if matches(got, want) {
			return
		}
	}
	t.Fatalf("ascolto errato.\nottenuto: %v\natteso:   %v", got, want)
}

func matches(got map[string]map[string]bool, want map[string][]string) bool {
	for name, heard := range got {
		w := want[name]
		if len(heard) != len(w) {
			return false
		}
		for _, x := range w {
			if !heard[x] {
				return false
			}
		}
	}
	return true
}

// expectMsg attende un messaggio di un certo tipo dal server.
func expectMsg(t *testing.T, f *fakePhone, typ string) sfu.Message {
	t.Helper()
	deadline := time.After(5 * time.Second)
	for {
		select {
		case m := <-f.msgs:
			if m.Type == typ {
				return m
			}
		case <-deadline:
			t.Fatalf("%s: nessun messaggio %q", f.name, typ)
		}
	}
}

func credentialsEvent(t *testing.T) *evento.Event {
	e := evento.New("Produzione", []string{"Video", "Audio", "Luci"})
	e.Mode = evento.ModeCredenziali
	for _, u := range []evento.User{
		{Username: "capovideo", Name: "CapoVideo", Role: evento.RoleAdmin, Channels: []string{"video"}},
		{Username: "cam1", Name: "Cam1", Role: evento.RoleUser, Channels: []string{"video"}},
		{Username: "cam2", Name: "Cam2", Role: evento.RoleUser, Channels: []string{"video"}},
		{Username: "fonico", Name: "Fonico", Role: evento.RoleUser, Channels: []string{"audio"}},
	} {
		if err := e.SetUser(u, "pass-"+u.Username); err != nil {
			t.Fatal(err)
		}
	}
	return e
}

// Scenario completo con login: regia, caporeparto video, cameraman, fonico.
func TestIntercomChannels(t *testing.T) {
	ts, room := startServer(t, credentialsEvent(t))
	user := func(u string) map[string]any { return map[string]any{"session": login(t, ts, u, "pass-"+u)} }

	regia := newFakePhone(t, ts, "Regia", map[string]any{"regia": true})
	capo := newFakePhone(t, ts, "CapoVideo", user("capovideo"))
	cam := newFakePhone(t, ts, "Cam1", user("cam1"))
	fon := newFakePhone(t, ts, "Fonico", user("fonico"))
	all := []*fakePhone{regia, capo, cam, fon}

	// Nessuno preme "parla": silenzio totale, anche se i microfoni mandano audio.
	expectHearing(t, all, map[string][]string{})

	// Il cameraman parla nel canale video: lo sentono capo video e regia.
	cam.talk("video")
	expectHearing(t, all, map[string][]string{"Regia": {"Cam1"}, "CapoVideo": {"Cam1"}})
	cam.talk()

	// Il caporeparto parla in Produzione: lo sente solo la regia, non i suoi cameraman.
	capo.talk("produzione")
	expectHearing(t, all, map[string][]string{"Regia": {"CapoVideo"}})
	capo.talk()

	// La regia parla solo all'audio: sente solo il fonico.
	regia.talk("audio")
	expectHearing(t, all, map[string][]string{"Fonico": {"Regia"}})

	// La regia "parla a tutti".
	regia.talk("produzione", "video", "audio", "luci")
	expectHearing(t, all, map[string][]string{"CapoVideo": {"Regia"}, "Cam1": {"Regia"}, "Fonico": {"Regia"}})
	regia.talk()

	// Un tecnico non può parlare su un canale di cui non è membro.
	fon.talk("video", "produzione")
	expectHearing(t, all, map[string][]string{})
	fon.talk()

	// La regia spegne l'ascolto del canale video: il cameraman lo sente solo il capo.
	regia.write(map[string]any{"type": "listen", "channel": "video", "on": false})
	cam.talk("video")
	expectHearing(t, all, map[string][]string{"CapoVideo": {"Cam1"}})
	regia.write(map[string]any{"type": "listen", "channel": "video", "on": true})
	expectHearing(t, all, map[string][]string{"CapoVideo": {"Cam1"}, "Regia": {"Cam1"}})

	// Il caporeparto chiude il microfono del cameraman.
	capo.write(map[string]any{"type": "control", "peer": cam.id, "channel": "video", "on": false})
	expectMsg(t, cam, "mute")
	expectHearing(t, all, map[string][]string{})

	// Modalità "solo il caporeparto apre": il cameraman non riesce ad aprirsi da solo...
	capo.write(map[string]any{"type": "channelSettings", "channel": "video", "micMode": evento.MicAdmin, "video": true})
	time.Sleep(200 * time.Millisecond)
	cam.talk("video")
	expectHearing(t, all, map[string][]string{})
	// ...ma il caporeparto può aprirglielo.
	capo.write(map[string]any{"type": "control", "peer": cam.id, "channel": "video", "on": true})
	expectHearing(t, all, map[string][]string{"CapoVideo": {"Cam1"}, "Regia": {"Cam1"}})
	capo.write(map[string]any{"type": "control", "peer": cam.id, "channel": "video", "on": false})
	expectHearing(t, all, map[string][]string{})

	// Il fonico non ha poteri sul canale video.
	fon.write(map[string]any{"type": "control", "peer": cam.id, "channel": "video", "on": true})
	expectMsg(t, fon, "error")
	expectHearing(t, all, map[string][]string{})
	capo.write(map[string]any{"type": "channelSettings", "channel": "video", "micMode": evento.MicLibero, "video": true})

	// La regia sposta il fonico anche sul canale video: lo sente subito.
	err := room.UpdateEvent(func(e *evento.Event) error {
		u := *e.User("fonico")
		u.Channels = []string{"audio", "video"}
		return e.SetUser(u, "")
	})
	if err != nil {
		t.Fatal(err)
	}
	expectMsg(t, fon, "me")
	regia.talk("video")
	expectHearing(t, all, map[string][]string{"CapoVideo": {"Regia"}, "Cam1": {"Regia"}, "Fonico": {"Regia"}})
	regia.talk()

	// Chi entra dopo è subito inserito nella logica.
	cam2 := newFakePhone(t, ts, "Cam2", user("cam2"))
	all = append(all, cam2)
	regia.talk("video")
	expectHearing(t, all, map[string][]string{"CapoVideo": {"Regia"}, "Cam1": {"Regia"}, "Cam2": {"Regia"}, "Fonico": {"Regia"}})
	regia.talk()

	// La regia elimina l'utente cam2: viene scollegato.
	if err := room.UpdateEvent(func(e *evento.Event) error { return e.RemoveUser("cam2") }); err != nil {
		t.Fatal(err)
	}
	expectMsg(t, cam2, "kicked")
	deadline := time.Now().Add(5 * time.Second)
	for len(room.Peers()) != 4 {
		if time.Now().After(deadline) {
			t.Fatalf("il partecipante eliminato è ancora in lista: %v", room.Peers())
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// Libero accesso: chi crea un sottocanale ne è responsabile.
func TestFreeAccessSubchannel(t *testing.T) {
	ts, _ := startServer(t, evento.New("Produzione", nil))

	regia := newFakePhone(t, ts, "Regia", map[string]any{"regia": true})
	luca := newFakePhone(t, ts, "Luca", map[string]any{"createName": "Palco", "createPassword": "1234"})
	ugo := newFakePhone(t, ts, "Ugo", map[string]any{"channel": "palco", "password": "1234"})
	anna := newFakePhone(t, ts, "Anna", map[string]any{"channel": "produzione"})
	all := []*fakePhone{regia, luca, ugo, anna}

	ugo.talk("palco")
	expectHearing(t, all, map[string][]string{"Luca": {"Ugo"}, "Regia": {"Ugo"}})
	ugo.talk()
	luca.talk("produzione")
	expectHearing(t, all, map[string][]string{"Regia": {"Luca"}, "Anna": {"Luca"}})
	luca.talk()
	// Luca è responsabile di Palco: può chiudere il microfono di Ugo.
	ugo.talk("palco")
	expectHearing(t, all, map[string][]string{"Luca": {"Ugo"}, "Regia": {"Ugo"}})
	luca.write(map[string]any{"type": "control", "peer": ugo.id, "channel": "palco", "on": false})
	expectMsg(t, ugo, "mute")
	expectHearing(t, all, map[string][]string{})

	// La regia toglie a Luca il ruolo di caporeparto.
	regia.write(map[string]any{"type": "demote", "peer": luca.id})
	expectMsg(t, luca, "ownerRevoked")
	if me := expectMsg(t, luca, "me"); me.Me.Role != sfu.RoleTecnico || len(me.Me.Admin) != 0 {
		t.Fatalf("dopo la revoca Luca deve essere tecnico: %+v", me.Me)
	}
	// Non ha più poteri su Ugo e non parla più in Produzione.
	luca.write(map[string]any{"type": "control", "peer": ugo.id, "channel": "palco", "on": true})
	expectMsg(t, luca, "error")
	luca.talk("produzione")
	expectHearing(t, all, map[string][]string{})
	luca.talk()

	// Il vecchio gettone di Luca non vale più: senza password non entra in Palco.
	if luca.token == "" {
		t.Fatal("Luca avrebbe dovuto ricevere un gettone creando Palco")
	}
	ws, _, err := (&websocket.Dialer{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}).Dial("wss"+strings.TrimPrefix(ts.URL, "https")+"/ws", nil)
	if err != nil {
		t.Fatal(err)
	}
	_ = ws.WriteJSON(map[string]any{"type": "join", "name": "Luca-bis", "channel": "palco", "ownerTokens": []string{luca.token}})
	var reply sfu.Message
	if err := ws.ReadJSON(&reply); err != nil || reply.Type != "error" {
		t.Fatalf("con il gettone revocato non deve entrare senza password: %+v %v", reply, err)
	}
	ws.Close()

	// La regia nomina caporeparto Ugo: riceve il gettone e i poteri.
	regia.write(map[string]any{"type": "promote", "peer": ugo.id, "channel": "palco"})
	tok := expectMsg(t, ugo, "ownerToken")
	if me := expectMsg(t, ugo, "me"); me.Me.Role != sfu.RoleCaporeparto {
		t.Fatalf("Ugo deve essere caporeparto: %+v", me.Me)
	}
	// Con il gettone si rientra da caporeparto anche senza password.
	ugo2 := newFakePhone(t, ts, "Ugo-bis", map[string]any{"channel": "palco", "ownerTokens": []string{tok.Token}})
	_ = ugo2
	if p := findPeer(t, ts, "Ugo-bis"); p.Role != sfu.RoleCaporeparto {
		t.Fatalf("rientro con gettone: %+v", p)
	}
}

func findPeer(t *testing.T, ts *httptest.Server, name string) sfu.PeerInfo {
	t.Helper()
	resp, err := ts.Client().Get(ts.URL + "/api/info")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var info struct{ Peers []sfu.PeerInfo }
	_ = json.NewDecoder(resp.Body).Decode(&info)
	for _, p := range info.Peers {
		if p.Name == name {
			return p
		}
	}
	t.Fatalf("%s non è collegato", name)
	return sfu.PeerInfo{}
}

// Credenziali: la regia nomina e toglie il ruolo di caporeparto dalla lista dei collegati.
func TestCredentialsPromoteDemote(t *testing.T) {
	ts, room := startServer(t, credentialsEvent(t))
	regia := newFakePhone(t, ts, "Regia", map[string]any{"regia": true})
	cam := newFakePhone(t, ts, "Cam1", map[string]any{"session": login(t, ts, "cam1", "pass-cam1")})

	regia.write(map[string]any{"type": "promote", "peer": cam.id, "channel": "video"})
	if me := expectMsg(t, cam, "me"); me.Me.Role != sfu.RoleCaporeparto || len(me.Me.Channels) != 2 {
		t.Fatalf("cam1 promosso: %+v", me.Me)
	}
	if room.Event().User("cam1").Role != evento.RoleAdmin {
		t.Fatal("la promozione deve essere salvata nell'utente")
	}
	regia.write(map[string]any{"type": "demote", "peer": cam.id})
	if me := expectMsg(t, cam, "me"); me.Me.Role != sfu.RoleTecnico {
		t.Fatalf("cam1 riportato a tecnico: %+v", me.Me)
	}
	if room.Event().User("cam1").Role != evento.RoleUser {
		t.Fatal("la revoca deve essere salvata nell'utente")
	}
	// Un tecnico non può togliere o dare ruoli.
	cam.write(map[string]any{"type": "promote", "peer": cam.id, "channel": "video"})
	expectMsg(t, cam, "error")
}

func TestRegiaAPI(t *testing.T) {
	ts, room := startServer(t, nil)
	post := func(body string) (int, string) {
		resp, err := ts.Client().Post(ts.URL+"/api/regia/evento", "application/json", strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(b)
	}
	if code, body := post(`{"op":"addChannel","name":"Palco"}`); code != 200 || !strings.Contains(body, `"palco"`) {
		t.Fatalf("aggiunta canale: %d %s", code, body)
	}
	if code, _ := post(`{"op":"removeChannel","id":"produzione"}`); code != 400 {
		t.Fatal("il canale principale non deve essere eliminabile")
	}
	if code, body := post(`{"op":"setUser","user":{"username":"marco","role":"user","channels":["palco"]},"password":"abcd"}`); code != 200 || strings.Contains(body, "$2a$") {
		t.Fatalf("aggiunta utente: %d %s (non deve mostrare l'impronta della password)", code, body)
	}
	// Richieste da un altro sito (o non JSON) vengono rifiutate.
	for _, h := range []map[string]string{
		{"Content-Type": "text/plain"},
		{"Content-Type": "application/json", "Origin": "https://sito-malevolo.example"},
	} {
		req, _ := http.NewRequest("POST", ts.URL+"/api/regia/evento", strings.NewReader(`{"op":"addChannel","name":"Hack"}`))
		for k, v := range h {
			req.Header.Set(k, v)
		}
		resp, err := ts.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusForbidden {
			t.Fatalf("richiesta %v: stato %d, atteso 403", h, resp.StatusCode)
		}
	}
	if code, _ := post(`{"op":"mode","mode":"credenziali"}`); code != 200 || room.Mode() != evento.ModeCredenziali {
		t.Fatal("cambio modalità non riuscito")
	}
	login(t, ts, "marco", "abcd")

	// Esporta, cambia tutto, reimporta: torna com'era e la password vale ancora.
	resp, err := ts.Client().Get(ts.URL + "/api/regia/esporta")
	if err != nil {
		t.Fatal(err)
	}
	exported, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(resp.Header.Get("Content-Disposition"), "attachment") || !strings.Contains(string(exported), evento.FileType) {
		t.Fatalf("esportazione: %s", exported)
	}
	if code, _ := post(`{"op":"removeUser","user":{"username":"marco"}}`); code != 200 {
		t.Fatal("eliminazione utente")
	}
	if code, _ := post(`{"op":"mode","mode":"libero"}`); code != 200 {
		t.Fatal("cambio modalità")
	}
	importa := func(body string) int {
		resp, err := ts.Client().Post(ts.URL+"/api/regia/importa", "application/json", strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		return resp.StatusCode
	}
	if code := importa(string(exported)); code != 200 {
		t.Fatalf("importazione: %d", code)
	}
	if room.Mode() != evento.ModeCredenziali || room.Event().User("marco") == nil {
		t.Fatal("dopo l'importazione modalità e utenti devono tornare quelli esportati")
	}
	login(t, ts, "marco", "abcd")
	// File non validi: rifiutati senza toccare l'evento.
	for _, bad := range []string{`ciao`, `{"tipo":"altro"}`, `{"tipo":"` + evento.FileType + `","evento":{"mode":"libero","channels":[]}}`} {
		if code := importa(bad); code != 400 {
			t.Fatalf("file non valido %q accettato (%d)", bad, code)
		}
	}
	if room.Event().User("marco") == nil {
		t.Fatal("un'importazione fallita non deve cambiare l'evento")
	}
}

func TestLogAndShutdown(t *testing.T) {
	room, err := sfu.NewRoom(sfu.Config{UDPPort: freeUDPPort(t)})
	if err != nil {
		t.Fatal(err)
	}
	defer room.Close()
	off := make(chan struct{})
	srv := New(Config{Host: "127.0.0.1", HTTPSPort: 8443, Web: web.FS, Room: room,
		Log:      func() []string { return []string{"riga uno", "riga due"} },
		Shutdown: func() { close(off) },
	})
	ts := httptest.NewTLSServer(srv.HTTPSHandler())
	defer ts.Close()
	resp, err := ts.Client().Get(ts.URL + "/api/regia/registro")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(b), "riga due") {
		t.Fatalf("registro: %s", b)
	}
	// Spegnimento: solo con richiesta JSON (un sito esterno non può spegnere l'intercom).
	resp, _ = ts.Client().Post(ts.URL+"/api/regia/spegni", "text/plain", strings.NewReader("{}"))
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("spegnimento senza JSON: %d", resp.StatusCode)
	}
	resp, _ = ts.Client().Post(ts.URL+"/api/regia/spegni", "application/json", strings.NewReader("{}"))
	resp.Body.Close()
	select {
	case <-off:
	case <-time.After(3 * time.Second):
		t.Fatal("lo spegnimento non è partito")
	}
}

func TestPagesAndCA(t *testing.T) {
	ts, _ := startServer(t, nil)
	client := ts.Client()
	for path, want := range map[string]string{
		"/":                          "CHARLIE26",
		"/regia":                     "Console di regia",
		"/aiuto":                     "https://127.0.0.1:8443/",
		"/ca.crt":                    "BEGIN CERTIFICATE",
		"/static/app.js":             "getUserMedia",
		"/api/info":                  `"produzione"`,
		"/static/rnnoise-worklet.js": "registerProcessor('rnnoise'",
	} {
		resp, err := client.Get(ts.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK || !strings.Contains(string(body), want) {
			t.Fatalf("%s: stato %d, manca %q", path, resp.StatusCode, want)
		}
	}
	// Il modulo del filtro non deve contenere "export": deve essere un unico script.
	resp, err := client.Get(ts.URL + "/static/rnnoise-worklet.js")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if strings.Contains(string(body), "export default") || !strings.Contains(string(body), "createRNNWasmModuleSync") {
		t.Fatal("rnnoise-worklet.js non è stato unito correttamente")
	}

	resp, err = client.Get(ts.URL + "/qr/app.png")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.Header.Get("Content-Type") != "image/png" {
		t.Fatalf("QR: content-type %q", resp.Header.Get("Content-Type"))
	}
}

func TestChangeNetwork(t *testing.T) {
	room, err := sfu.NewRoom(sfu.Config{UDPPort: freeUDPPort(t)})
	if err != nil {
		t.Fatal(err)
	}
	defer room.Close()
	var asked string
	srv := New(Config{Host: "192.168.1.10", HTTPSPort: 8443, HTTPPort: 8080, Web: web.FS, Room: room,
		DetectIPs: func() []netinfo.Address {
			return []netinfo.Address{{IP: "192.168.1.10", Interface: "Ethernet"}, {IP: "10.0.0.7", Interface: "Wi-Fi"}}
		},
		ChangeIP: func(ip string) (string, error) { asked = ip; return ip, nil },
	})
	ts := httptest.NewTLSServer(srv.HTTPSHandler())
	defer ts.Close()

	resp, err := ts.Client().Post(ts.URL+"/api/regia/rete", "application/json", strings.NewReader(`{"ip":"10.0.0.7"}`))
	if err != nil {
		t.Fatal(err)
	}
	var out struct {
		Host, AppURL string
		IPs          []netinfo.Address
	}
	_ = json.NewDecoder(resp.Body).Decode(&out)
	resp.Body.Close()
	if asked != "10.0.0.7" || out.Host != "10.0.0.7" || out.AppURL != "https://10.0.0.7:8443/" || len(out.IPs) != 2 {
		t.Fatalf("cambio rete: %+v (richiesto %q)", out, asked)
	}
	if srv.AppURL() != "https://10.0.0.7:8443/" || !strings.Contains(srv.HelpURL(), "10.0.0.7") {
		t.Fatal("i QR devono puntare al nuovo IP")
	}
	resp, _ = ts.Client().Post(ts.URL+"/api/regia/rete", "application/json", strings.NewReader(`{"ip":"non-un-ip"}`))
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest || srv.Host() != "10.0.0.7" {
		t.Fatal("un IP non valido deve essere rifiutato senza cambiare nulla")
	}
}

func TestUserQR(t *testing.T) {
	ts, room := startServer(t, credentialsEvent(t))
	get := func(path string) (int, []byte) {
		resp, err := ts.Client().Get(ts.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, b
	}
	if code, b := get("/qr/utente/cam1.png"); code != 200 || !bytes.HasPrefix(b, []byte("\x89PNG")) {
		t.Fatalf("QR personale: %d", code)
	}
	if code, _ := get("/qr/utente/nessuno.png"); code != 404 {
		t.Fatal("QR di un utente inesistente")
	}
	if code, _ := get("/qr/app.png?canale=video"); code != 200 {
		t.Fatal("QR di un canale")
	}
	if code, _ := get("/qr/app.png?canale=inventato"); code != 404 {
		t.Fatal("QR di un canale inesistente")
	}
	key, err := room.UserQRKey("cam1", false)
	if err != nil || len(key) < 32 {
		t.Fatalf("chiave: %q %v", key, err)
	}
	if room.Event().User("cam1").QRKey != "" {
		t.Fatal("la configurazione mostrata nella console non deve contenere la chiave")
	}
	loginKey := func(k string) (int, string) {
		resp, err := ts.Client().Post(ts.URL+"/api/login", "application/json", strings.NewReader(`{"key":"`+k+`"}`))
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		var out struct{ Session, Name string }
		_ = json.NewDecoder(resp.Body).Decode(&out)
		return resp.StatusCode, out.Name
	}
	if code, name := loginKey(key); code != 200 || name != "Cam1" {
		t.Fatalf("accesso con chiave: %d %q", code, name)
	}
	// Con la sessione ottenuta dalla chiave si entra davvero.
	resp, _ := ts.Client().Post(ts.URL+"/api/login", "application/json", strings.NewReader(`{"key":"`+key+`"}`))
	var out struct{ Session string }
	_ = json.NewDecoder(resp.Body).Decode(&out)
	resp.Body.Close()
	cam := newFakePhone(t, ts, "Cam1", map[string]any{"session": out.Session})
	if p := findPeer(t, ts, "Cam1"); p.ID != cam.id {
		t.Fatal("ingresso con QR personale non riuscito")
	}
	// Nuovo codice: quello vecchio non vale più.
	if _, err := room.UserQRKey("cam1", true); err != nil {
		t.Fatal(err)
	}
	if code, _ := loginKey(key); code != http.StatusUnauthorized {
		t.Fatalf("la chiave vecchia deve essere rifiutata: %d", code)
	}
	if code, _ := loginKey(strings.Repeat("0", 48)); code != http.StatusUnauthorized {
		t.Fatal("chiave inventata accettata")
	}
}

func TestModels(t *testing.T) {
	room, err := sfu.NewRoom(sfu.Config{UDPPort: freeUDPPort(t), Event: evento.New("Produzione", []string{"Video"})})
	if err != nil {
		t.Fatal(err)
	}
	defer room.Close()
	srv := New(Config{Host: "127.0.0.1", HTTPSPort: 8443, Web: web.FS, Room: room, ModelsDir: filepath.Join(t.TempDir(), "modelli")})
	ts := httptest.NewTLSServer(srv.HTTPSHandler())
	defer ts.Close()
	call := func(body string) (int, []map[string]any) {
		resp, err := ts.Client().Post(ts.URL+"/api/regia/modelli", "application/json", strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		var list []map[string]any
		_ = json.NewDecoder(resp.Body).Decode(&list)
		return resp.StatusCode, list
	}
	code, list := call(`{"op":"save","nome":"Concerto piazza"}`)
	if code != 200 || len(list) != 1 || list[0]["nome"] != "Concerto piazza" || list[0]["file"] != "concerto-piazza.json" {
		t.Fatalf("salvataggio modello: %d %v", code, list)
	}
	// Cambio l'evento, poi ricarico il modello.
	_ = room.UpdateEvent(func(e *evento.Event) error { _, err := e.AddChannel("Palco"); return err })
	if len(room.Channels()) != 3 {
		t.Fatal("canale non aggiunto")
	}
	if code, _ := call(`{"op":"load","file":"concerto-piazza.json"}`); code != 200 || len(room.Channels()) != 2 {
		t.Fatalf("caricamento modello: %d, canali %d", code, len(room.Channels()))
	}
	// Nomi di file pericolosi rifiutati.
	for _, f := range []string{"../evento.json", "..%2Fevento.json", "/etc/passwd", "a.txt"} {
		if code, _ := call(`{"op":"load","file":"` + f + `"}`); code != 400 {
			t.Fatalf("file %q accettato", f)
		}
	}
	if code, list := call(`{"op":"delete","file":"concerto-piazza.json"}`); code != 200 || len(list) != 0 {
		t.Fatalf("eliminazione: %d %v", code, list)
	}
}

// Video: permessi, consenso, qualità, stop.
func TestVideo(t *testing.T) {
	ts, room := startServer(t, credentialsEvent(t))
	user := func(u string) map[string]any { return map[string]any{"session": login(t, ts, u, "pass-"+u)} }
	regia := newFakePhone(t, ts, "Regia", map[string]any{"regia": true})
	capo := newFakePhone(t, ts, "CapoVideo", user("capovideo"))
	cam1 := newFakePhone(t, ts, "Cam1", user("cam1"))
	cam2 := newFakePhone(t, ts, "Cam2", user("cam2"))
	fon := newFakePhone(t, ts, "Fonico", user("fonico"))

	// Il caporeparto manda un video nel suo canale: parte subito.
	capo.startVideo("Palco", "video")
	started := expectMsg(t, capo, "videoStarted")
	v1 := "v:" + started.Stream
	if !started.Videos[0].Approved {
		t.Fatal("il video del caporeparto nel suo canale deve partire senza consenso")
	}
	// Qualità predefinita: bassa. Il fonico (altro reparto) non riceve nulla.
	expectVideo(t, cam1, v1+"/L")
	expectVideo(t, cam2, v1+"/L")
	expectVideo(t, regia, v1+"/L")
	expectVideo(t, fon)
	// Cam1 sceglie la qualità alta: passa alla corsia H.
	cam1.write(map[string]any{"type": "videoQuality", "quality": "h"})
	expectVideo(t, cam1, v1+"/H")
	expectVideo(t, cam2, v1+"/L")
	// Video spento: niente pacchetti.
	cam2.write(map[string]any{"type": "videoQuality", "quality": "off"})
	expectVideo(t, cam2)
	cam2.write(map[string]any{"type": "videoQuality", "quality": "l"})
	expectVideo(t, cam2, v1+"/L")

	// Un tecnico chiede di mostrare un video: in attesa di consenso.
	cam1.startVideo("Cablaggio", "video")
	req := expectMsg(t, cam1, "videoStarted")
	v2 := "v:" + req.Stream
	if req.Videos[0].Approved {
		t.Fatal("il video di un tecnico deve aspettare il consenso")
	}
	// Lo vedono solo chi deve decidere (caporeparto e regia), non gli altri tecnici.
	expectVideo(t, capo, v2+"/L")
	expectVideo(t, regia, v1+"/L", v2+"/L")
	expectVideo(t, cam2, v1+"/L")
	// Un tecnico non può approvare.
	cam2.write(map[string]any{"type": "videoApprove", "stream": req.Stream, "on": true})
	expectMsg(t, cam2, "error")
	// Il caporeparto approva: ora lo vede anche Cam2.
	capo.write(map[string]any{"type": "videoApprove", "stream": req.Stream, "on": true})
	expectVideo(t, cam2, v1+"/L", v2+"/L")
	expectVideo(t, fon)

	// Il caporeparto ferma il video di Cam1.
	capo.write(map[string]any{"type": "videoStop", "stream": req.Stream})
	expectMsg(t, cam1, "videoDenied")
	expectVideo(t, cam2, v1+"/L")

	// Video della regia: arriva nel canale Video solo se il caporeparto lo permette.
	regia.startVideo("Programma", "video", "audio")
	pv := "v:" + expectMsg(t, regia, "videoStarted").Stream
	expectVideo(t, fon) // canale Audio: video della regia non abilitato
	// Il caporeparto non può fermare il video della regia.
	capo.write(map[string]any{"type": "videoStop", "stream": strings.TrimPrefix(pv, "v:")})
	expectMsg(t, capo, "error")
	expectVideo(t, cam2, v1+"/L")
	capo.write(map[string]any{"type": "channelSettings", "channel": "video", "micMode": "libero", "video": true})
	expectVideo(t, cam2, v1+"/L", pv+"/L")
	expectVideo(t, fon)
	if n := len(room.Videos()); n != 2 {
		t.Fatalf("attesi 2 video attivi, trovati %d", n)
	}

	// Chi esce: i suoi video si fermano.
	capo.ws.Close()
	expectVideo(t, cam2, pv+"/L")
}

// WHIP: un client come OBS invia video con la chiave.
func TestWHIP(t *testing.T) {
	ts, room := startServer(t, credentialsEvent(t))
	user := func(u string) map[string]any { return map[string]any{"session": login(t, ts, u, "pass-"+u)} }
	regia := newFakePhone(t, ts, "Regia", map[string]any{"regia": true})
	capo := newFakePhone(t, ts, "CapoVideo", user("capovideo")) // in Produzione (video della regia abilitato)
	cam1 := newFakePhone(t, ts, "Cam1", user("cam1"))           // solo canale Video (non abilitato)

	token := room.WHIPToken(false)
	se := webrtc.SettingEngine{}
	se.SetIncludeLoopbackCandidate(true)
	se.SetNetworkTypes([]webrtc.NetworkType{webrtc.NetworkTypeUDP4})
	obs, err := webrtc.NewAPI(webrtc.WithSettingEngine(se)).NewPeerConnection(webrtc.Configuration{})
	if err != nil {
		t.Fatal(err)
	}
	defer obs.Close()
	track, _ := webrtc.NewTrackLocalStaticSample(webrtc.RTPCodecCapability{MimeType: webrtc.MimeTypeH264, ClockRate: 90000, SDPFmtpLine: "level-asymmetry-allowed=1;packetization-mode=1;profile-level-id=42e01f"}, "obs", "obs")
	if _, err := obs.AddTransceiverFromTrack(track, webrtc.RTPTransceiverInit{Direction: webrtc.RTPTransceiverDirectionSendonly}); err != nil {
		t.Fatal(err)
	}
	offer, _ := obs.CreateOffer(nil)
	done := webrtc.GatheringCompletePromise(obs)
	_ = obs.SetLocalDescription(offer)
	<-done

	post := func(tok string) *http.Response {
		req, _ := http.NewRequest("POST", ts.URL+"/whip?titolo=Programma", strings.NewReader(obs.LocalDescription().SDP))
		req.Header.Set("Content-Type", "application/sdp")
		req.Header.Set("Authorization", "Bearer "+tok)
		resp, err := ts.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		return resp
	}
	if resp := post("sbagliata"); resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("chiave sbagliata: %d", resp.StatusCode)
	}
	resp := post(token)
	answer, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusCreated || !strings.HasPrefix(resp.Header.Get("Location"), "/whip/v") {
		t.Fatalf("WHIP: %d %s", resp.StatusCode, answer)
	}
	if err := obs.SetRemoteDescription(webrtc.SessionDescription{Type: webrtc.SDPTypeAnswer, SDP: string(answer)}); err != nil {
		t.Fatal(err)
	}
	stop := make(chan struct{})
	defer close(stop)
	go func() {
		tk := time.NewTicker(33 * time.Millisecond)
		defer tk.Stop()
		for i := 0; ; i++ {
			select {
			case <-stop:
				return
			case <-tk.C:
			}
			frame := []byte{0, 0, 0, 1, 0x41, 'H', 9, 9}
			if i%15 == 0 {
				frame = []byte{0, 0, 0, 1, 0x67, 0x42, 0xe0, 0x1f, 0, 0, 0, 1, 0x65, 'H', 1, 2}
			}
			_ = track.WriteSample(media.Sample{Data: frame, Duration: 33 * time.Millisecond})
		}
	}()
	id := strings.TrimPrefix(resp.Header.Get("Location"), "/whip/")
	v := "v:" + id
	// Una sola corsia: anche con qualità bassa arriva la H. Il canale Video
	// (video della regia non abilitato) non lo riceve.
	expectVideo(t, capo, v+"/H")
	expectVideo(t, regia, v+"/H")
	expectVideo(t, cam1)
	if vs := room.Videos(); len(vs) != 1 || vs[0].Label != "Programma" || vs[0].OwnerName != "OBS" {
		t.Fatalf("elenco video: %+v", vs)
	}
	// La regia lo manda solo al canale Video... che però non lo ha abilitato:
	// non lo vede più nessuno tranne la regia.
	regia.write(map[string]any{"type": "videoChannels", "stream": id, "channels": []string{"video"}})
	expectVideo(t, capo)
	expectVideo(t, regia, v+"/H")
	// Un tecnico non può cambiare i canali.
	cam1.write(map[string]any{"type": "videoChannels", "stream": id, "channels": []string{"produzione"}})
	expectMsg(t, cam1, "error")

	// Stop da OBS (DELETE con la chiave).
	req, _ := http.NewRequest("DELETE", ts.URL+resp.Header.Get("Location"), nil)
	req.Header.Set("Authorization", "Bearer "+token)
	dr, err := ts.Client().Do(req)
	if err != nil || dr.StatusCode != 200 {
		t.Fatalf("DELETE: %v %v", err, dr)
	}
	dr.Body.Close()
	if len(room.Videos()) != 0 {
		t.Fatal("dopo DELETE il video deve sparire")
	}
	expectVideo(t, regia)
}

func testImage(t *testing.T, w, h int) string {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	var b bytes.Buffer
	if err := jpeg.Encode(&b, img, nil); err != nil {
		t.Fatal(err)
	}
	return "data:image/jpeg;base64," + base64.StdEncoding.EncodeToString(b.Bytes())
}

func TestLook(t *testing.T) {
	ev := credentialsEvent(t)
	room, err := sfu.NewRoom(sfu.Config{UDPPort: freeUDPPort(t), Event: ev})
	if err != nil {
		t.Fatal(err)
	}
	defer room.Close()
	local, _ := certs.EnsureLocal(t.TempDir(), []string{"127.0.0.1"})
	store := immagini.Store{Dir: filepath.Join(t.TempDir(), "immagini")}
	srv := New(Config{Host: "127.0.0.1", HTTPSPort: 8443, Web: web.FS, Room: room, Images: store})
	ts := httptest.NewUnstartedServer(srv.HTTPSHandler())
	ts.TLS = TLSConfig(local.Server)
	ts.StartTLS()
	defer ts.Close()
	postJSON := func(path string, v any) int {
		b, _ := json.Marshal(v)
		resp, err := ts.Client().Post(ts.URL+path, "application/json", bytes.NewReader(b))
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		return resp.StatusCode
	}

	// La regia: sfondo generale con colore e immagine.
	if code := postJSON("/api/regia/aspetto", map[string]any{"color": "#123456", "imageData": testImage(t, 800, 450)}); code != 200 {
		t.Fatalf("sfondo: %d", code)
	}
	look := room.Look()
	if look.Color != "#123456" || look.Image == "" {
		t.Fatalf("sfondo non salvato: %+v", look)
	}
	resp, _ := ts.Client().Get(ts.URL + "/img/" + look.Image)
	resp.Body.Close()
	if resp.StatusCode != 200 || resp.Header.Get("Content-Type") != "image/jpeg" {
		t.Fatalf("immagine servita: %d %s", resp.StatusCode, resp.Header.Get("Content-Type"))
	}
	if code := postJSON("/api/regia/aspetto", map[string]any{"color": "rosso"}); code != 400 {
		t.Fatal("colore non valido accettato")
	}

	// Il caporeparto cambia l'icona del suo canale; un tecnico non può.
	user := func(u string) map[string]any { return map[string]any{"session": login(t, ts, u, "pass-"+u)} }
	capo := newFakePhone(t, ts, "CapoVideo", user("capovideo"))
	cam := newFakePhone(t, ts, "Cam1", user("cam1"))
	capo.write(map[string]any{"type": "channelLook", "channel": "video", "iconData": testImage(t, 128, 128), "color": "#202020"})
	cam.write(map[string]any{"type": "channelLook", "channel": "video", "icon": "m3"})
	expectMsg(t, cam, "error")
	capo.write(map[string]any{"type": "channelLook", "channel": "produzione", "icon": "m2"})
	expectMsg(t, capo, "error") // Produzione è della regia
	deadline := time.Now().Add(3 * time.Second)
	var vch sfu.ChannelInfo
	for time.Now().Before(deadline) {
		for _, c := range room.Channels() {
			if c.ID == "video" {
				vch = c
			}
		}
		if vch.Icon != "" {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if !store.Exists(vch.Icon) || vch.Look.Color != "#202020" {
		t.Fatalf("icona del canale: %+v", vch)
	}

	// Export con le immagini, import su un "altro computer" (archivio vuoto).
	resp, _ = ts.Client().Get(ts.URL + "/api/regia/esporta")
	exported, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	var f evento.ExportFile
	_ = json.Unmarshal(exported, &f)
	if len(f.Immagini) != 2 {
		t.Fatalf("l'export deve contenere 2 immagini, ne ha %d", len(f.Immagini))
	}
	room2, _ := sfu.NewRoom(sfu.Config{UDPPort: freeUDPPort(t)})
	defer room2.Close()
	store2 := immagini.Store{Dir: filepath.Join(t.TempDir(), "altro")}
	ts2 := httptest.NewTLSServer(New(Config{Host: "127.0.0.1", HTTPSPort: 8443, Web: web.FS, Room: room2, Images: store2}).HTTPSHandler())
	defer ts2.Close()
	r2, err := ts2.Client().Post(ts2.URL+"/api/regia/importa", "application/json", bytes.NewReader(exported))
	if err != nil || r2.StatusCode != 200 {
		t.Fatalf("import: %v %v", err, r2)
	}
	r2.Body.Close()
	if !store2.Exists(room2.Look().Image) || !store2.Exists(vch.Icon) {
		t.Fatal("dopo l'import le immagini devono esistere anche sull'altro computer")
	}
}
