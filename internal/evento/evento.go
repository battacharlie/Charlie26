// Package evento descrive la configurazione di un evento (modalità di accesso,
// canali, utenti) e la salva su file JSON nella cartella dati.
package evento

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode"

	"golang.org/x/crypto/bcrypt"
)

// Modalità di accesso.
const (
	ModeLibero      = "libero"      // QR unico, ognuno sceglie canale e nome
	ModeCredenziali = "credenziali" // login con utente e password creati dalla regia
)

// Gestione del microfono degli utenti di un canale (decisa dal caporeparto).
const (
	MicLibero = "libero" // tocco per aprire/chiudere e push-to-talk
	MicPTT    = "ptt"    // solo push-to-talk (tenere premuto)
	MicAdmin  = "admin"  // solo il caporeparto apre il microfono degli utenti
)

// Ruoli degli utenti con credenziali.
const (
	RoleAdmin = "admin" // Responsabile di canale (caporeparto)
	RoleUser  = "user"  // Utente base (tecnico)
)

// Channel è un canale intercom.
type Channel struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Main    bool   `json:"main,omitempty"`
	MicMode string `json:"micMode"`
	Video   bool   `json:"video"` // il flusso video della regia arriva in questo canale (Fase 5)

	// Solo in libero accesso: password del sottocanale e chi lo amministra.
	PasswordHash string   `json:"passwordHash,omitempty"`
	OwnerHashes  []string `json:"ownerHashes,omitempty"` // sha256 dei gettoni di chi è caporeparto
}

// User è un utente con credenziali.
type User struct {
	Username     string   `json:"username"`
	Name         string   `json:"name"`
	PasswordHash string   `json:"passwordHash"`
	Role         string   `json:"role"`
	Channels     []string `json:"channels"`
}

// Event è la configurazione completa.
type Event struct {
	Name     string    `json:"name"`
	Mode     string    `json:"mode"`
	Channels []Channel `json:"channels"`
	Users    []User    `json:"users,omitempty"`
}

// New crea un evento in libero accesso con il canale principale e i reparti indicati.
func New(mainName string, departments []string) *Event {
	e := &Event{Name: "Evento", Mode: ModeLibero}
	if strings.TrimSpace(mainName) == "" {
		mainName = "Produzione"
	}
	e.Channels = []Channel{{ID: "produzione", Name: strings.TrimSpace(mainName), Main: true, MicMode: MicLibero, Video: true}}
	for _, d := range departments {
		if strings.TrimSpace(d) != "" {
			_, _ = e.AddChannel(d)
		}
	}
	return e
}

// Load legge l'evento da file.
func Load(path string) (*Event, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var e Event
	if err := json.Unmarshal(b, &e); err != nil {
		return nil, fmt.Errorf("file evento non valido: %w", err)
	}
	if err := e.Validate(); err != nil {
		return nil, err
	}
	return &e, nil
}

// Save scrive l'evento su file in modo sicuro (prima su un file temporaneo).
func (e *Event) Save(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	b, err := json.MarshalIndent(e, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// Clone restituisce una copia indipendente.
func (e *Event) Clone() *Event {
	b, _ := json.Marshal(e)
	var c Event
	_ = json.Unmarshal(b, &c)
	return &c
}

// Validate controlla la coerenza e completa i campi mancanti.
func (e *Event) Validate() error {
	if e.Mode != ModeLibero && e.Mode != ModeCredenziali {
		return fmt.Errorf("modalità sconosciuta: %q", e.Mode)
	}
	mains := 0
	seen := map[string]bool{}
	for i := range e.Channels {
		c := &e.Channels[i]
		if c.ID == "" || seen[c.ID] {
			return fmt.Errorf("canale con id vuoto o duplicato: %q", c.ID)
		}
		seen[c.ID] = true
		if c.Main {
			mains++
		}
		switch c.MicMode {
		case MicLibero, MicPTT, MicAdmin:
		case "":
			c.MicMode = MicLibero
		default:
			return fmt.Errorf("canale %s: gestione microfono sconosciuta %q", c.Name, c.MicMode)
		}
	}
	if mains != 1 {
		return fmt.Errorf("serve esattamente un canale principale, trovati %d", mains)
	}
	users := map[string]bool{}
	for _, u := range e.Users {
		key := strings.ToLower(u.Username)
		if key == "" || users[key] {
			return fmt.Errorf("utente vuoto o duplicato: %q", u.Username)
		}
		users[key] = true
		if u.Role != RoleAdmin && u.Role != RoleUser {
			return fmt.Errorf("utente %s: ruolo sconosciuto %q", u.Username, u.Role)
		}
	}
	return nil
}

// Main restituisce il canale principale.
func (e *Event) Main() *Channel {
	for i := range e.Channels {
		if e.Channels[i].Main {
			return &e.Channels[i]
		}
	}
	return nil
}

// Channel cerca un canale per id.
func (e *Event) Channel(id string) *Channel {
	for i := range e.Channels {
		if e.Channels[i].ID == id {
			return &e.Channels[i]
		}
	}
	return nil
}

// AddChannel aggiunge un reparto e ne restituisce l'id.
func (e *Event) AddChannel(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", errors.New("il nome del canale è vuoto")
	}
	if len([]rune(name)) > 24 {
		return "", errors.New("nome del canale troppo lungo (massimo 24 caratteri)")
	}
	for _, c := range e.Channels {
		if strings.EqualFold(c.Name, name) {
			return "", fmt.Errorf("esiste già un canale %q", c.Name)
		}
	}
	id := Slug(name)
	for base, n := id, 2; e.Channel(id) != nil; n++ {
		id = fmt.Sprintf("%s-%d", base, n)
	}
	e.Channels = append(e.Channels, Channel{ID: id, Name: name, MicMode: MicLibero})
	return id, nil
}

// RemoveChannel elimina un reparto (il canale principale non si elimina)
// e lo toglie dagli utenti.
func (e *Event) RemoveChannel(id string) error {
	for i, c := range e.Channels {
		if c.ID != id {
			continue
		}
		if c.Main {
			return errors.New("il canale principale non si può eliminare")
		}
		e.Channels = append(e.Channels[:i], e.Channels[i+1:]...)
		for j := range e.Users {
			e.Users[j].Channels = without(e.Users[j].Channels, id)
		}
		return nil
	}
	return errors.New("canale inesistente")
}

// User cerca un utente (senza distinguere maiuscole e minuscole).
func (e *Event) User(username string) *User {
	for i := range e.Users {
		if strings.EqualFold(e.Users[i].Username, strings.TrimSpace(username)) {
			return &e.Users[i]
		}
	}
	return nil
}

// SetUser crea o aggiorna un utente. Con password vuota la password non cambia
// (ma per un utente nuovo è obbligatoria).
func (e *Event) SetUser(u User, password string) error {
	u.Username = strings.TrimSpace(u.Username)
	u.Name = strings.TrimSpace(u.Name)
	if u.Username == "" || strings.ContainsAny(u.Username, " \t") {
		return errors.New("nome utente vuoto o con spazi")
	}
	if u.Name == "" {
		u.Name = u.Username
	}
	if u.Role != RoleAdmin && u.Role != RoleUser {
		return fmt.Errorf("ruolo sconosciuto: %q", u.Role)
	}
	var chs []string
	for _, c := range u.Channels {
		if e.Channel(c) != nil && !contains(chs, c) {
			chs = append(chs, c)
		}
	}
	u.Channels = chs
	existing := e.User(u.Username)
	if password != "" {
		h, err := HashPassword(password)
		if err != nil {
			return err
		}
		u.PasswordHash = h
	} else if existing != nil {
		u.PasswordHash = existing.PasswordHash
	} else {
		return errors.New("serve una password per il nuovo utente")
	}
	if existing != nil {
		*existing = u
	} else {
		e.Users = append(e.Users, u)
	}
	return nil
}

// RemoveUser elimina un utente.
func (e *Event) RemoveUser(username string) error {
	for i := range e.Users {
		if strings.EqualFold(e.Users[i].Username, username) {
			e.Users = append(e.Users[:i], e.Users[i+1:]...)
			return nil
		}
	}
	return errors.New("utente inesistente")
}

// Authenticate verifica utente e password.
func (e *Event) Authenticate(username, password string) (*User, bool) {
	u := e.User(username)
	if u == nil || !CheckPassword(u.PasswordHash, password) {
		return nil, false
	}
	return u, true
}

// HashPassword calcola l'impronta bcrypt di una password.
func HashPassword(p string) (string, error) {
	if len(p) < 4 {
		return "", errors.New("password troppo corta (minimo 4 caratteri)")
	}
	b, err := bcrypt.GenerateFromPassword([]byte(p), bcrypt.DefaultCost)
	return string(b), err
}

// CheckPassword confronta una password con la sua impronta.
func CheckPassword(hash, p string) bool {
	return hash != "" && bcrypt.CompareHashAndPassword([]byte(hash), []byte(p)) == nil
}

// NewToken genera un gettone casuale e la sua impronta da salvare.
func NewToken() (token, hash string) {
	b := make([]byte, 24)
	_, _ = rand.Read(b)
	token = hex.EncodeToString(b)
	return token, TokenHash(token)
}

// TokenHash è l'impronta sha256 di un gettone (i gettoni sono casuali e lunghi,
// quindi non serve bcrypt).
func TokenHash(token string) string {
	s := sha256.Sum256([]byte(token))
	return hex.EncodeToString(s[:])
}

// Slug trasforma un nome in un identificativo semplice.
func Slug(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
		case strings.ContainsRune("àáâä", r):
			b.WriteRune('a')
		case strings.ContainsRune("èéêë", r):
			b.WriteRune('e')
		case strings.ContainsRune("ìíîï", r):
			b.WriteRune('i')
		case strings.ContainsRune("òóôö", r):
			b.WriteRune('o')
		case strings.ContainsRune("ùúûü", r):
			b.WriteRune('u')
		case unicode.IsSpace(r) || r == '-' || r == '_':
			if b.Len() > 0 && !strings.HasSuffix(b.String(), "-") {
				b.WriteRune('-')
			}
		}
	}
	id := strings.Trim(b.String(), "-")
	if id == "" {
		id = "canale"
	}
	return id
}

func contains(xs []string, x string) bool {
	for _, y := range xs {
		if y == x {
			return true
		}
	}
	return false
}

func without(xs []string, x string) []string {
	out := xs[:0]
	for _, y := range xs {
		if y != x {
			out = append(out, y)
		}
	}
	return out
}

// FileType identifica i file di configurazione esportati da questo programma.
const FileType = "intercom-regia/evento"

// ExportFile è il contenuto di un file di configurazione esportato.
type ExportFile struct {
	Tipo      string `json:"tipo"`
	Versione  int    `json:"versione"`
	Esportato string `json:"esportato"`
	Evento    *Event `json:"evento"`
}

// ParseExport legge un file esportato (accetta anche un evento.json "nudo").
// I gettoni da caporeparto non vengono importati: valgono solo per l'evento
// in cui sono nati.
func ParseExport(b []byte) (*Event, error) {
	var f ExportFile
	if err := json.Unmarshal(b, &f); err != nil {
		return nil, errors.New("il file non è una configurazione valida (JSON non leggibile)")
	}
	e := f.Evento
	if f.Tipo == "" && e == nil {
		var bare Event
		if err := json.Unmarshal(b, &bare); err != nil || len(bare.Channels) == 0 {
			return nil, errors.New("il file non sembra una configurazione dell'intercom")
		}
		e = &bare
	} else if f.Tipo != FileType || e == nil {
		return nil, errors.New("il file non sembra una configurazione dell'intercom")
	}
	for i := range e.Channels {
		e.Channels[i].OwnerHashes = nil
	}
	if err := e.Validate(); err != nil {
		return nil, fmt.Errorf("configurazione non valida: %w", err)
	}
	return e, nil
}
