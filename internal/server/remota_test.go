package server

import (
	"bytes"
	"crypto/tls"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gorilla/websocket"

	"github.com/battacharlie/charlie26/internal/certs"
	"github.com/battacharlie/charlie26/internal/evento"
	"github.com/battacharlie/charlie26/internal/sfu"
	"github.com/battacharlie/charlie26/web"
)

// Le richieste con l'intestazione X-Finto-IP arrivano "da un telefono in rete"
// invece che dalla macchina di regia.
func fakeRemote(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if ip := r.Header.Get("X-Finto-IP"); ip != "" {
			r.RemoteAddr = ip + ":40000"
		}
		h.ServeHTTP(w, r)
	})
}

func TestRegiaRemota(t *testing.T) {
	room, err := sfu.NewRoom(sfu.Config{UDPPort: freeUDPPort(t), Event: evento.New("Produzione", []string{"Video"})})
	if err != nil {
		t.Fatal(err)
	}
	local, err := certs.EnsureLocal(t.TempDir(), []string{"127.0.0.1"})
	if err != nil {
		t.Fatal(err)
	}
	file := t.TempDir() + "/regia-remota.json"
	srv := New(Config{Host: "127.0.0.1", HTTPSPort: 8443, CertMode: "locale", CAPEM: local.CAPEM, Web: web.FS, Room: room, RemoteFile: file})
	ts := httptest.NewUnstartedServer(fakeRemote(srv.HTTPSHandler()))
	ts.TLS = TLSConfig(local.Server)
	ts.StartTLS()
	t.Cleanup(func() { ts.Close(); room.Close() })

	do := func(method, path, cookie string, remote bool, body any) (*http.Response, map[string]any) {
		t.Helper()
		var rd *bytes.Reader
		if body != nil {
			b, _ := json.Marshal(body)
			rd = bytes.NewReader(b)
		} else {
			rd = bytes.NewReader(nil)
		}
		req, _ := http.NewRequest(method, ts.URL+path, rd)
		req.Header.Set("Content-Type", "application/json")
		if remote {
			req.Header.Set("X-Finto-IP", "192.0.2.50")
		}
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

	// Un telefono qualsiasi non è regia.
	if resp, _ := do("GET", "/api/regia/evento", "", true, nil); resp.StatusCode != http.StatusForbidden {
		t.Fatalf("telefono non abbinato: atteso 403, avuto %d", resp.StatusCode)
	}
	// Un telefono non può generare codici da solo.
	if resp, _ := do("POST", "/api/regia/abbina", "", true, map[string]any{}); resp.StatusCode != http.StatusForbidden {
		t.Fatalf("abbinamento da telefono non abbinato: atteso 403, avuto %d", resp.StatusCode)
	}

	// La console crea il codice; il QR contiene l'impronta della CA.
	_, start := do("POST", "/api/regia/abbina", "", false, map[string]any{})
	code, _ := start["codice"].(string)
	pairURL, _ := start["url"].(string)
	if len(code) != 32 || !strings.Contains(pairURL, "regia#abbina="+code+"&ca="+srv.PinCA()) || srv.PinCA() == "" {
		t.Fatalf("codice/indirizzo di abbinamento: %v", start)
	}

	// Codice sbagliato: niente.
	if resp, _ := do("POST", "/api/regia-remota/abbina", "", true, map[string]any{"codice": strings.Repeat("0", 32)}); resp.StatusCode != http.StatusForbidden {
		t.Fatalf("codice inventato: atteso 403, avuto %d", resp.StatusCode)
	}
	// Codice giusto: arriva il cookie.
	resp, _ := do("POST", "/api/regia-remota/abbina", "", true, map[string]any{"codice": code, "nome": "Telefono Andrea"})
	var cookie string
	for _, c := range resp.Cookies() {
		if c.Name == remoteCookie {
			cookie = c.Value
			if !c.HttpOnly || !c.Secure || c.SameSite != http.SameSiteStrictMode {
				t.Fatalf("cookie poco protetto: %+v", c)
			}
		}
	}
	if resp.StatusCode != 200 || cookie == "" {
		t.Fatalf("abbinamento: %d, cookie %q", resp.StatusCode, cookie)
	}
	// Il codice vale una volta sola.
	if resp, _ := do("POST", "/api/regia-remota/abbina", "", true, map[string]any{"codice": code}); resp.StatusCode != http.StatusForbidden {
		t.Fatalf("codice riusato: atteso 403, avuto %d", resp.StatusCode)
	}
	// Con il cookie il telefono comanda come la regia.
	if resp, _ := do("GET", "/api/regia/evento", cookie, true, nil); resp.StatusCode != 200 {
		t.Fatalf("telefono abbinato: atteso 200, avuto %d", resp.StatusCode)
	}
	if resp, _ := do("POST", "/api/regia/evento", cookie, true, map[string]any{"op": "name", "name": "Dal telefono"}); resp.StatusCode != 200 {
		t.Fatalf("modifica dal telefono: %d", resp.StatusCode)
	}

	// Pannello intercom della regia dal telefono, mentre il PC resta regia.
	dialer := websocket.Dialer{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}
	wsURL := "wss" + strings.TrimPrefix(ts.URL, "https") + "/ws"
	join := func(cookie string, remote bool) (*websocket.Conn, sfu.Message) {
		h := http.Header{}
		if remote {
			h.Set("X-Finto-IP", "192.0.2.50")
		}
		if cookie != "" {
			h.Set("Cookie", remoteCookie+"="+cookie)
		}
		ws, _, err := dialer.Dial(wsURL, h)
		if err != nil {
			t.Fatal(err)
		}
		_ = ws.WriteJSON(map[string]any{"type": "join", "name": "Regia", "regia": true})
		for {
			var m sfu.Message
			if err := ws.ReadJSON(&m); err != nil {
				return ws, sfu.Message{Type: "chiuso"}
			}
			if m.Type == "welcome" || m.Type == "error" {
				return ws, m
			}
		}
	}
	pc, m := join("", false)
	defer pc.Close()
	if m.Type != "welcome" || m.Me.Role != sfu.RoleRegia {
		t.Fatalf("regia dal PC: %+v", m)
	}
	phone, m := join(cookie, true)
	if m.Type != "welcome" || m.Me.Role != sfu.RoleRegia {
		t.Fatalf("regia dal telefono abbinato: %+v", m)
	}
	if _, m := join("", true); m.Type != "error" {
		t.Fatalf("regia da telefono non abbinato deve essere rifiutata: %+v", m)
	}

	// Elenco dispositivi visto dal telefono: c'è lui, segnato come "io".
	req, _ := http.NewRequest("GET", ts.URL+"/api/regia/dispositivi", nil)
	req.Header.Set("X-Finto-IP", "192.0.2.50")
	req.AddCookie(&http.Cookie{Name: remoteCookie, Value: cookie})
	r2, err := ts.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	var devs []map[string]any
	_ = json.NewDecoder(r2.Body).Decode(&devs)
	r2.Body.Close()
	if len(devs) != 1 || devs[0]["nome"] != "Telefono Andrea" || devs[0]["io"] != true || devs[0]["impronta"] != "" && devs[0]["impronta"] != nil {
		t.Fatalf("dispositivi: %+v", devs)
	}

	// Riavvio del programma: il telefono resta abbinato.
	if _, ok := newRemoteStore(file).check(cookie); !ok {
		t.Fatal("dopo il riavvio il telefono deve restare abbinato")
	}

	// La console revoca: il collegamento del telefono si chiude e il cookie non vale più.
	id, _ := devs[0]["id"].(string)
	if resp, _ := do("POST", "/api/regia/dispositivi", "", false, map[string]any{"op": "revoca", "id": id}); resp.StatusCode != 200 {
		t.Fatalf("revoca: %d", resp.StatusCode)
	}
	for {
		var x sfu.Message
		if err := phone.ReadJSON(&x); err != nil {
			break // chiuso dal server
		}
	}
	if resp, _ := do("GET", "/api/regia/evento", cookie, true, nil); resp.StatusCode != http.StatusForbidden {
		t.Fatalf("dopo la revoca: atteso 403, avuto %d", resp.StatusCode)
	}
}
