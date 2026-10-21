package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/battacharlie/charlie26/internal/certs"
	"github.com/battacharlie/charlie26/internal/evento"
	"github.com/battacharlie/charlie26/internal/sfu"
	"github.com/battacharlie/charlie26/web"
)

// startPublic avvia il server in modalità pubblica (VPS) con una password di regia.
func startPublic(t *testing.T, password string) *httptest.Server {
	t.Helper()
	room, err := sfu.NewRoom(sfu.Config{UDPPort: freeUDPPort(t), Event: evento.New("Produzione", []string{"Video"})})
	if err != nil {
		t.Fatal(err)
	}
	local, err := certs.EnsureLocal(t.TempDir(), []string{"127.0.0.1"})
	if err != nil {
		t.Fatal(err)
	}
	srv := New(Config{
		Host: "intercom.example.org", HTTPSPort: 443, CertMode: "dominio", Web: web.FS, Room: room,
		RemoteFile: t.TempDir() + "/regia-remota.json", Public: true, AdminPassword: password,
	})
	ts := httptest.NewUnstartedServer(srv.HTTPSHandler())
	ts.TLS = TLSConfig(local.Server)
	ts.StartTLS()
	t.Cleanup(func() { ts.Close(); room.Close() })
	return ts
}

func call(t *testing.T, ts *httptest.Server, method, path, cookie string, body any) (*http.Response, map[string]any) {
	t.Helper()
	b, _ := json.Marshal(body)
	req, _ := http.NewRequest(method, ts.URL+path, bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	if cookie != "" {
		req.AddCookie(&http.Cookie{Name: remoteCookie, Value: cookie})
	}
	resp, err := ts.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var out map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&out)
	return resp, out
}

func TestModalitaPubblicaPassword(t *testing.T) {
	const pass = "una-password-molto-lunga"
	ts := startPublic(t, pass)

	// Anche da localhost (qui il client di prova lo è) la console non si apre senza password.
	if resp, _ := call(t, ts, "GET", "/api/regia/evento", "", nil); resp.StatusCode != http.StatusForbidden {
		t.Fatalf("senza password: atteso 403, avuto %d", resp.StatusCode)
	}
	// La pagina sa che qui serve la password.
	if _, st := call(t, ts, "GET", "/api/regia-remota/stato", "", nil); st["password"] != true || st["regia"] != false {
		t.Fatalf("stato: %v", st)
	}
	// Password sbagliata.
	if resp, _ := call(t, ts, "POST", "/api/regia-remota/entra", "", map[string]any{"password": "sbagliata"}); resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("password sbagliata: atteso 401, avuto %d", resp.StatusCode)
	}
	// Password giusta: arriva il cookie, protetto.
	resp, _ := call(t, ts, "POST", "/api/regia-remota/entra", "", map[string]any{"password": pass, "nome": "Portatile Andrea"})
	var cookie string
	for _, c := range resp.Cookies() {
		if c.Name == remoteCookie {
			cookie = c.Value
			if !c.HttpOnly || !c.Secure || c.SameSite != http.SameSiteStrictMode {
				t.Fatalf("cookie poco protetto: %+v", c)
			}
		}
	}
	if resp.StatusCode != http.StatusOK || cookie == "" {
		t.Fatalf("accesso con password: %d, cookie %q", resp.StatusCode, cookie)
	}
	// Con il cookie la console risponde, e il dispositivo compare nell'elenco.
	if resp, _ := call(t, ts, "GET", "/api/regia/evento", cookie, nil); resp.StatusCode != http.StatusOK {
		t.Fatalf("con cookie: atteso 200, avuto %d", resp.StatusCode)
	}
	if _, st := call(t, ts, "GET", "/api/regia-remota/stato", cookie, nil); st["regia"] != true {
		t.Fatalf("stato con cookie: %v", st)
	}
	// Il cookie si revoca come quello dei telefoni abbinati.
	if resp, _ := call(t, ts, "POST", "/api/regia-remota/esci", cookie, map[string]any{}); resp.StatusCode != http.StatusOK {
		t.Fatalf("uscita: %d", resp.StatusCode)
	}
	if resp, _ := call(t, ts, "GET", "/api/regia/evento", cookie, nil); resp.StatusCode != http.StatusForbidden {
		t.Fatalf("dopo l'uscita: atteso 403, avuto %d", resp.StatusCode)
	}
}

func TestAccessoPasswordDisattivato(t *testing.T) {
	ts := startPublic(t, "")
	if resp, _ := call(t, ts, "POST", "/api/regia-remota/entra", "", map[string]any{"password": ""}); resp.StatusCode != http.StatusNotFound {
		t.Fatalf("password non configurata: atteso 404, avuto %d", resp.StatusCode)
	}
}

func TestLimitatoreTentativi(t *testing.T) {
	l := newLoginLimiter()
	for i := 0; i < loginMaxFails-1; i++ {
		l.fail("192.0.2.1")
	}
	if l.blocked("192.0.2.1") {
		t.Fatal("bloccato troppo presto")
	}
	l.fail("192.0.2.1")
	if !l.blocked("192.0.2.1") {
		t.Fatal("dopo i tentativi massimi doveva bloccare")
	}
	if l.blocked("192.0.2.2") {
		t.Fatal("un altro indirizzo non deve essere bloccato")
	}
	// Tentativi vecchi non contano più.
	l.mu.Lock()
	old := time.Now().Add(-loginFailSpan - time.Minute)
	l.fails["192.0.2.1"] = []time.Time{old, old, old, old, old}
	l.mu.Unlock()
	if l.blocked("192.0.2.1") {
		t.Fatal("i tentativi scaduti devono essere dimenticati")
	}
	l.fail("192.0.2.3")
	l.reset("192.0.2.3")
	if l.blocked("192.0.2.3") || len(l.fails["192.0.2.3"]) != 0 {
		t.Fatal("reset non ha pulito")
	}
}

func TestBloccoDopoTroppiTentativi(t *testing.T) {
	ts := startPublic(t, "una-password-molto-lunga")
	// Ogni risposta sbagliata aspetta 0,7 s: il test dura qualche secondo.
	for i := 0; i < loginMaxFails; i++ {
		call(t, ts, "POST", "/api/regia-remota/entra", "", map[string]any{"password": "x"})
	}
	resp, _ := call(t, ts, "POST", "/api/regia-remota/entra", "", map[string]any{"password": "una-password-molto-lunga"})
	if resp.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("dopo %d errori: atteso 429, avuto %d", loginMaxFails, resp.StatusCode)
	}
}
