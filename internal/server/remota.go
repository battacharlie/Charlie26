package server

// Regia remota: uno o più smartphone abbinati alla console hanno gli stessi
// poteri della macchina di regia (secondo SupAdmin), mentre la console sul
// PC resta attiva. L'abbinamento parte dalla console: mostra un QR con un
// codice che vale pochi minuti e una sola volta; il telefono che lo usa
// riceve un cookie personale, revocabile in qualsiasi momento.

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/gorilla/websocket"
)

const (
	remoteCookie   = "c26regia"
	pairingTTL     = 5 * time.Minute
	remoteMaxAge   = 365 * 24 * time.Hour
	maxRemoteDevs  = 16
	maxPendingPair = 8
)

// RemoteDevice è un telefono abbinato come regia.
type RemoteDevice struct {
	ID        string    `json:"id"`
	Name      string    `json:"nome"`
	TokenHash string    `json:"impronta"` // sha256 del cookie: il cookie vero non si salva
	Paired    time.Time `json:"abbinato"`
	LastSeen  time.Time `json:"visto"`
}

type remoteStore struct {
	mu      sync.Mutex
	file    string // vuoto = solo in memoria
	devices []RemoteDevice
	pending map[string]time.Time // codice di abbinamento → scadenza
	conns   map[string]map[*websocket.Conn]struct{}
}

func newRemoteStore(file string) *remoteStore {
	st := &remoteStore{file: file, pending: map[string]time.Time{}, conns: map[string]map[*websocket.Conn]struct{}{}}
	if file != "" {
		if b, err := os.ReadFile(file); err == nil {
			_ = json.Unmarshal(b, &st.devices)
		}
	}
	return st
}

func (st *remoteStore) saveLocked() {
	if st.file == "" {
		return
	}
	b, _ := json.MarshalIndent(st.devices, "", "  ")
	_ = os.MkdirAll(filepath.Dir(st.file), 0o700)
	tmp := st.file + ".tmp"
	if os.WriteFile(tmp, b, 0o600) == nil {
		_ = os.Rename(tmp, st.file)
	}
}

func randomHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func tokenHash(t string) string {
	h := sha256.Sum256([]byte(t))
	return hex.EncodeToString(h[:])
}

// newCode crea un codice di abbinamento monouso.
func (st *remoteStore) newCode() (string, time.Time) {
	st.mu.Lock()
	defer st.mu.Unlock()
	now := time.Now()
	for c, exp := range st.pending {
		if now.After(exp) {
			delete(st.pending, c)
		}
	}
	// Non accumulare codici: tiene solo i più recenti.
	for len(st.pending) >= maxPendingPair {
		var oldest string
		for c, exp := range st.pending {
			if oldest == "" || exp.Before(st.pending[oldest]) {
				oldest = c
			}
		}
		delete(st.pending, oldest)
	}
	code := randomHex(16)
	exp := now.Add(pairingTTL)
	st.pending[code] = exp
	return code, exp
}

// pair consuma il codice e crea il dispositivo; restituisce il cookie.
func (st *remoteStore) pair(code, name string) (token string, dev RemoteDevice, ok bool) {
	st.mu.Lock()
	defer st.mu.Unlock()
	exp, found := st.pending[code]
	if !found || time.Now().After(exp) {
		return "", dev, false
	}
	delete(st.pending, code)
	if len(st.devices) >= maxRemoteDevs {
		// il più vecchio lascia il posto
		sort.Slice(st.devices, func(i, j int) bool { return st.devices[i].LastSeen.Before(st.devices[j].LastSeen) })
		st.devices = st.devices[1:]
	}
	token = randomHex(32)
	dev = RemoteDevice{ID: randomHex(6), Name: name, TokenHash: tokenHash(token), Paired: time.Now(), LastSeen: time.Now()}
	st.devices = append(st.devices, dev)
	st.saveLocked()
	return token, dev, true
}

// check verifica il cookie e aggiorna "visto".
func (st *remoteStore) check(token string) (RemoteDevice, bool) {
	if token == "" {
		return RemoteDevice{}, false
	}
	h := tokenHash(token)
	st.mu.Lock()
	defer st.mu.Unlock()
	for i := range st.devices {
		if subtle.ConstantTimeCompare([]byte(st.devices[i].TokenHash), []byte(h)) == 1 {
			if time.Since(st.devices[i].LastSeen) > time.Minute {
				st.devices[i].LastSeen = time.Now()
				st.saveLocked()
			}
			return st.devices[i], true
		}
	}
	return RemoteDevice{}, false
}

func (st *remoteStore) list() []RemoteDevice {
	st.mu.Lock()
	defer st.mu.Unlock()
	out := make([]RemoteDevice, len(st.devices))
	copy(out, st.devices)
	return out
}

// revoke elimina il dispositivo e chiude i suoi collegamenti aperti.
func (st *remoteStore) revoke(id string) bool {
	st.mu.Lock()
	found := false
	for i, d := range st.devices {
		if d.ID == id {
			st.devices = append(st.devices[:i], st.devices[i+1:]...)
			found = true
			break
		}
	}
	var conns []*websocket.Conn
	for c := range st.conns[id] {
		conns = append(conns, c)
	}
	delete(st.conns, id)
	if found {
		st.saveLocked()
	}
	st.mu.Unlock()
	for _, c := range conns {
		_ = c.Close()
	}
	return found
}

func (st *remoteStore) track(id string, c *websocket.Conn) func() {
	st.mu.Lock()
	if st.conns[id] == nil {
		st.conns[id] = map[*websocket.Conn]struct{}{}
	}
	st.conns[id][c] = struct{}{}
	st.mu.Unlock()
	return func() {
		st.mu.Lock()
		delete(st.conns[id], c)
		st.mu.Unlock()
	}
}

// remoteDevice: il dispositivo abbinato che fa la richiesta, se c'è.
func (s *Server) remoteDevice(r *http.Request) (RemoteDevice, bool) {
	c, err := r.Cookie(remoteCookie)
	if err != nil {
		return RemoteDevice{}, false
	}
	return s.remote.check(c.Value)
}

// isRegia: macchina di regia oppure telefono abbinato.
func (s *Server) isRegia(r *http.Request) bool {
	if !s.cfg.Public && isLoopback(r.RemoteAddr) {
		return true
	}
	_, ok := s.remoteDevice(r)
	return ok
}

// PinCA è l'impronta (sha256 della CA, base64url) che i QR portano con sé:
// l'app si fida del certificato dell'evento solo se combacia.
func (s *Server) PinCA() string {
	if len(s.cfg.CAPEM) == 0 {
		return ""
	}
	blk, _ := pem.Decode(s.cfg.CAPEM)
	if blk == nil {
		return ""
	}
	if _, err := x509.ParseCertificate(blk.Bytes); err != nil {
		return ""
	}
	h := sha256.Sum256(blk.Bytes)
	return base64.RawURLEncoding.EncodeToString(h[:])
}

// withPin aggiunge l'impronta della CA al frammento (#…) di un indirizzo.
func (s *Server) withPin(u string) string {
	pin := s.PinCA()
	if pin == "" {
		return u
	}
	if strings.Contains(u, "#") {
		return u + "&ca=" + pin
	}
	return u + "#ca=" + pin
}

// pairURL: indirizzo che il telefono apre per abbinarsi.
func (s *Server) pairURL(code string) string {
	return s.withPin(s.AppURL() + "regia#abbina=" + code)
}

// handlePairStart (solo regia): nuovo codice di abbinamento.
func (s *Server) handlePairStart(w http.ResponseWriter, r *http.Request) {
	code, exp := s.remote.newCode()
	writeJSON(w, http.StatusOK, map[string]any{"codice": code, "url": s.pairURL(code), "scade": exp})
}

// handlePairQR (solo regia): il QR del codice.
func (s *Server) handlePairQR(w http.ResponseWriter, r *http.Request) {
	code := r.URL.Query().Get("codice")
	if len(code) != 32 {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeQR(w, s.pairURL(code))
}

// handlePair: il telefono consuma il codice e diventa regia.
func (s *Server) handlePair(w http.ResponseWriter, r *http.Request) {
	if !sameOrigin(r) {
		http.Error(w, "richiesta non permessa", http.StatusForbidden)
		return
	}
	var req struct{ Codice, Nome string }
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "richiesta non valida"})
		return
	}
	name := strings.TrimSpace(req.Nome)
	if name == "" || !utf8.ValidString(name) {
		name = "Telefono regia"
	}
	if utf8.RuneCountInString(name) > 32 {
		name = string([]rune(name)[:32])
	}
	token, dev, ok := s.remote.pair(req.Codice, name)
	if !ok {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "Codice scaduto o già usato: dalla console premi di nuovo \"Abbina telefono regia\"."})
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name: remoteCookie, Value: token, Path: "/",
		MaxAge: int(remoteMaxAge / time.Second), HttpOnly: true, Secure: true, SameSite: http.SameSiteStrictMode,
	})
	log.Printf("regia remota: abbinato \"%s\" da %s", dev.Name, r.RemoteAddr)
	writeJSON(w, http.StatusOK, map[string]string{"ok": "abbinato", "nome": dev.Name})
}

// handleDevices (solo regia): elenco e revoca dei telefoni abbinati.
func (s *Server) handleDevices(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPost {
		var req struct{ Op, ID string }
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&req); err != nil || req.Op != "revoca" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "richiesta non valida"})
			return
		}
		if !s.remote.revoke(req.ID) {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "dispositivo non trovato"})
			return
		}
		log.Printf("regia remota: revocato %s", req.ID)
	}
	me, _ := s.remoteDevice(r)
	type row struct {
		RemoteDevice
		Io bool `json:"io"`
	}
	out := []row{}
	for _, d := range s.remote.list() {
		d.TokenHash = ""
		out = append(out, row{d, d.ID == me.ID})
	}
	writeJSON(w, http.StatusOK, out)
}

// handleRemoteLogout: il telefono si scollega da solo ("Chiudi" dalla app).
func (s *Server) handleRemoteLogout(w http.ResponseWriter, r *http.Request) {
	if !sameOrigin(r) {
		http.Error(w, "richiesta non permessa", http.StatusForbidden)
		return
	}
	if d, ok := s.remoteDevice(r); ok {
		s.remote.revoke(d.ID)
	}
	http.SetCookie(w, &http.Cookie{Name: remoteCookie, Value: "", Path: "/", MaxAge: -1, HttpOnly: true, Secure: true, SameSite: http.SameSiteStrictMode})
	writeJSON(w, http.StatusOK, map[string]string{"ok": "scollegato"})
}
