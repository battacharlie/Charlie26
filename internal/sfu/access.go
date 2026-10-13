package sfu

import (
	"errors"
	"fmt"
	"log"
	"strings"

	"github.com/battacharlie/charlie26/internal/evento"
)

// Ruoli mostrati nell'interfaccia.
const (
	RoleRegia       = "regia"       // SupAdmin: tutti i canali, tutti i poteri
	RoleCaporeparto = "caporeparto" // Admin: Produzione + i propri canali
	RoleTecnico     = "tecnico"     // User: solo i canali assegnati/scelti
)

// Tipi di identità.
const (
	identRegia = "regia" // console sulla macchina di regia
	identUser  = "user"  // utente con credenziali
	identFree  = "free"  // libero accesso
)

// identity è "chi è" un partecipante. Canali e permessi si ricalcolano da qui
// ogni volta che la regia cambia l'evento.
type identity struct {
	kind        string
	name        string
	username    string   // identUser
	channel     string   // identFree: canale scelto all'ingresso
	ownerTokens []string // identFree: gettoni da caporeparto
}

// JoinRequest sono i dati inviati dal browser per entrare.
type JoinRequest struct {
	Name           string
	Regia          bool     // impostato dal server solo per connessioni dalla macchina di regia
	Session        string   // credenziali: gettone ottenuto con Login
	Channel        string   // libero: canale scelto
	Password       string   // libero: password del sottocanale
	CreateName     string   // libero: crea un nuovo sottocanale con questo nome...
	CreatePassword string   // ...e questa password (facoltativa)
	OwnerTokens    []string // libero: gettoni da caporeparto memorizzati sul telefono
}

// ChannelInfo è la parte pubblica di un canale.
type ChannelInfo struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Main      bool   `json:"main,omitempty"`
	MicMode   string `json:"micMode"`
	Video     bool   `json:"video"`
	Protected bool   `json:"protected,omitempty"` // serve una password per entrare
}

// Errori mostrati all'utente.
var (
	ErrBadPassword    = errors.New("password del canale errata")
	ErrBadLogin       = errors.New("nome utente o password errati")
	ErrSessionExpired = errors.New("sessione scaduta: rifai l'accesso")
	ErrNotAllowed     = errors.New("operazione non permessa")
)

type ownerGrant struct{ channel, token string }

// admitLocked verifica la richiesta di ingresso e restituisce l'identità.
// In libero accesso può creare un nuovo sottocanale (chi lo crea ne diventa
// responsabile e riceve un gettone da conservare sul telefono).
func (r *Room) admitLocked(req JoinRequest) (identity, *ownerGrant, error) {
	name := req.Name
	if req.Regia {
		return identity{kind: identRegia, name: name}, nil, nil
	}
	if r.event.Mode == evento.ModeCredenziali {
		username, ok := r.sessions[req.Session]
		if !ok || r.event.User(username) == nil {
			return identity{}, nil, ErrSessionExpired
		}
		return identity{kind: identUser, username: username}, nil, nil
	}

	id := identity{kind: identFree, name: name, ownerTokens: req.OwnerTokens}
	if req.CreateName != "" {
		chID, err := r.event.AddChannel(req.CreateName)
		if err != nil {
			return identity{}, nil, err
		}
		ch := r.event.Channel(chID)
		if req.CreatePassword != "" {
			h, err := evento.HashPassword(req.CreatePassword)
			if err != nil {
				_ = r.event.RemoveChannel(chID)
				return identity{}, nil, err
			}
			ch.PasswordHash = h
		}
		token, hash := evento.NewToken()
		ch.OwnerHashes = []string{hash}
		id.channel = chID
		id.ownerTokens = append(id.ownerTokens, token)
		r.saveLocked()
		log.Printf("creato il canale %q da %s", ch.Name, name)
		return id, &ownerGrant{channel: chID, token: token}, nil
	}

	ch := r.event.Channel(req.Channel)
	if ch == nil {
		return identity{}, nil, fmt.Errorf("il canale non esiste più")
	}
	if ch.PasswordHash != "" && !r.ownsLocked(id, ch) && !evento.CheckPassword(ch.PasswordHash, req.Password) {
		return identity{}, nil, ErrBadPassword
	}
	id.channel = ch.ID
	return id, nil, nil
}

func (r *Room) ownsLocked(id identity, ch *evento.Channel) bool {
	for _, t := range id.ownerTokens {
		h := evento.TokenHash(t)
		for _, oh := range ch.OwnerHashes {
			if h == oh {
				return true
			}
		}
	}
	return false
}

type resolved struct {
	name, role    string
	member, admin map[string]bool
}

// resolveLocked ricava nome, ruolo, canali e responsabilità da un'identità.
func (r *Room) resolveLocked(id identity) (resolved, error) {
	res := resolved{name: id.name, member: map[string]bool{}, admin: map[string]bool{}}
	main := r.event.Main()
	switch id.kind {
	case identRegia:
		res.role = RoleRegia
		for _, c := range r.event.Channels {
			res.member[c.ID] = true
			res.admin[c.ID] = true
		}
	case identUser:
		u := r.event.User(id.username)
		if u == nil {
			return res, errors.New("il tuo utente è stato eliminato dalla regia")
		}
		res.name = u.Name
		for _, c := range u.Channels {
			if r.event.Channel(c) != nil {
				res.member[c] = true
				if u.Role == evento.RoleAdmin && c != main.ID {
					res.admin[c] = true
				}
			}
		}
		if u.Role == evento.RoleAdmin {
			res.member[main.ID] = true
		}
		if len(res.member) == 0 {
			return res, errors.New("non hai canali assegnati: chiedi alla regia")
		}
	case identFree:
		ch := r.event.Channel(id.channel)
		if ch == nil {
			return res, errors.New("il tuo canale è stato eliminato dalla regia")
		}
		res.member[ch.ID] = true
		for i := range r.event.Channels {
			c := &r.event.Channels[i]
			if !c.Main && r.ownsLocked(id, c) {
				res.admin[c.ID] = true
				res.member[c.ID] = true
			}
		}
		if len(res.admin) > 0 {
			res.member[main.ID] = true // il caporeparto parla anche in Produzione
		}
	}
	switch {
	case res.role == RoleRegia:
	case len(res.admin) > 0:
		res.role = RoleCaporeparto
	default:
		res.role = RoleTecnico
	}
	return res, nil
}

// canTalkLocked: p può aprire da sé il microfono sul canale c?
func (r *Room) canTalkLocked(p *Peer, c string) bool {
	if !p.member[c] {
		return false
	}
	if p.admin[c] {
		return true
	}
	ch := r.event.Channel(c)
	return ch != nil && ch.MicMode != evento.MicAdmin
}

func (r *Room) channelInfoLocked() []ChannelInfo {
	out := make([]ChannelInfo, 0, len(r.event.Channels))
	for _, c := range r.event.Channels {
		out = append(out, ChannelInfo{ID: c.ID, Name: c.Name, Main: c.Main, MicMode: c.MicMode, Video: c.Video, Protected: c.PasswordHash != ""})
	}
	return out
}

// Channels restituisce l'elenco pubblico dei canali.
func (r *Room) Channels() []ChannelInfo {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.channelInfoLocked()
}

// Mode restituisce la modalità di accesso.
func (r *Room) Mode() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.event.Mode
}

// Login verifica le credenziali e restituisce un gettone di sessione.
func (r *Room) Login(username, password string) (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.event.Mode != evento.ModeCredenziali {
		return "", errors.New("l'evento è in libero accesso")
	}
	u, ok := r.event.Authenticate(username, password)
	if !ok {
		return "", ErrBadLogin
	}
	token, _ := evento.NewToken()
	r.sessions[token] = u.Username
	return token, nil
}

// LoginKey accede con la chiave del QR personale. Restituisce il gettone di
// sessione e il nome mostrato dell'utente.
func (r *Room) LoginKey(key string) (string, string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.event.Mode != evento.ModeCredenziali {
		return "", "", errors.New("l'evento è in libero accesso")
	}
	u := r.event.UserByKey(key)
	if u == nil {
		return "", "", errors.New("QR non più valido: chiedine uno nuovo alla regia")
	}
	token, _ := evento.NewToken()
	r.sessions[token] = u.Username
	return token, u.Name, nil
}

// UserQRKey restituisce la chiave del QR personale di un utente, creandola
// se non esiste (o sostituendola se renew è vero: i QR vecchi smettono di
// funzionare).
func (r *Room) UserQRKey(username string, renew bool) (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	u := r.event.User(username)
	if u == nil {
		return "", errors.New("utente inesistente")
	}
	if u.QRKey == "" || renew {
		u.QRKey, _ = evento.NewToken()
		r.saveLocked()
	}
	return u.QRKey, nil
}

// ---------- Poteri di responsabile e regia ----------

// Control: il responsabile apre (open=true) o chiude il microfono di un
// utente del suo canale.
func (p *Peer) Control(targetID, channel string, open bool) error {
	r := p.room
	r.mu.Lock()
	defer r.mu.Unlock()
	t := r.peers[targetID]
	if t == nil || !p.admin[channel] || !t.member[channel] || t == p {
		return ErrNotAllowed
	}
	if t.admin[channel] && p.Role != RoleRegia {
		return ErrNotAllowed // un caporeparto non comanda un altro caporeparto
	}
	if open {
		t.forced[channel] = true
	} else {
		delete(t.forced, channel)
		if t.talk[channel] {
			delete(t.talk, channel)
			t.send(Message{Type: "mute", Channel: channel})
		}
	}
	log.Printf("[%s %s] %s il microfono di %s su %s", p.ID, p.Name, map[bool]string{true: "apre", false: "chiude"}[open], t.Name, channel)
	r.updateGatesLocked()
	r.broadcastPeersLocked()
	return nil
}

// SetChannelSettings: il responsabile decide come gli utenti usano il
// microfono e se nel canale arriva il video della regia.
func (p *Peer) SetChannelSettings(channel, micMode string, video bool) error {
	r := p.room
	r.mu.Lock()
	defer r.mu.Unlock()
	if !p.admin[channel] {
		return ErrNotAllowed
	}
	ch := r.event.Channel(channel)
	if ch == nil {
		return ErrNotAllowed
	}
	switch micMode {
	case evento.MicLibero, evento.MicPTT, evento.MicAdmin:
	default:
		return fmt.Errorf("gestione microfono sconosciuta: %q", micMode)
	}
	ch.MicMode, ch.Video = micMode, video
	r.saveLocked()
	r.refreshLocked()
	return nil
}

// Kick: la regia scollega un partecipante.
func (p *Peer) Kick(targetID string) error {
	r := p.room
	r.mu.Lock()
	t := r.peers[targetID]
	if p.Role != RoleRegia || t == nil || t == p {
		r.mu.Unlock()
		return ErrNotAllowed
	}
	t.send(Message{Type: "kicked", Error: "La regia ti ha scollegato."})
	r.mu.Unlock()
	go t.Close()
	return nil
}

// Promote: la regia nomina caporeparto un partecipante.
//   - libero accesso: responsabile del canale indicato; riceve un gettone da
//     conservare sul telefono;
//   - credenziali: il suo utente diventa Caporeparto dei canali assegnati.
func (p *Peer) Promote(targetID, channel string) error {
	r := p.room
	r.mu.Lock()
	defer r.mu.Unlock()
	t := r.peers[targetID]
	if p.Role != RoleRegia || t == nil || t == p {
		return ErrNotAllowed
	}
	switch t.ident.kind {
	case identFree:
		ch := r.event.Channel(channel)
		if ch == nil || ch.Main {
			return errors.New("scegli un sottocanale (non il canale principale)")
		}
		token, hash := evento.NewToken()
		ch.OwnerHashes = append(ch.OwnerHashes, hash)
		t.ident.ownerTokens = append(t.ident.ownerTokens, token)
		t.send(Message{Type: "ownerToken", Channel: channel, Token: token})
	case identUser:
		u := r.event.User(t.ident.username)
		if u == nil {
			return ErrNotAllowed
		}
		u.Role = evento.RoleAdmin
	default:
		return ErrNotAllowed
	}
	log.Printf("la regia nomina %s caporeparto", t.Name)
	r.saveLocked()
	r.refreshLocked()
	return nil
}

// Demote: la regia toglie a un partecipante il ruolo di caporeparto.
//   - libero accesso: i suoi gettoni da caporeparto vengono annullati su tutti
//     i canali (anche quelli salvati sul suo telefono smettono di valere);
//   - credenziali: il suo utente diventa Tecnico.
func (p *Peer) Demote(targetID string) error {
	r := p.room
	r.mu.Lock()
	defer r.mu.Unlock()
	t := r.peers[targetID]
	if p.Role != RoleRegia || t == nil || t == p {
		return ErrNotAllowed
	}
	switch t.ident.kind {
	case identFree:
		mine := map[string]bool{}
		for _, tok := range t.ident.ownerTokens {
			mine[evento.TokenHash(tok)] = true
		}
		for i := range r.event.Channels {
			ch := &r.event.Channels[i]
			kept := ch.OwnerHashes[:0]
			for _, h := range ch.OwnerHashes {
				if !mine[h] {
					kept = append(kept, h)
				}
			}
			ch.OwnerHashes = kept
		}
		t.ident.ownerTokens = nil
		t.send(Message{Type: "ownerRevoked"})
	case identUser:
		u := r.event.User(t.ident.username)
		if u == nil {
			return ErrNotAllowed
		}
		u.Role = evento.RoleUser
	default:
		return ErrNotAllowed
	}
	log.Printf("la regia toglie a %s il ruolo di caporeparto", t.Name)
	r.saveLocked()
	r.refreshLocked()
	return nil
}

// ---------- Configurazione dell'evento (solo console di regia) ----------

// Event restituisce una copia dell'evento senza impronte di password e gettoni.
func (r *Room) Event() *evento.Event {
	r.mu.Lock()
	defer r.mu.Unlock()
	e := r.event.Clone()
	for i := range e.Users {
		e.Users[i].PasswordHash = ""
		e.Users[i].QRKey = ""
	}
	for i := range e.Channels {
		e.Channels[i].OwnerHashes = nil
		if e.Channels[i].PasswordHash != "" {
			e.Channels[i].PasswordHash = "*"
		}
	}
	return e
}

// ExportEvent restituisce una copia completa dell'evento da salvare su file:
// include le impronte delle password (così gli utenti restano validi
// reimportandolo) ma non i gettoni da caporeparto.
func (r *Room) ExportEvent() *evento.Event {
	r.mu.Lock()
	defer r.mu.Unlock()
	e := r.event.Clone()
	for i := range e.Channels {
		e.Channels[i].OwnerHashes = nil
	}
	return e
}

// UpdateEvent applica una modifica all'evento. Se la modifica non è valida
// l'evento resta com'era. I partecipanti collegati vengono aggiornati subito.
func (r *Room) UpdateEvent(change func(e *evento.Event) error) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	next := r.event.Clone()
	if err := change(next); err != nil {
		return err
	}
	if err := next.Validate(); err != nil {
		return err
	}
	if next.Mode != r.event.Mode {
		r.sessions = map[string]string{} // cambio modalità: tutti rifanno l'accesso
	}
	r.event = next
	r.saveLocked()
	r.refreshLocked()
	return nil
}

func (r *Room) saveLocked() {
	if r.save == nil {
		return
	}
	if err := r.save(r.event); err != nil {
		log.Printf("ATTENZIONE: impossibile salvare l'evento: %v", err)
	}
}

// refreshLocked ricalcola canali e permessi di tutti i collegati dopo una
// modifica dell'evento. Chi non ha più diritto di restare viene scollegato.
func (r *Room) refreshLocked() {
	for _, p := range r.peers {
		res, err := r.resolveLocked(p.ident)
		if err == nil && p.ident.kind == identUser && r.event.Mode != evento.ModeCredenziali {
			err = errors.New("la regia ha cambiato la modalità di accesso")
		}
		if err == nil && p.ident.kind == identFree && r.event.Mode != evento.ModeLibero {
			err = errors.New("la regia ha attivato l'accesso con credenziali")
		}
		if err != nil {
			p.send(Message{Type: "kicked", Error: err.Error()})
			go p.Close()
			continue
		}
		changed := p.Name != res.name || p.Role != res.role || !sameSet(p.member, res.member) || !sameSet(p.admin, res.admin)
		p.Name, p.Role, p.member, p.admin = res.name, res.role, res.member, res.admin
		for _, set := range []map[string]bool{p.talk, p.forced, p.muted} {
			for c := range set {
				if !p.member[c] {
					delete(set, c)
				}
			}
		}
		for c := range p.talk {
			if !r.canTalkLocked(p, c) {
				delete(p.talk, c)
				p.send(Message{Type: "mute", Channel: c})
			}
		}
		if changed {
			info := p.infoLocked()
			p.send(Message{Type: "me", Me: &info})
		}
	}
	r.syncLocked()
	r.broadcastChannelsLocked()
	r.broadcastPeersLocked()
}

func (r *Room) broadcastChannelsLocked() {
	chs := r.channelInfoLocked()
	for _, p := range r.peers {
		p.send(Message{Type: "channels", Channels: chs, Mode: r.event.Mode})
	}
}

// CleanName normalizza il nome mostrato.
func CleanName(n string) string {
	n = strings.TrimSpace(n)
	if n == "" {
		return "Senza nome"
	}
	rs := []rune(n)
	if len(rs) > 24 {
		rs = rs[:24]
	}
	return string(rs)
}
