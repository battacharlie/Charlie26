package sfu

import (
	"fmt"
	"strings"
	"unicode"
)

// DefaultChannels: Produzione più tre reparti tipici.
func DefaultChannels() []Channel {
	return ChannelsFromNames("Produzione", []string{"Video", "Audio", "Luci"})
}

// ChannelsFromNames crea l'elenco canali: main è il canale principale,
// departments i reparti.
func ChannelsFromNames(main string, departments []string) []Channel {
	out := []Channel{{ID: "produzione", Name: strings.TrimSpace(main), Main: true}}
	if out[0].Name == "" {
		out[0].Name = "Produzione"
	}
	used := map[string]bool{"produzione": true}
	for _, d := range departments {
		d = strings.TrimSpace(d)
		if d == "" {
			continue
		}
		id := slug(d)
		for base, n := id, 2; used[id]; n++ {
			id = fmt.Sprintf("%s-%d", base, n)
		}
		used[id] = true
		out = append(out, Channel{ID: id, Name: d})
	}
	return out
}

func slug(s string) string {
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

func validateChannels(chs []Channel) error {
	mains := 0
	seen := map[string]bool{}
	for _, c := range chs {
		if c.ID == "" || seen[c.ID] {
			return fmt.Errorf("canale con id vuoto o duplicato: %q", c.ID)
		}
		seen[c.ID] = true
		if c.Main {
			mains++
		}
	}
	if mains != 1 {
		return fmt.Errorf("serve esattamente un canale principale, trovati %d", mains)
	}
	return nil
}

func (r *Room) mainLocked() string {
	for _, c := range r.channels {
		if c.Main {
			return c.ID
		}
	}
	return ""
}

func (r *Room) hasChannelLocked(id string) bool {
	for _, c := range r.channels {
		if c.ID == id {
			return true
		}
	}
	return false
}

// membershipLocked calcola i canali di cui fa parte chi entra, in base al ruolo:
//   - regia: tutti;
//   - caporeparto: Produzione + il proprio reparto;
//   - tecnico: solo il canale scelto.
func (r *Room) membershipLocked(role, channel string) (map[string]bool, error) {
	m := map[string]bool{}
	switch role {
	case RoleRegia:
		for _, c := range r.channels {
			m[c.ID] = true
		}
		return m, nil
	case RoleCaporeparto, RoleTecnico:
		if channel == "" {
			channel = r.mainLocked()
		}
		if !r.hasChannelLocked(channel) {
			return nil, fmt.Errorf("%w: %q", errUnknownChannel, channel)
		}
		m[channel] = true
		if role == RoleCaporeparto {
			m[r.mainLocked()] = true
		}
		return m, nil
	default:
		return nil, fmt.Errorf("ruolo sconosciuto: %q", role)
	}
}

// subscribed: "to" deve avere pronto il flusso di "from" (hanno un canale in comune).
func subscribed(to, from *Peer) bool {
	if to == from || to.closed || from.closed {
		return false
	}
	for c := range from.member {
		if to.member[c] {
			return true
		}
	}
	return false
}

// audible: "to" sente "from" in questo istante, cioè "from" parla su almeno
// un canale che "to" ascolta.
func audible(from, to *Peer) bool {
	if to == from || to.closed || from.closed {
		return false
	}
	for c := range from.talk {
		if to.member[c] && !to.muted[c] {
			return true
		}
	}
	return false
}

// orderedLocked restituisce gli id dell'insieme nell'ordine dei canali.
func (r *Room) orderedLocked(set map[string]bool) []string {
	out := []string{}
	for _, c := range r.channels {
		if set[c.ID] {
			out = append(out, c.ID)
		}
	}
	return out
}

func sameSet(a, b map[string]bool) bool {
	if len(a) != len(b) {
		return false
	}
	for k := range a {
		if !b[k] {
			return false
		}
	}
	return true
}
