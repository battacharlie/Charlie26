// Package server espone le pagine web, il signaling WebSocket e i QR code.
package server

import (
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io/fs"
	"log"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/pion/webrtc/v4"
	qrcode "github.com/skip2/go-qrcode"

	"github.com/battacharlie/charlie26/internal/netinfo"
	"github.com/battacharlie/charlie26/internal/sfu"
)

// Config del server web.
type Config struct {
	Host      string // IP o nome a cui puntano i QR code
	HTTPSPort int
	HTTPPort  int    // porta in chiaro: solo pagina di aiuto e download della CA
	CertMode  string // "locale" oppure "dominio"
	CAPEM     []byte // CA da far installare ai telefoni (solo modalità locale)
	Web       fs.FS
	Room      *sfu.Room
	IPs       []netinfo.Address
}

// Server raggruppa le due porte (HTTPS e HTTP di aiuto).
type Server struct {
	cfg      Config
	upgrader websocket.Upgrader

	rnnoiseOnce sync.Once
	rnnoise     []byte
}

// New crea il server.
func New(cfg Config) *Server {
	return &Server{cfg: cfg}
}

// AppURL è l'indirizzo che i tecnici aprono dal telefono.
func (s *Server) AppURL() string {
	return fmt.Sprintf("https://%s%s/", s.cfg.Host, portSuffix(s.cfg.HTTPSPort, 443))
}

// HelpURL è la pagina di aiuto in HTTP semplice, apribile anche prima di fidarsi del certificato.
func (s *Server) HelpURL() string {
	return fmt.Sprintf("http://%s%s/aiuto", s.cfg.Host, portSuffix(s.cfg.HTTPPort, 80))
}

// ConsoleURL è l'indirizzo della console sulla macchina di regia.
func (s *Server) ConsoleURL() string {
	return fmt.Sprintf("https://localhost%s/regia", portSuffix(s.cfg.HTTPSPort, 443))
}

func portSuffix(p, def int) string {
	if p == def {
		return ""
	}
	return ":" + strconv.Itoa(p)
}

// HTTPSHandler gestisce tutte le pagine sulla porta sicura.
func (s *Server) HTTPSHandler() http.Handler {
	mux := http.NewServeMux()
	s.common(mux)
	mux.HandleFunc("GET /ws", s.handleWS)
	mux.HandleFunc("GET /api/info", s.handleInfo)
	mux.HandleFunc("GET /regia", s.page("regia.html"))
	mux.HandleFunc("GET /{$}", s.page("index.html"))
	mux.HandleFunc("GET /static/rnnoise-worklet.js", s.handleRNNoise)
	mux.Handle("GET /static/", http.StripPrefix("/static/", http.FileServerFS(s.cfg.Web)))
	return securityHeaders(mux)
}

// HTTPHandler è la porta in chiaro: aiuto, CA e rinvio verso l'HTTPS.
func (s *Server) HTTPHandler() http.Handler {
	mux := http.NewServeMux()
	s.common(mux)
	mux.Handle("GET /static/", http.StripPrefix("/static/", http.FileServerFS(s.cfg.Web)))
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		host := hostOnly(r.Host)
		if host == "" {
			host = s.cfg.Host
		}
		target := fmt.Sprintf("https://%s%s%s", host, portSuffix(s.cfg.HTTPSPort, 443), r.URL.RequestURI())
		http.Redirect(w, r, target, http.StatusFound)
	})
	return mux
}

func (s *Server) common(mux *http.ServeMux) {
	mux.HandleFunc("GET /aiuto", s.page("aiuto.html"))
	mux.HandleFunc("GET /ca.crt", s.handleCA)
	mux.HandleFunc("GET /qr/{name}", s.handleQR)
}

func (s *Server) page(name string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		b, err := fs.ReadFile(s.cfg.Web, name)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		page := strings.NewReplacer(
			"{{APP_URL}}", s.AppURL(),
			"{{HELP_URL}}", s.HelpURL(),
			"{{CERT_MODE}}", s.cfg.CertMode,
		).Replace(string(b))
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-cache")
		_, _ = w.Write([]byte(page))
	}
}

func (s *Server) handleCA(w http.ResponseWriter, r *http.Request) {
	if len(s.cfg.CAPEM) == 0 {
		http.Error(w, "Con il certificato di dominio non serve installare nulla.", http.StatusNotFound)
		return
	}
	// Questo tipo MIME fa aprire a iOS e Android la procedura di installazione.
	w.Header().Set("Content-Type", "application/x-x509-ca-cert")
	w.Header().Set("Content-Disposition", `attachment; filename="Intercom-Regia-CA.crt"`)
	_, _ = w.Write(s.cfg.CAPEM)
}

func (s *Server) handleQR(w http.ResponseWriter, r *http.Request) {
	var text string
	switch r.PathValue("name") {
	case "app.png":
		text = s.AppURL()
	case "aiuto.png":
		text = s.HelpURL()
	default:
		http.NotFound(w, r)
		return
	}
	png, err := qrcode.Encode(text, qrcode.Medium, 512)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("Cache-Control", "no-cache")
	_, _ = w.Write(png)
}

func (s *Server) handleInfo(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"appURL":   s.AppURL(),
		"helpURL":  s.HelpURL(),
		"certMode": s.cfg.CertMode,
		"host":     s.cfg.Host,
		"ips":      s.cfg.IPs,
		"peers":    s.cfg.Room.Peers(),
		"channels": s.cfg.Room.Channels(),
	})
}

// handleRNNoise unisce la libreria RNNoise e il nostro AudioWorklet in un
// unico modulo: dentro un AudioWorklet non tutti i browser permettono import.
func (s *Server) handleRNNoise(w http.ResponseWriter, r *http.Request) {
	s.rnnoiseOnce.Do(func() {
		lib, err1 := fs.ReadFile(s.cfg.Web, "rnnoise/rnnoise-sync.js")
		proc, err2 := fs.ReadFile(s.cfg.Web, "rnnoise/processor.js")
		if err1 != nil || err2 != nil {
			return
		}
		code := strings.Replace(string(lib), "export default createRNNWasmModuleSync;", "", 1)
		s.rnnoise = []byte(code + "\n" + string(proc))
	})
	if s.rnnoise == nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
	w.Header().Set("Cache-Control", "max-age=86400") // 1,9 MB: lo si scarica una volta al giorno
	_, _ = w.Write(s.rnnoise)
}

// clientMsg è un messaggio dal browser.
type clientMsg struct {
	Type      string                   `json:"type"`
	Name      string                   `json:"name"`
	Role      string                   `json:"role"`
	Channel   string                   `json:"channel"`
	Channels  []string                 `json:"channels"`
	SDP       string                   `json:"sdp"`
	Candidate *webrtc.ICECandidateInit `json:"candidate"`
	On        bool                     `json:"on"`
}

const (
	pingEvery = 10 * time.Second
	readLimit = 30 * time.Second // un telefono che non risponde per 30 s viene scollegato
)

func (s *Server) handleWS(w http.ResponseWriter, r *http.Request) {
	conn, err := s.upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer conn.Close()
	conn.SetReadLimit(64 << 10)

	out := make(chan sfu.Message, 64)
	done := make(chan struct{})
	defer close(done)

	// Scrittore: unico goroutine che scrive sul WebSocket.
	go func() {
		t := time.NewTicker(pingEvery)
		defer t.Stop()
		for {
			select {
			case <-done:
				return
			case m := <-out:
				_ = conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
				if err := conn.WriteJSON(m); err != nil {
					conn.Close()
					return
				}
			case <-t.C:
				_ = conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
				if err := conn.WriteMessage(websocket.PingMessage, nil); err != nil {
					conn.Close()
					return
				}
			}
		}
	}()
	send := func(m sfu.Message) {
		select {
		case out <- m:
		case <-done:
		default:
			// Client troppo lento: meglio scollegarlo che bloccare tutti.
			conn.Close()
		}
	}

	_ = conn.SetReadDeadline(time.Now().Add(readLimit))
	conn.SetPongHandler(func(string) error { return conn.SetReadDeadline(time.Now().Add(readLimit)) })

	var peer *sfu.Peer
	defer func() {
		if peer != nil {
			peer.Close()
		}
	}()

	for {
		var m clientMsg
		if err := conn.ReadJSON(&m); err != nil {
			return
		}
		_ = conn.SetReadDeadline(time.Now().Add(readLimit))
		switch m.Type {
		case "join":
			if peer != nil {
				continue
			}
			// Fino alla Fase 3 (login) la regia può entrare solo dalla macchina di regia.
			if m.Role == sfu.RoleRegia && !isLoopback(r.RemoteAddr) {
				send(sfu.Message{Type: "error", Error: "il ruolo regia è disponibile solo sul computer di regia"})
				return
			}
			peer, err = s.cfg.Room.Join(sfu.JoinOptions{Name: cleanName(m.Name), Role: m.Role, Channel: m.Channel}, send)
			if err != nil {
				send(sfu.Message{Type: "error", Error: "impossibile entrare: " + err.Error()})
				return
			}
			log.Printf("[%s %s] entrato come %s da %s", peer.ID, peer.Name, peer.Role, r.RemoteAddr)
		case "answer":
			if peer != nil {
				if err := peer.HandleAnswer(m.SDP); err != nil {
					log.Printf("[%s] risposta SDP non valida: %v", peer.ID, err)
				}
			}
		case "candidate":
			if peer != nil && m.Candidate != nil {
				_ = peer.AddCandidate(*m.Candidate)
			}
		case "talk":
			if peer != nil {
				peer.SetTalk(m.Channels)
			}
		case "listen":
			if peer != nil {
				peer.SetListen(m.Channel, m.On)
			}
		case "ping":
			// tiene vivo il collegamento anche dove i ping WebSocket non arrivano
		}
	}
}

func cleanName(n string) string {
	n = strings.TrimSpace(n)
	if n == "" {
		return "Senza nome"
	}
	r := []rune(n)
	if len(r) > 24 {
		r = r[:24]
	}
	return string(r)
}

func isLoopback(addr string) bool {
	ip := net.ParseIP(hostOnly(addr))
	return ip != nil && ip.IsLoopback()
}

func hostOnly(h string) string {
	if host, _, err := net.SplitHostPort(h); err == nil {
		return host
	}
	return h
}

func securityHeaders(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Permissions-Policy", "microphone=(self), camera=(self)")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		h.ServeHTTP(w, r)
	})
}

// TLSConfig con un certificato fisso.
func TLSConfig(cert tls.Certificate) *tls.Config {
	return &tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS12}
}
