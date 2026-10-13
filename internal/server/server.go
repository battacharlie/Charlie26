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
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/pion/webrtc/v4"
	qrcode "github.com/skip2/go-qrcode"

	"github.com/battacharlie/charlie26/internal/immagini"
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
	Log       func() []string // ultime righe del registro (può essere nil)
	Shutdown  func()          // spegne il programma (può essere nil)

	// ChangeIP prepara il programma a usare un altro IP (certificato,
	// impostazioni salvate) e restituisce l'host da mettere nei QR.
	ChangeIP func(ip string) (host string, err error)
	// DetectIPs rileva di nuovo le schede di rete (può essere nil).
	DetectIPs func() []netinfo.Address
	// ModelsDir è la cartella dei modelli di evento (vuota = funzione non disponibile).
	ModelsDir string
	// Images è l'archivio delle immagini caricate (sfondi, icone).
	Images immagini.Store
	// Version è la versione del programma (mostrata in console).
	Version string
	// RemoteFile è il file dei telefoni abbinati come regia (vuoto = solo in memoria).
	RemoteFile string
}

// Server raggruppa le due porte (HTTPS e HTTP di aiuto).
type Server struct {
	cfg      Config
	upgrader websocket.Upgrader
	remote   *remoteStore

	hostMu sync.RWMutex
	host   string // IP o nome a cui puntano i QR (cambiabile dalla console)

	rnnoiseOnce sync.Once
	rnnoise     []byte
}

// New crea il server.
func New(cfg Config) *Server {
	return &Server{cfg: cfg, host: cfg.Host, remote: newRemoteStore(cfg.RemoteFile)}
}

// Host restituisce l'IP o il nome usato nei QR.
func (s *Server) Host() string {
	s.hostMu.RLock()
	defer s.hostMu.RUnlock()
	return s.host
}

func (s *Server) setHost(h string) {
	s.hostMu.Lock()
	s.host = h
	s.hostMu.Unlock()
}

// AppURL è l'indirizzo che i tecnici aprono dal telefono.
func (s *Server) AppURL() string {
	return fmt.Sprintf("https://%s%s/", s.Host(), portSuffix(s.cfg.HTTPSPort, 443))
}

// HelpURL è la pagina di aiuto in HTTP semplice, apribile anche prima di fidarsi del certificato.
func (s *Server) HelpURL() string {
	return fmt.Sprintf("http://%s%s/aiuto", s.Host(), portSuffix(s.cfg.HTTPPort, 80))
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
	mux.HandleFunc("GET /avatar/{id}", s.handleAvatar)
	mux.HandleFunc("GET /img/{name}", s.handleImage)
	mux.HandleFunc("POST /api/login", s.handleLogin)
	mux.HandleFunc("GET /api/regia/evento", s.onlyRegia(s.handleEventGet))
	mux.HandleFunc("POST /api/regia/evento", s.onlyRegia(s.handleEventChange))
	mux.HandleFunc("GET /api/regia/esporta", s.onlyRegia(s.handleExport))
	mux.HandleFunc("POST /api/regia/importa", s.onlyRegia(s.handleImport))
	mux.HandleFunc("GET /api/regia/registro", s.onlyRegia(s.handleLog))
	mux.HandleFunc("GET /api/regia/rete", s.onlyRegia(s.handleNetGet))
	mux.HandleFunc("GET /api/regia/whip", s.onlyRegia(s.handleWHIPInfo))
	mux.HandleFunc("POST /api/regia/aspetto", s.onlyRegia(s.handleLook))
	mux.HandleFunc("POST /api/regia/whip", s.onlyRegia(s.handleWHIPInfo))
	mux.HandleFunc("GET /api/regia/modelli", s.onlyRegia(s.handleModels))
	mux.HandleFunc("POST /api/regia/modelli", s.onlyRegia(s.handleModelOp))
	mux.HandleFunc("POST /api/regia/rete", s.onlyRegia(s.handleNetSet))
	mux.HandleFunc("POST /api/regia/spegni", s.onlyRegia(s.handleShutdown))
	mux.HandleFunc("POST /api/regia/abbina", s.onlyRegia(s.handlePairStart))
	mux.HandleFunc("GET /qr/abbina.png", s.onlyRegia(s.handlePairQR))
	mux.HandleFunc("GET /api/regia/dispositivi", s.onlyRegia(s.handleDevices))
	mux.HandleFunc("POST /api/regia/dispositivi", s.onlyRegia(s.handleDevices))
	mux.HandleFunc("POST /api/regia-remota/abbina", s.handlePair)
	mux.HandleFunc("POST /api/regia-remota/esci", s.handleRemoteLogout)
	mux.HandleFunc("GET /regia", s.page("regia.html"))
	mux.HandleFunc("GET /regia/qr", s.onlyRegia(s.page("qr.html")))
	mux.HandleFunc("GET /qr/utente/{user}", s.onlyRegia(s.handleUserQR))
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
			host = s.Host()
		}
		target := fmt.Sprintf("https://%s%s%s", host, portSuffix(s.cfg.HTTPSPort, 443), r.URL.RequestURI())
		http.Redirect(w, r, target, http.StatusFound)
	})
	return mux
}

func (s *Server) common(mux *http.ServeMux) {
	// WHIP (OBS) anche sulla porta in chiaro: OBS non si fida del certificato
	// locale; la chiave nell'intestazione protegge comunque l'accesso.
	mux.HandleFunc("POST /whip", s.handleWHIP)
	mux.HandleFunc("DELETE /whip/{id}", s.handleWHIPDelete)
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
			"{{VERSION}}", s.cfg.Version,
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
	w.Header().Set("Content-Disposition", `attachment; filename="CHARLIE26-CA.crt"`)
	_, _ = w.Write(s.cfg.CAPEM)
}

func (s *Server) handleQR(w http.ResponseWriter, r *http.Request) {
	var text string
	switch r.PathValue("name") {
	case "app.png":
		text = s.AppURL()
		// QR di un canale (libero accesso): la pagina lo propone già scelto.
		if ch := r.URL.Query().Get("canale"); ch != "" {
			if !s.channelExists(ch) {
				http.NotFound(w, r)
				return
			}
			text += "?canale=" + url.QueryEscape(ch)
		}
		text = s.withPin(text) // per l'app: impronta del certificato dell'evento
	case "aiuto.png":
		text = s.HelpURL()
	default:
		http.NotFound(w, r)
		return
	}
	writeQR(w, text)
}

// handleUserQR: QR personale con accesso diretto (solo dalla regia: contiene
// la chiave dell'utente).
func (s *Server) handleUserQR(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimSuffix(r.PathValue("user"), ".png")
	key, err := s.cfg.Room.UserQRKey(name, false)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeQR(w, s.withPin(s.AppURL()+"#accesso="+key))
}

func (s *Server) channelExists(id string) bool {
	for _, c := range s.cfg.Room.Channels() {
		if c.ID == id {
			return true
		}
	}
	return false
}

func writeQR(w http.ResponseWriter, text string) {
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
		"host":     s.Host(),
		"ips":      s.cfg.IPs,
		"peers":    s.cfg.Room.Peers(),
		"channels": s.cfg.Room.Channels(),
		"mode":     s.cfg.Room.Mode(),
		"look":     s.cfg.Room.Look(),
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

func (s *Server) handleAvatar(w http.ResponseWriter, r *http.Request) {
	img, mime, ok := s.cfg.Room.AvatarImage(r.PathValue("id"))
	if !ok {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", mime)
	w.Header().Set("Cache-Control", "max-age=86400") // l'indirizzo cambia a ogni nuova foto (?v=)
	w.Header().Set("Content-Security-Policy", "default-src 'none'")
	_, _ = w.Write(img)
}

func (s *Server) handleImage(w http.ResponseWriter, r *http.Request) {
	b, mime, err := s.cfg.Images.Read(r.PathValue("name"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", mime)
	w.Header().Set("Cache-Control", "max-age=31536000, immutable") // il nome dipende dal contenuto
	w.Header().Set("Content-Security-Policy", "default-src 'none'")
	_, _ = w.Write(b)
}

// clientMsg è un messaggio dal browser.
type clientMsg struct {
	Type        string                   `json:"type"`
	Name        string                   `json:"name"`
	Regia       bool                     `json:"regia"`
	Session     string                   `json:"session"`
	Channel     string                   `json:"channel"`
	Password    string                   `json:"password"`
	CreateName  string                   `json:"createName"`
	CreatePass  string                   `json:"createPassword"`
	OwnerTokens []string                 `json:"ownerTokens"`
	Channels    []string                 `json:"channels"`
	Peer        string                   `json:"peer"`
	MicMode     string                   `json:"micMode"`
	Video       bool                     `json:"video"`
	Avatar      string                   `json:"avatar"`
	Image       string                   `json:"image"`
	Stream      string                   `json:"stream"`
	Ref         string                   `json:"ref"`
	Label       string                   `json:"label"`
	Quality     string                   `json:"quality"`
	Icon        string                   `json:"icon"`
	IconData    string                   `json:"iconData"`
	Color       string                   `json:"color"`
	ImageData   string                   `json:"imageData"`
	ClearImage  bool                     `json:"clearImage"`
	SDP         string                   `json:"sdp"`
	Candidate   *webrtc.ICECandidateInit `json:"candidate"`
	On          bool                     `json:"on"`
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
	conn.SetReadLimit(128 << 10) // abbastanza per una foto profilo rimpicciolita

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
			// La regia può entrare solo dalla macchina di regia o da un telefono abbinato.
			if m.Regia && !isLoopback(r.RemoteAddr) {
				dev, ok := s.remoteDevice(r)
				if !ok {
					send(sfu.Message{Type: "error", Error: "il pannello di regia funziona solo sul computer di regia o su un telefono abbinato"})
					time.Sleep(200 * time.Millisecond) // lascia partire il messaggio
					return
				}
				defer s.remote.track(dev.ID, conn)()
			}
			if len(m.OwnerTokens) > 32 {
				m.OwnerTokens = m.OwnerTokens[:32]
			}
			peer, err = s.cfg.Room.Join(sfu.JoinRequest{
				Name:           sfu.CleanName(m.Name),
				Regia:          m.Regia,
				Session:        m.Session,
				Channel:        m.Channel,
				Password:       m.Password,
				CreateName:     strings.TrimSpace(m.CreateName),
				CreatePassword: m.CreatePass,
				OwnerTokens:    m.OwnerTokens,
			}, send)
			if err != nil {
				send(sfu.Message{Type: "error", Error: err.Error()})
				// Chiusura dopo che il messaggio è partito.
				time.Sleep(200 * time.Millisecond)
				return
			}
			log.Printf("[%s] entrato da %s", peer.Label, r.RemoteAddr)
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
		case "videoStart":
			if peer != nil {
				info, err := peer.StartVideo(m.Ref, strings.TrimSpace(m.Label), m.Channels)
				if err != nil {
					send(sfu.Message{Type: "videoError", Ref: m.Ref, Error: err.Error()})
				} else {
					send(sfu.Message{Type: "videoStarted", Ref: m.Ref, Stream: info.ID, Videos: []sfu.VideoInfo{*info}})
				}
			}
		case "videoStop", "videoApprove":
			if peer != nil {
				var err error
				if m.Type == "videoStop" {
					err = peer.StopVideo(m.Stream)
				} else {
					err = peer.ApproveVideo(m.Stream, m.On)
				}
				if err != nil {
					send(sfu.Message{Type: "error", Error: err.Error()})
				}
			}
		case "channelLook":
			// Il caporeparto cambia icona e sfondo del suo canale.
			if peer == nil {
				continue
			}
			if !peer.CanAdminChannel(m.Channel) {
				send(sfu.Message{Type: "error", Error: sfu.ErrNotAllowed.Error()})
				continue
			}
			lc := lookChange{ID: m.Channel, Icon: m.Icon, IconData: m.IconData, Color: m.Color, ImageData: m.ImageData, ClearImage: m.ClearImage}
			if err := s.applyLookChange(lc); err != nil {
				send(sfu.Message{Type: "error", Error: err.Error()})
			}
		case "videoChannels":
			if peer != nil {
				if err := peer.SetVideoChannels(m.Stream, m.Channels); err != nil {
					send(sfu.Message{Type: "error", Error: err.Error()})
				}
			}
		case "videoQuality":
			if peer != nil {
				peer.SetVideoQuality(m.Stream, m.Quality)
			}
		case "avatar":
			if peer != nil {
				if err := peer.SetAvatar(m.Avatar, m.Image); err != nil {
					send(sfu.Message{Type: "error", Error: err.Error()})
				}
			}
		case "control", "channelSettings", "kick", "promote", "demote":
			if peer == nil {
				continue
			}
			var err error
			switch m.Type {
			case "control":
				err = peer.Control(m.Peer, m.Channel, m.On)
			case "channelSettings":
				err = peer.SetChannelSettings(m.Channel, m.MicMode, m.Video)
			case "kick":
				err = peer.Kick(m.Peer)
			case "promote":
				err = peer.Promote(m.Peer, m.Channel)
			case "demote":
				err = peer.Demote(m.Peer)
			}
			if err != nil {
				send(sfu.Message{Type: "error", Error: err.Error()})
			}
		case "ping":
			// tiene vivo il collegamento anche dove i ping WebSocket non arrivano
		}
	}
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
