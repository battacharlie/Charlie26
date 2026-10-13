package server

import (
	"crypto/tls"
	"encoding/json"
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
	"github.com/battacharlie/charlie26/internal/evento"
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
	msgs    chan sfu.Message // altri messaggi dal server (mute, kicked, me…)
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
			f.welcome <- m.ID
		case "error":
			f.t.Logf("%s: errore dal server: %s", f.name, m.Error)
			f.msgs <- m
		case "mute", "kicked", "me", "ownerToken":
			f.msgs <- m
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
}

func TestPagesAndCA(t *testing.T) {
	ts, _ := startServer(t, nil)
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
