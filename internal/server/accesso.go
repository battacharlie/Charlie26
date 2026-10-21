package server

// Accesso alla regia da internet (modalità server, per esempio su un VPS).
//
// In rete locale la macchina di regia si riconosce perché apre la console da
// localhost. Su un server pubblico tutti arrivano "da fuori", quindi la regia
// entra con una password (scelta dall'amministratore all'avvio) e riceve lo
// stesso cookie personale dei telefoni abbinati: da lì in poi è identica a
// un telefono di regia, revocabile dalla console.

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

const (
	loginMaxFails  = 5                // tentativi sbagliati consentiti…
	loginFailSpan  = 15 * time.Minute // …in questo intervallo, per ogni indirizzo
	loginRemoteAge = 7 * 24 * time.Hour
)

// loginLimiter conta i tentativi sbagliati per indirizzo IP.
type loginLimiter struct {
	mu    sync.Mutex
	fails map[string][]time.Time
}

func newLoginLimiter() *loginLimiter { return &loginLimiter{fails: map[string][]time.Time{}} }

// blocked dice se l'indirizzo ha esaurito i tentativi.
func (l *loginLimiter) blocked(ip string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.recentLocked(ip)) >= loginMaxFails
}

func (l *loginLimiter) fail(ip string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.fails[ip] = append(l.recentLocked(ip), time.Now())
	if len(l.fails) > 4096 { // non crescere senza limite sotto attacco
		for k, v := range l.fails {
			if len(l.pruneLocked(v)) == 0 {
				delete(l.fails, k)
			}
		}
	}
}

func (l *loginLimiter) reset(ip string) {
	l.mu.Lock()
	delete(l.fails, ip)
	l.mu.Unlock()
}

func (l *loginLimiter) recentLocked(ip string) []time.Time {
	v := l.pruneLocked(l.fails[ip])
	if len(v) == 0 {
		delete(l.fails, ip)
	} else {
		l.fails[ip] = v
	}
	return v
}

func (l *loginLimiter) pruneLocked(v []time.Time) []time.Time {
	cut := time.Now().Add(-loginFailSpan)
	out := v[:0:0]
	for _, t := range v {
		if t.After(cut) {
			out = append(out, t)
		}
	}
	return out
}

// passwordOK confronta senza rivelare nulla dal tempo di risposta.
func passwordOK(given, want string) bool {
	a, b := sha256.Sum256([]byte(given)), sha256.Sum256([]byte(want))
	return subtle.ConstantTimeCompare(a[:], b[:]) == 1
}

// addDevice crea un telefono/browser di regia senza codice di abbinamento
// (accesso con password) e restituisce il cookie.
func (st *remoteStore) addDevice(name string) (token string, dev RemoteDevice) {
	st.mu.Lock()
	defer st.mu.Unlock()
	if len(st.devices) >= maxRemoteDevs {
		// il più vecchio lascia il posto
		oldest := 0
		for i := range st.devices {
			if st.devices[i].LastSeen.Before(st.devices[oldest].LastSeen) {
				oldest = i
			}
		}
		st.devices = append(st.devices[:oldest], st.devices[oldest+1:]...)
	}
	token = randomHex(32)
	dev = RemoteDevice{ID: randomHex(6), Name: name, TokenHash: tokenHash(token), Paired: time.Now(), LastSeen: time.Now()}
	st.devices = append(st.devices, dev)
	st.saveLocked()
	return token, dev
}

// handleRegiaState dice alla pagina se qui serve la password della regia.
func (s *Server) handleRegiaState(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, map[string]any{
		"password": s.cfg.AdminPassword != "",
		"regia":    s.isRegia(r),
	})
}

// handleRegiaLogin: la regia entra con la password.
func (s *Server) handleRegiaLogin(w http.ResponseWriter, r *http.Request) {
	if s.cfg.AdminPassword == "" {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "accesso con password non attivo"})
		return
	}
	if !sameOrigin(r) {
		http.Error(w, "richiesta non permessa", http.StatusForbidden)
		return
	}
	ip := hostOnly(r.RemoteAddr)
	if s.logins.blocked(ip) {
		w.Header().Set("Retry-After", "900")
		writeJSON(w, http.StatusTooManyRequests, map[string]string{"error": "Troppi tentativi sbagliati: riprova tra 15 minuti."})
		return
	}
	var req struct{ Password, Nome string }
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "richiesta non valida"})
		return
	}
	if !passwordOK(req.Password, s.cfg.AdminPassword) {
		s.logins.fail(ip)
		log.Printf("regia: password errata da %s", ip)
		time.Sleep(700 * time.Millisecond) // rallenta chi prova a caso
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "Password errata."})
		return
	}
	s.logins.reset(ip)
	name := strings.TrimSpace(req.Nome)
	if name == "" || !utf8.ValidString(name) {
		name = "Regia (password)"
	}
	if utf8.RuneCountInString(name) > 32 {
		name = string([]rune(name)[:32])
	}
	token, dev := s.remote.addDevice(name)
	http.SetCookie(w, &http.Cookie{
		Name: remoteCookie, Value: token, Path: "/",
		MaxAge: int(loginRemoteAge / time.Second), HttpOnly: true, Secure: true, SameSite: http.SameSiteStrictMode,
	})
	log.Printf("regia: accesso con password di \"%s\" da %s", dev.Name, ip)
	writeJSON(w, http.StatusOK, map[string]string{"ok": "entrato", "nome": dev.Name})
}
