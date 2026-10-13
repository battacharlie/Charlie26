package server

import (
	"crypto/tls"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/pion/webrtc/v4"
	"github.com/pion/webrtc/v4/pkg/media"

	"github.com/battacharlie/charlie26/internal/certs"
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

func startServer(t *testing.T) (*httptest.Server, *sfu.Room) {
	t.Helper()
	room, err := sfu.NewRoom(sfu.Config{UDPPort: freeUDPPort(t), Channels: sfu.DefaultChannels()})
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
}

func newFakePhone(t *testing.T, ts *httptest.Server, name, role, channel string) *fakePhone {
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
	f := &fakePhone{t: t, name: name, ws: ws, pc: pc, audio: audio, packets: map[string]*atomic.Int64{}, welcome: make(chan string, 1)}
	pc.OnICECandidate(func(c *webrtc.ICECandidate) {
		if c != nil {
			f.write(map[string]any{"type": "candidate", "candidate": c.ToJSON()})
		}
	})
	pc.OnTrack(func(tr *webrtc.TrackRemote, _ *webrtc.RTPReceiver) {
		ctr := f.counter(tr.StreamID())
		buf := make([]byte, 1500)
		for {
			if _, _, err := tr.Read(buf); err != nil {
				return
			}
			ctr.Add(1)
		}
	})
	t.Cleanup(func() { ws.Close(); pc.Close() })

	go f.loop()
	f.write(map[string]any{"type": "join", "name": name, "role": role, "channel": channel})
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
			f.welcome <- m.ID
		case "error":
			f.t.Logf("%s: errore dal server: %s", f.name, m.Error)
		case "offer":
			if f.pc.ConnectionState() == webrtc.PeerConnectionStateClosed {
				return
			}
			if err := f.pc.SetRemoteDescription(webrtc.SessionDescription{Type: webrtc.SDPTypeOffer, SDP: m.SDP}); err != nil {
				f.t.Logf("SetRemoteDescription: %v", err)
				return
			}
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

// Scenario completo: regia, caporeparto video, cameraman, fonico.
func TestIntercomChannels(t *testing.T) {
	ts, room := startServer(t)

	regia := newFakePhone(t, ts, "Regia", sfu.RoleRegia, "")
	capo := newFakePhone(t, ts, "CapoVideo", sfu.RoleCaporeparto, "video")
	cam := newFakePhone(t, ts, "Cam1", sfu.RoleTecnico, "video")
	fon := newFakePhone(t, ts, "Fonico", sfu.RoleTecnico, "audio")
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
	cam.talk()

	// Chi entra dopo è subito inserito nella logica.
	cam2 := newFakePhone(t, ts, "Cam2", sfu.RoleTecnico, "video")
	all = append(all, cam2)
	regia.talk("video")
	expectHearing(t, all, map[string][]string{"CapoVideo": {"Regia"}, "Cam1": {"Regia"}, "Cam2": {"Regia"}})
	regia.talk()

	// Chi esce sparisce dalla lista.
	cam2.ws.Close()
	deadline := time.Now().Add(5 * time.Second)
	for len(room.Peers()) != 4 {
		if time.Now().After(deadline) {
			t.Fatalf("il partecipante uscito è ancora in lista: %v", room.Peers())
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func TestPagesAndCA(t *testing.T) {
	ts, _ := startServer(t)
	client := ts.Client()
	for path, want := range map[string]string{
		"/":                          "Intercom di produzione",
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
