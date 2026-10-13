package server

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/battacharlie/charlie26/internal/evento"
)

// onlyRegia limita un'azione alla macchina di regia (connessioni da localhost).
func (s *Server) onlyRegia(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !isLoopback(r.RemoteAddr) {
			http.Error(w, "disponibile solo sul computer di regia", http.StatusForbidden)
			return
		}
		if r.Method != http.MethodGet && !sameOrigin(r) {
			// Un sito qualsiasi aperto nel browser della regia non deve poter
			// mandare comandi a localhost.
			http.Error(w, "richiesta non permessa", http.StatusForbidden)
			return
		}
		h(w, r)
	}
}

// sameOrigin: richiesta JSON partita da una pagina di questo stesso server.
func sameOrigin(r *http.Request) bool {
	if !strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
		return false // obbliga il browser a un controllo preventivo (CORS)
	}
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true // client non browser (es. test, curl)
	}
	u, err := url.Parse(origin)
	return err == nil && u.Host == r.Host
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	var req struct{ Username, Password string }
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "richiesta non valida"})
		return
	}
	token, err := s.cfg.Room.Login(req.Username, req.Password)
	if err != nil {
		time.Sleep(700 * time.Millisecond) // rallenta chi prova password a caso
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"session": token})
}

func (s *Server) handleExport(w http.ResponseWriter, r *http.Request) {
	e := s.cfg.Room.ExportEvent()
	b, err := json.MarshalIndent(evento.ExportFile{
		Tipo:      evento.FileType,
		Versione:  1,
		Esportato: time.Now().Format(time.RFC3339),
		Evento:    e,
	}, "", "  ")
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	name := evento.Slug(e.Name) + "-" + time.Now().Format("2006-01-02") + ".json"
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Content-Disposition", `attachment; filename="`+name+`"`)
	_, _ = w.Write(b)
}

func (s *Server) handleImport(w http.ResponseWriter, r *http.Request) {
	b, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 2<<20))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "file troppo grande"})
		return
	}
	imported, err := evento.ParseExport(b)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	if err := s.cfg.Room.UpdateEvent(func(e *evento.Event) error { *e = *imported; return nil }); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, s.cfg.Room.Event())
}

func (s *Server) handleLog(w http.ResponseWriter, r *http.Request) {
	var lines []string
	if s.cfg.Log != nil {
		lines = s.cfg.Log()
	}
	writeJSON(w, http.StatusOK, map[string]any{"lines": lines})
}

func (s *Server) handleShutdown(w http.ResponseWriter, r *http.Request) {
	if s.cfg.Shutdown == nil {
		writeJSON(w, http.StatusNotImplemented, map[string]string{"error": "non disponibile"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"ok": "spegnimento"})
	go func() {
		time.Sleep(300 * time.Millisecond) // lascia arrivare la risposta
		s.cfg.Shutdown()
	}()
}

func (s *Server) handleEventGet(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.cfg.Room.Event())
}

// eventOp è una modifica richiesta dalla console di regia.
type eventOp struct {
	Op       string      `json:"op"`
	Mode     string      `json:"mode"`
	Name     string      `json:"name"`
	ID       string      `json:"id"`
	MicMode  string      `json:"micMode"`
	Video    bool        `json:"video"`
	Password string      `json:"password"`
	User     evento.User `json:"user"`
}

func (s *Server) handleEventChange(w http.ResponseWriter, r *http.Request) {
	var op eventOp
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&op); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "richiesta non valida"})
		return
	}
	err := s.cfg.Room.UpdateEvent(func(e *evento.Event) error { return applyOp(e, op) })
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, s.cfg.Room.Event())
}

func applyOp(e *evento.Event, op eventOp) error {
	switch op.Op {
	case "mode":
		if op.Mode != evento.ModeLibero && op.Mode != evento.ModeCredenziali {
			return errors.New("modalità sconosciuta")
		}
		e.Mode = op.Mode
	case "name":
		if strings.TrimSpace(op.Name) == "" {
			return errors.New("il nome dell'evento è vuoto")
		}
		e.Name = strings.TrimSpace(op.Name)
	case "addChannel":
		_, err := e.AddChannel(op.Name)
		return err
	case "renameChannel":
		ch := e.Channel(op.ID)
		name := strings.TrimSpace(op.Name)
		if ch == nil || name == "" {
			return errors.New("canale o nome non valido")
		}
		for _, c := range e.Channels {
			if c.ID != ch.ID && strings.EqualFold(c.Name, name) {
				return errors.New("esiste già un canale con questo nome")
			}
		}
		ch.Name = name
	case "removeChannel":
		return e.RemoveChannel(op.ID)
	case "channelSettings":
		ch := e.Channel(op.ID)
		if ch == nil {
			return errors.New("canale inesistente")
		}
		ch.MicMode, ch.Video = op.MicMode, op.Video
	case "channelPassword":
		ch := e.Channel(op.ID)
		if ch == nil || ch.Main {
			return errors.New("il canale principale non ha password")
		}
		if op.Password == "" {
			ch.PasswordHash = ""
			return nil
		}
		h, err := evento.HashPassword(op.Password)
		if err != nil {
			return err
		}
		ch.PasswordHash = h
	case "setUser":
		return e.SetUser(op.User, op.Password)
	case "removeUser":
		return e.RemoveUser(op.User.Username)
	default:
		return errors.New("operazione sconosciuta: " + op.Op)
	}
	return nil
}
