package evento

import (
	"path/filepath"
	"testing"
)

func TestNewAndChannels(t *testing.T) {
	e := New("Produzione", []string{"Video", "Audio", "Città", "Video"})
	ids := []string{}
	for _, c := range e.Channels {
		ids = append(ids, c.ID)
	}
	want := []string{"produzione", "video", "audio", "citta"}
	if len(ids) != len(want) {
		t.Fatalf("canali %v, attesi %v (Video doppio va scartato)", ids, want)
	}
	for i := range want {
		if ids[i] != want[i] {
			t.Fatalf("canali %v, attesi %v", ids, want)
		}
	}
	if err := e.RemoveChannel("produzione"); err == nil {
		t.Fatal("il canale principale non deve essere eliminabile")
	}
}

func TestUsersAndSaveLoad(t *testing.T) {
	e := New("Produzione", []string{"Video", "Audio"})
	e.Mode = ModeCredenziali
	if err := e.SetUser(User{Username: "marco", Name: "Marco", Role: RoleUser, Channels: []string{"video", "inesistente"}}, "cam1"); err != nil {
		t.Fatal(err)
	}
	if err := e.SetUser(User{Username: "nuovo", Role: RoleUser}, ""); err == nil {
		t.Fatal("un utente nuovo senza password deve dare errore")
	}
	if got := e.User("MARCO").Channels; len(got) != 1 || got[0] != "video" {
		t.Fatalf("canali utente %v: quelli inesistenti vanno scartati", got)
	}
	if _, ok := e.Authenticate("marco", "sbagliata"); ok {
		t.Fatal("password sbagliata accettata")
	}
	if _, ok := e.Authenticate("Marco", "cam1"); !ok {
		t.Fatal("password giusta rifiutata")
	}
	// Aggiornare senza password mantiene quella vecchia.
	if err := e.SetUser(User{Username: "marco", Name: "Marco B.", Role: RoleAdmin, Channels: []string{"video"}}, ""); err != nil {
		t.Fatal(err)
	}
	if _, ok := e.Authenticate("marco", "cam1"); !ok {
		t.Fatal("la password non deve cambiare se lasciata vuota")
	}

	path := filepath.Join(t.TempDir(), "evento.json")
	if err := e.Save(path); err != nil {
		t.Fatal(err)
	}
	l, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if l.Mode != ModeCredenziali || l.User("marco").Name != "Marco B." || l.User("marco").Role != RoleAdmin {
		t.Fatalf("evento riletto diverso: %+v", l)
	}

	// Eliminare un canale lo toglie agli utenti.
	if err := l.RemoveChannel("video"); err != nil {
		t.Fatal(err)
	}
	if len(l.User("marco").Channels) != 0 {
		t.Fatal("il canale eliminato deve sparire dagli utenti")
	}
}

func TestTokens(t *testing.T) {
	tok, h := NewToken()
	if len(tok) < 40 || TokenHash(tok) != h {
		t.Fatal("gettone non valido")
	}
	tok2, _ := NewToken()
	if tok == tok2 {
		t.Fatal("due gettoni uguali")
	}
}
