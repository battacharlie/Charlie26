package sfu

// Regole di ascolto dell'intercom.

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
// un canale di cui "to" è membro e che "to" non ha silenziato.
func audible(from, to *Peer) bool {
	if to == from || to.closed || from.closed {
		return false
	}
	for c := range from.speaking() {
		if to.member[c] && !to.muted[c] {
			return true
		}
	}
	return false
}

// orderedLocked restituisce gli id dell'insieme nell'ordine dei canali.
func (r *Room) orderedLocked(set map[string]bool) []string {
	out := []string{}
	for _, c := range r.event.Channels {
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
