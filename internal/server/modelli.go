package server

import (
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/battacharlie/charlie26/internal/evento"
)

// Modelli di evento: configurazioni salvate nella cartella dati della regia,
// da ricaricare con un clic (stesso formato dell'esportazione).

var modelFile = regexp.MustCompile(`^[a-z0-9-]{1,60}\.json$`)

type modelInfo struct {
	File    string `json:"file"`
	Nome    string `json:"nome"`
	Salvato string `json:"salvato"`
	Mode    string `json:"mode"`
	Canali  int    `json:"canali"`
	Utenti  int    `json:"utenti"`
}

func (s *Server) listModels() ([]modelInfo, error) {
	entries, err := os.ReadDir(s.cfg.ModelsDir)
	if errors.Is(err, os.ErrNotExist) {
		return []modelInfo{}, nil
	}
	if err != nil {
		return nil, err
	}
	out := []modelInfo{}
	for _, e := range entries {
		if e.IsDir() || !modelFile.MatchString(e.Name()) {
			continue
		}
		b, err := os.ReadFile(filepath.Join(s.cfg.ModelsDir, e.Name()))
		if err != nil {
			continue
		}
		var f evento.ExportFile
		if json.Unmarshal(b, &f) != nil || f.Evento == nil {
			continue
		}
		name := f.Nome
		if name == "" {
			name = strings.TrimSuffix(e.Name(), ".json")
		}
		out = append(out, modelInfo{File: e.Name(), Nome: name, Salvato: f.Esportato, Mode: f.Evento.Mode, Canali: len(f.Evento.Channels), Utenti: len(f.Evento.Users)})
	}
	sort.Slice(out, func(i, j int) bool { return strings.ToLower(out[i].Nome) < strings.ToLower(out[j].Nome) })
	return out, nil
}

func (s *Server) handleModels(w http.ResponseWriter, r *http.Request) {
	if s.cfg.ModelsDir == "" {
		writeJSON(w, http.StatusNotImplemented, map[string]string{"error": "non disponibile"})
		return
	}
	list, err := s.listModels()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, list)
}

func (s *Server) handleModelOp(w http.ResponseWriter, r *http.Request) {
	if s.cfg.ModelsDir == "" {
		writeJSON(w, http.StatusNotImplemented, map[string]string{"error": "non disponibile"})
		return
	}
	var req struct{ Op, Nome, File string }
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "richiesta non valida"})
		return
	}
	if err := s.modelOp(req.Op, req.Nome, req.File); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	s.handleModels(w, r)
}

func (s *Server) modelOp(op, name, file string) error {
	switch op {
	case "save":
		name = strings.TrimSpace(name)
		if name == "" || len([]rune(name)) > 60 {
			return errors.New("scrivi un nome per il modello (massimo 60 caratteri)")
		}
		if err := os.MkdirAll(s.cfg.ModelsDir, 0o700); err != nil {
			return err
		}
		f := evento.ExportFile{
			Tipo: evento.FileType, Versione: 1, Nome: name,
			Esportato: time.Now().Format(time.RFC3339),
			Evento:    s.cfg.Room.ExportEvent(),
		}
		s.embedImages(&f) // il modello resta valido anche copiato su un altro computer
		b, err := json.MarshalIndent(f, "", "  ")
		if err != nil {
			return err
		}
		// Stesso nome = sovrascrive il modello precedente.
		return os.WriteFile(filepath.Join(s.cfg.ModelsDir, evento.Slug(name)+".json"), b, 0o600)
	case "load", "delete":
		if !modelFile.MatchString(file) {
			return errors.New("modello non valido")
		}
		path := filepath.Join(s.cfg.ModelsDir, file)
		if op == "delete" {
			return os.Remove(path)
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return errors.New("modello non trovato")
		}
		f, err := evento.ParseExportFile(b)
		if err != nil {
			return err
		}
		s.storeImages(f, f.Evento)
		return s.cfg.Room.UpdateEvent(func(e *evento.Event) error { *e = *f.Evento; return nil })
	default:
		return errors.New("operazione sconosciuta")
	}
}
