package sfu

import "testing"

func peer(member, talk, muted []string) *Peer {
	set := func(xs []string) map[string]bool {
		m := map[string]bool{}
		for _, x := range xs {
			m[x] = true
		}
		return m
	}
	return &Peer{member: set(member), talk: set(talk), muted: set(muted)}
}

func TestMembershipByRole(t *testing.T) {
	r := &Room{channels: DefaultChannels()}
	cases := []struct {
		role, ch string
		want     []string
	}{
		{RoleRegia, "", []string{"produzione", "video", "audio", "luci"}},
		{RoleCaporeparto, "video", []string{"produzione", "video"}},
		{RoleTecnico, "luci", []string{"luci"}},
		{RoleTecnico, "", []string{"produzione"}},
		{RoleCaporeparto, "produzione", []string{"produzione"}},
	}
	for _, c := range cases {
		m, err := r.membershipLocked(c.role, c.ch)
		if err != nil {
			t.Fatalf("%s/%s: %v", c.role, c.ch, err)
		}
		got := r.orderedLocked(m)
		if len(got) != len(c.want) {
			t.Fatalf("%s/%s: %v, attesi %v", c.role, c.ch, got, c.want)
		}
		for i := range got {
			if got[i] != c.want[i] {
				t.Fatalf("%s/%s: %v, attesi %v", c.role, c.ch, got, c.want)
			}
		}
	}
	if _, err := r.membershipLocked(RoleTecnico, "cucina"); err == nil {
		t.Fatal("un canale inesistente deve dare errore")
	}
	if _, err := r.membershipLocked("hacker", "video"); err == nil {
		t.Fatal("un ruolo sconosciuto deve dare errore")
	}
}

// Lo scenario tipico di un evento, verificato coppia per coppia.
func TestAudibleIntercomRules(t *testing.T) {
	all := []string{"produzione", "video", "audio", "luci"}
	regiaTalkVideo := peer(all, []string{"video"}, nil)
	regiaTalkAll := peer(all, all, nil)
	capoVideo := peer([]string{"produzione", "video"}, nil, nil)
	capoVideoTalkProd := peer([]string{"produzione", "video"}, []string{"produzione"}, nil)
	capoAudio := peer([]string{"produzione", "audio"}, nil, nil)
	cam1 := peer([]string{"video"}, nil, nil)
	cam2Talk := peer([]string{"video"}, []string{"video"}, nil)
	fonico := peer([]string{"audio"}, nil, nil)
	regiaNoVideo := peer(all, nil, []string{"video"})

	check := func(name string, from, to *Peer, want bool) {
		t.Helper()
		if got := audible(from, to); got != want {
			t.Errorf("%s: atteso %v, ottenuto %v", name, want, got)
		}
	}
	// Regia che parla al solo canale video.
	check("regia→video: cam1 sente", regiaTalkVideo, cam1, true)
	check("regia→video: capo video sente", regiaTalkVideo, capoVideo, true)
	check("regia→video: fonico NON sente", regiaTalkVideo, fonico, false)
	check("regia→video: capo audio NON sente", regiaTalkVideo, capoAudio, false)
	// Regia "parla a tutti".
	check("regia→tutti: fonico sente", regiaTalkAll, fonico, true)
	check("regia→tutti: capo audio sente", regiaTalkAll, capoAudio, true)
	// Caporeparto che parla in Produzione.
	check("capo video→produzione: capo audio sente", capoVideoTalkProd, capoAudio, true)
	check("capo video→produzione: regia sente", capoVideoTalkProd, peer(all, nil, nil), true)
	check("capo video→produzione: i suoi cameraman NON sentono", capoVideoTalkProd, cam1, false)
	check("capo video→produzione: fonico NON sente", capoVideoTalkProd, fonico, false)
	// Cameraman che parla nel suo reparto.
	check("cam2→video: cam1 sente", cam2Talk, cam1, true)
	check("cam2→video: capo video sente", cam2Talk, capoVideo, true)
	check("cam2→video: capo audio NON sente", cam2Talk, capoAudio, false)
	check("cam2→video: regia sente", cam2Talk, peer(all, nil, nil), true)
	check("cam2→video: regia con ascolto video spento NON sente", cam2Talk, regiaNoVideo, false)
	// Nessuno sente se stesso, chi tace non si sente.
	check("se stesso", cam2Talk, cam2Talk, false)
	check("chi tace", cam1, capoVideo, false)
}

func TestSubscribedNeedsSharedChannel(t *testing.T) {
	cam := peer([]string{"video"}, nil, nil)
	fonico := peer([]string{"audio"}, nil, nil)
	capo := peer([]string{"produzione", "video"}, nil, nil)
	if subscribed(cam, fonico) {
		t.Error("reparti diversi non devono scambiarsi flussi")
	}
	if !subscribed(cam, capo) || !subscribed(capo, cam) {
		t.Error("capo e cameraman condividono il canale video")
	}
}

func TestChannelsFromNames(t *testing.T) {
	chs := ChannelsFromNames("Regia", []string{"Video", " Luci ", "", "Città", "Video"})
	ids := []string{}
	for _, c := range chs {
		ids = append(ids, c.ID)
	}
	want := []string{"produzione", "video", "luci", "citta", "video-2"}
	if len(ids) != len(want) {
		t.Fatalf("%v", ids)
	}
	for i := range want {
		if ids[i] != want[i] {
			t.Fatalf("%v, attesi %v", ids, want)
		}
	}
	if chs[0].Name != "Regia" || !chs[0].Main {
		t.Fatalf("canale principale errato: %+v", chs[0])
	}
}
