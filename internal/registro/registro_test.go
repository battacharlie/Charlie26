package registro

import (
	"bytes"
	"fmt"
	"testing"
)

func TestKeepsLastLines(t *testing.T) {
	var copyOut bytes.Buffer
	r := New(3, &copyOut)
	fmt.Fprint(r, "uno\ndue\n")
	fmt.Fprint(r, "tre\nqua")
	fmt.Fprint(r, "ttro\ncinque\n")
	got := r.Lines()
	want := []string{"tre", "quattro", "cinque"}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("righe %q, attese %q", got, want)
	}
	if copyOut.String() != "uno\ndue\ntre\nquattro\ncinque\n" {
		t.Fatalf("copia: %q", copyOut.String())
	}
}
