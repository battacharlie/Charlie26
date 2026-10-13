package certs

import (
	"bytes"
	"crypto/x509"
	"testing"
)

func verify(t *testing.T, l *Local, host string) error {
	t.Helper()
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(l.CAPEM) {
		t.Fatal("CA PEM non leggibile")
	}
	leaf, err := x509.ParseCertificate(l.Server.Certificate[0])
	if err != nil {
		t.Fatal(err)
	}
	_, err = leaf.Verify(x509.VerifyOptions{DNSName: host, Roots: pool})
	return err
}

func TestEnsureLocalCoversHostsAndIsStable(t *testing.T) {
	dir := t.TempDir()
	l, err := EnsureLocal(dir, []string{"192.168.1.10"})
	if err != nil {
		t.Fatal(err)
	}
	for _, h := range []string{"192.168.1.10", "localhost", "127.0.0.1"} {
		if err := verify(t, l, h); err != nil {
			t.Fatalf("host %s non valido: %v", h, err)
		}
	}
	if verify(t, l, "10.0.0.5") == nil {
		t.Fatal("un IP non richiesto non dovrebbe essere valido")
	}

	// Secondo avvio con gli stessi IP: stessa CA e stesso certificato.
	l2, err := EnsureLocal(dir, []string{"192.168.1.10"})
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(l.CAPEM, l2.CAPEM) || !bytes.Equal(l.Server.Certificate[0], l2.Server.Certificate[0]) {
		t.Fatal("al riavvio CA e certificato devono restare uguali")
	}

	// Cambia rete: stessa CA (i telefoni restano fidati), nuovo certificato.
	l3, err := EnsureLocal(dir, []string{"10.0.0.5"})
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(l.CAPEM, l3.CAPEM) {
		t.Fatal("la CA non deve cambiare quando cambia l'IP")
	}
	if err := verify(t, l3, "10.0.0.5"); err != nil {
		t.Fatalf("nuovo IP non coperto: %v", err)
	}
}
