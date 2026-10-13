package server

import (
	"crypto/tls"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
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
	room, err := sfu.NewRoom(sfu.Config{UDPPort: freeUDPPort(t)})
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
// e conta i pacchetti audio che riceve dagli altri.
type fakePhone struct {
	t        *testing.T
	ws       *websocket.Conn
	pc       *webrtc.PeerConnection
	wmu      sync.Mutex
	received chan string // id del partecipante da cui arriva audio
	audio    *webrtc.TrackLocalStaticSample
	id       chan string
}

func newFakePhone(t *testing.T, ts *httptest.Server, name string) *fakePhone {
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
	f := &fakePhone{t: t, ws: ws, pc: pc, audio: audio, received: make(chan string, 16), id: make(chan string, 1)}
	pc.OnICECandidate(func(c *webrtc.ICECandidate) {
		if c != nil {
			init := c.ToJSON()
			f.write(map[string]any{"type": "candidate", "candidate": init})
		}
	})
	pc.OnTrack(func(tr *webrtc.TrackRemote, _ *webrtc.RTPReceiver) {
		buf := make([]byte, 1500)
		for i := 0; i < 5; i++ { // almeno 5 pacchetti veri, non solo la negoziazione
			if _, _, err := tr.Read(buf); err != nil {
				return
			}
		}
		f.received <- tr.StreamID()
		_, _ = io.Copy(io.Discard, readerFunc(func(b []byte) (int, error) { n, _, err := tr.Read(b); return n, err }))
	})
	t.Cleanup(func() { ws.Close(); pc.Close() })

	go f.loop()
	f.write(map[string]any{"type": "join", "name": name})
	go f.speak()
	return f
}

type readerFunc func([]byte) (int, error)

func (r readerFunc) Read(b []byte) (int, error) { return r(b) }

func (f *fakePhone) write(v any) {
	f.wmu.Lock()
	defer f.wmu.Unlock()
	_ = f.ws.WriteJSON(v)
}

func (f *fakePhone) loop() {
	for {
		var m sfu.Message
		if err := f.ws.ReadJSON(&m); err != nil {
			return
		}
		switch m.Type {
		case "welcome":
			f.id <- m.ID
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

func waitID(t *testing.T, f *fakePhone) string {
	t.Helper()
	select {
	case id := <-f.id:
		return id
	case <-time.After(5 * time.Second):
		t.Fatal("nessun benvenuto dal server")
		return ""
	}
}

func waitAudioFrom(t *testing.T, f *fakePhone, from ...string) {
	t.Helper()
	missing := map[string]bool{}
	for _, id := range from {
		missing[id] = true
	}
	deadline := time.After(15 * time.Second)
	for len(missing) > 0 {
		select {
		case got := <-f.received:
			delete(missing, got)
		case <-deadline:
			t.Fatalf("nessun audio ricevuto da %v", missing)
		}
	}
}

// Due telefoni: ognuno deve sentire l'altro, passando dal server di regia.
func TestTwoPhonesHearEachOther(t *testing.T) {
	ts, room := startServer(t)

	a := newFakePhone(t, ts, "Marco")
	idA := waitID(t, a)
	b := newFakePhone(t, ts, "Giulia")
	idB := waitID(t, b)

	waitAudioFrom(t, b, idA)
	waitAudioFrom(t, a, idB)

	if n := len(room.Peers()); n != 2 {
		t.Fatalf("attesi 2 partecipanti, trovati %d", n)
	}

	// Un terzo che entra dopo deve sentire entrambi.
	c := newFakePhone(t, ts, "Luca")
	waitID(t, c)
	waitAudioFrom(t, c, idA, idB)

	// Chi esce sparisce dalla lista.
	c.ws.Close()
	deadline := time.Now().Add(5 * time.Second)
	for len(room.Peers()) != 2 {
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
		"/":              "Intercom di produzione",
		"/regia":         "Console di regia",
		"/aiuto":         "https://127.0.0.1:8443/",
		"/ca.crt":        "BEGIN CERTIFICATE",
		"/static/app.js": "getUserMedia",
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
	resp, err := client.Get(ts.URL + "/qr/app.png")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.Header.Get("Content-Type") != "image/png" {
		t.Fatalf("QR: content-type %q", resp.Header.Get("Content-Type"))
	}
}
