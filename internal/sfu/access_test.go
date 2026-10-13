package sfu

import (
	"errors"
	"testing"

	"github.com/battacharlie/charlie26/internal/evento"
)

func testRoom(e *evento.Event) *Room {
	return &Room{event: e, sessions: map[string]string{}, peers: map[string]*Peer{}, tracks: map[string]*forward{}}
}

func join(t *testing.T, r *Room, req JoinRequest) (resolved, *ownerGrant, error) {
	t.Helper()
	id, grant, err := r.admitLocked(req)
	if err != nil {
		return resolved{}, nil, err
	}
	res, err := r.resolveLocked(id)
	return res, grant, err
}

func keys(r *Room, m map[string]bool) []string { return r.orderedLocked(m) }

func eq(a []string, b ...string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestFreeAccess(t *testing.T) {
	r := testRoom(evento.New("Produzione", []string{"Video"}))

	res, _, err := join(t, r, JoinRequest{Name: "Anna", Channel: "produzione"})
	if err != nil || res.role != RoleTecnico || !eq(keys(r, res.member), "produzione") {
		t.Fatalf("ingresso in Produzione: %+v %v", res, err)
	}

	// Chi crea un sottocanale ne diventa caporeparto.
	res, grant, err := join(t, r, JoinRequest{Name: "Luca", CreateName: "Palco", CreatePassword: "1234"})
	if err != nil || grant == nil || grant.channel != "palco" {
		t.Fatalf("creazione canale: %v %+v", err, grant)
	}
	if res.role != RoleCaporeparto || !eq(keys(r, res.member), "produzione", "palco") || !eq(keys(r, res.admin), "palco") {
		t.Fatalf("il creatore deve essere caporeparto di Palco: %+v", res)
	}

	// Gli altri entrano solo con la password giusta.
	if _, _, err := join(t, r, JoinRequest{Name: "Ugo", Channel: "palco", Password: "0000"}); !errors.Is(err, ErrBadPassword) {
		t.Fatalf("password sbagliata: atteso errore, ottenuto %v", err)
	}
	res, _, err = join(t, r, JoinRequest{Name: "Ugo", Channel: "palco", Password: "1234"})
	if err != nil || res.role != RoleTecnico || !eq(keys(r, res.member), "palco") {
		t.Fatalf("password giusta: %+v %v", res, err)
	}

	// Il creatore che si ricollega con il suo gettone rientra da caporeparto senza password.
	res, _, err = join(t, r, JoinRequest{Name: "Luca", Channel: "palco", OwnerTokens: []string{grant.token}})
	if err != nil || res.role != RoleCaporeparto {
		t.Fatalf("rientro del creatore: %+v %v", res, err)
	}

	// Un gettone inventato non dà poteri.
	res, _, _ = join(t, r, JoinRequest{Name: "Furbo", Channel: "produzione", OwnerTokens: []string{"deadbeef"}})
	if res.role != RoleTecnico {
		t.Fatalf("un gettone falso non deve dare poteri: %+v", res)
	}

	// Nomi doppi rifiutati.
	if _, _, err := join(t, r, JoinRequest{Name: "X", CreateName: "palco"}); err == nil {
		t.Fatal("un canale con lo stesso nome deve essere rifiutato")
	}
	// In credenziali non si creano canali e serve il login.
	r.event.Mode = evento.ModeCredenziali
	if _, _, err := join(t, r, JoinRequest{Name: "X", CreateName: "Cucina"}); !errors.Is(err, ErrSessionExpired) {
		t.Fatalf("in modalità credenziali serve il login: %v", err)
	}
}

func TestCredentials(t *testing.T) {
	e := evento.New("Produzione", []string{"Video", "Audio"})
	e.Mode = evento.ModeCredenziali
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(e.SetUser(evento.User{Username: "capovideo", Name: "Paolo", Role: evento.RoleAdmin, Channels: []string{"video"}}, "pppp"))
	must(e.SetUser(evento.User{Username: "cam1", Name: "Marco", Role: evento.RoleUser, Channels: []string{"video"}}, "mmmm"))
	must(e.SetUser(evento.User{Username: "jolly", Name: "Jolly", Role: evento.RoleUser, Channels: []string{"video", "audio"}}, "jjjj"))
	must(e.SetUser(evento.User{Username: "vuoto", Role: evento.RoleUser}, "vvvv"))
	r := testRoom(e)

	if _, err := r.Login("cam1", "sbagliata"); !errors.Is(err, ErrBadLogin) {
		t.Fatalf("login sbagliato: %v", err)
	}
	login := func(u, p string) string {
		tok, err := r.Login(u, p)
		if err != nil {
			t.Fatal(err)
		}
		return tok
	}
	res, _, err := join(t, r, JoinRequest{Session: login("CapoVideo", "pppp")})
	if err != nil || res.name != "Paolo" || res.role != RoleCaporeparto || !eq(keys(r, res.member), "produzione", "video") || !eq(keys(r, res.admin), "video") {
		t.Fatalf("caporeparto: %+v %v", res, err)
	}
	res, _, err = join(t, r, JoinRequest{Session: login("cam1", "mmmm")})
	if err != nil || res.role != RoleTecnico || !eq(keys(r, res.member), "video") {
		t.Fatalf("tecnico: %+v %v", res, err)
	}
	res, _, err = join(t, r, JoinRequest{Session: login("jolly", "jjjj")})
	if err != nil || !eq(keys(r, res.member), "video", "audio") || len(res.admin) != 0 {
		t.Fatalf("tecnico su due canali: %+v %v", res, err)
	}
	if _, _, err := join(t, r, JoinRequest{Session: login("vuoto", "vvvv")}); err == nil {
		t.Fatal("un utente senza canali non deve entrare")
	}
	if _, _, err := join(t, r, JoinRequest{Session: "inventato"}); !errors.Is(err, ErrSessionExpired) {
		t.Fatalf("sessione inventata: %v", err)
	}
	// Cambio di modalità: le sessioni vengono annullate.
	tok := login("cam1", "mmmm")
	if err := r.UpdateEvent(func(e *evento.Event) error { e.Mode = evento.ModeLibero; return nil }); err != nil {
		t.Fatal(err)
	}
	r.event.Mode = evento.ModeCredenziali
	if _, _, err := join(t, r, JoinRequest{Session: tok}); !errors.Is(err, ErrSessionExpired) {
		t.Fatal("dopo un cambio di modalità le vecchie sessioni non valgono più")
	}
}

func TestMicPermissions(t *testing.T) {
	r := testRoom(evento.New("Produzione", []string{"Video"}))
	capo := &Peer{member: map[string]bool{"produzione": true, "video": true}, admin: map[string]bool{"video": true}}
	cam := &Peer{member: map[string]bool{"video": true}, admin: map[string]bool{}}

	for _, mode := range []string{evento.MicLibero, evento.MicPTT} {
		r.event.Channel("video").MicMode = mode
		if !r.canTalkLocked(cam, "video") {
			t.Fatalf("modalità %s: il tecnico deve poter parlare", mode)
		}
	}
	r.event.Channel("video").MicMode = evento.MicAdmin
	if r.canTalkLocked(cam, "video") {
		t.Fatal("modalità admin: il tecnico non deve aprirsi il microfono da solo")
	}
	if !r.canTalkLocked(capo, "video") {
		t.Fatal("il caporeparto parla sempre nel suo canale")
	}
	if r.canTalkLocked(cam, "produzione") {
		t.Fatal("nessuno parla dove non è membro")
	}
}

func TestEventCopyHidesSecrets(t *testing.T) {
	e := evento.New("Produzione", nil)
	e.Mode = evento.ModeCredenziali
	_ = e.SetUser(evento.User{Username: "a", Role: evento.RoleUser}, "segreta")
	r := testRoom(e)
	if r.Event().Users[0].PasswordHash != "" {
		t.Fatal("la copia dell'evento non deve contenere le password")
	}
	if r.event.Users[0].PasswordHash == "" {
		t.Fatal("la copia non deve modificare l'originale")
	}
}
