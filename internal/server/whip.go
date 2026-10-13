package server

import (
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"

	"github.com/battacharlie/charlie26/internal/sfu"
)

// WHIP (RFC 9725): OBS manda un'offerta SDP con POST e riceve la risposta.

func bearer(r *http.Request) string {
	h := r.Header.Get("Authorization")
	if len(h) > 7 && strings.EqualFold(h[:7], "bearer ") {
		return strings.TrimSpace(h[7:])
	}
	return ""
}

func (s *Server) handleWHIP(w http.ResponseWriter, r *http.Request) {
	if ct := r.Header.Get("Content-Type"); !strings.HasPrefix(ct, "application/sdp") {
		http.Error(w, "serve Content-Type: application/sdp", http.StatusUnsupportedMediaType)
		return
	}
	offer, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 64<<10))
	if err != nil {
		http.Error(w, "offerta troppo grande", http.StatusRequestEntityTooLarge)
		return
	}
	answer, id, err := s.cfg.Room.IngestWHIP(bearer(r), string(offer), strings.TrimSpace(r.URL.Query().Get("titolo")))
	if errors.Is(err, sfu.ErrWHIPToken) {
		log.Printf("WHIP: chiave sbagliata da %s", r.RemoteAddr)
		http.Error(w, err.Error(), http.StatusUnauthorized)
		return
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	w.Header().Set("Content-Type", "application/sdp")
	w.Header().Set("Location", "/whip/"+id)
	w.WriteHeader(http.StatusCreated)
	_, _ = io.WriteString(w, answer)
}

func (s *Server) handleWHIPDelete(w http.ResponseWriter, r *http.Request) {
	if bearer(r) != s.cfg.Room.WHIPToken(false) {
		http.Error(w, sfu.ErrWHIPToken.Error(), http.StatusUnauthorized)
		return
	}
	if !s.cfg.Room.StopWHIP(r.PathValue("id")) {
		http.NotFound(w, r)
		return
	}
	w.WriteHeader(http.StatusOK)
}

// handleWHIPInfo: indirizzi e chiave da copiare in OBS (POST con
// {"renew":true} crea una chiave nuova).
func (s *Server) handleWHIPInfo(w http.ResponseWriter, r *http.Request) {
	renew := false
	if r.Method == http.MethodPost {
		var req struct{ Renew bool }
		_ = jsonDecode(r, &req)
		renew = req.Renew
	}
	token := s.cfg.Room.WHIPToken(renew)
	writeJSON(w, http.StatusOK, map[string]string{
		"token":    token,
		"urlHTTP":  fmt.Sprintf("http://%s%s/whip", s.Host(), portSuffix(s.cfg.HTTPPort, 80)),
		"urlHTTPS": fmt.Sprintf("https://%s%s/whip", s.Host(), portSuffix(s.cfg.HTTPSPort, 443)),
	})
}
