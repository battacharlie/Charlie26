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
	return &Peer{member: set(member), talk: set(talk), muted: set(muted), forced: map[string]bool{}}
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
