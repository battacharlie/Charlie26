// Package certs gestisce i certificati HTTPS.
//
// Due modalità:
//   - "locale" (opzione A): al primo avvio crea una CA locale (autorità di
//     certificazione) e con questa firma un certificato per gli IP della macchina.
//     I telefoni accettano l'avviso una volta, oppure installano la CA.
//   - "dominio" (opzione B): si usa un certificato vero (es. Let's Encrypt)
//     per un dominio che risolve sugli IP locali. Nessun avviso.
package certs

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"time"
)

const (
	caCertFile     = "ca.crt"
	caKeyFile      = "ca.key"
	serverCertFile = "server.crt"
	serverKeyFile  = "server.key"

	// iOS rifiuta certificati server con validità oltre 825 giorni:
	// restiamo ben sotto e rigeneriamo da soli quando si avvicina la scadenza.
	serverValidity = 397 * 24 * time.Hour
	caValidity     = 10 * 365 * 24 * time.Hour
	renewBefore    = 30 * 24 * time.Hour
)

// Local è la CA locale con il certificato server corrente.
type Local struct {
	Dir    string
	CAPEM  []byte
	ca     *x509.Certificate
	caKey  *ecdsa.PrivateKey
	Server tls.Certificate
}

// EnsureLocal carica (o crea) la CA in dir e un certificato server valido
// per tutti gli host indicati (IP o nomi). Se il certificato esistente non
// copre gli host, sta per scadere o non è firmato dalla CA, viene rigenerato.
func EnsureLocal(dir string, hosts []string) (*Local, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	l := &Local{Dir: dir}
	if err := l.loadOrCreateCA(); err != nil {
		return nil, fmt.Errorf("CA locale: %w", err)
	}
	if err := l.ensureServer(hosts); err != nil {
		return nil, fmt.Errorf("certificato server: %w", err)
	}
	return l, nil
}

func (l *Local) loadOrCreateCA() error {
	certPath := filepath.Join(l.Dir, caCertFile)
	keyPath := filepath.Join(l.Dir, caKeyFile)
	if cert, key, err := readPair(certPath, keyPath); err == nil && time.Now().Before(cert.NotAfter) {
		l.ca, l.caKey = cert, key
		l.CAPEM, _ = os.ReadFile(certPath)
		return nil
	}

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return err
	}
	host, _ := os.Hostname()
	tmpl := &x509.Certificate{
		SerialNumber: randomSerial(),
		Subject: pkix.Name{
			CommonName:   "Intercom Regia CA (" + host + ")",
			Organization: []string{"Intercom Regia"},
		},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(caValidity),
		IsCA:                  true,
		BasicConstraintsValid: true,
		MaxPathLenZero:        true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign | x509.KeyUsageDigitalSignature,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return err
	}
	cert, _ := x509.ParseCertificate(der)
	if err := writePair(certPath, keyPath, der, key); err != nil {
		return err
	}
	// Una nuova CA invalida qualsiasi certificato server precedente.
	_ = os.Remove(filepath.Join(l.Dir, serverCertFile))
	l.ca, l.caKey = cert, key
	l.CAPEM = pemCert(der)
	return nil
}

func (l *Local) ensureServer(hosts []string) error {
	certPath := filepath.Join(l.Dir, serverCertFile)
	keyPath := filepath.Join(l.Dir, serverKeyFile)
	if cert, _, err := readPair(certPath, keyPath); err == nil && l.serverOK(cert, hosts) {
		pair, err := tls.LoadX509KeyPair(certPath, keyPath)
		if err == nil {
			l.Server = pair
			return nil
		}
	}

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return err
	}
	tmpl := &x509.Certificate{
		SerialNumber: randomSerial(),
		Subject:      pkix.Name{CommonName: "Intercom Regia", Organization: []string{"Intercom Regia"}},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(serverValidity),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	for _, h := range withLocalhost(hosts) {
		if ip := net.ParseIP(h); ip != nil {
			tmpl.IPAddresses = append(tmpl.IPAddresses, ip)
		} else {
			tmpl.DNSNames = append(tmpl.DNSNames, h)
		}
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, l.ca, &key.PublicKey, l.caKey)
	if err != nil {
		return err
	}
	if err := writePair(certPath, keyPath, der, key); err != nil {
		return err
	}
	l.Server, err = tls.LoadX509KeyPair(certPath, keyPath)
	return err
}

func (l *Local) serverOK(cert *x509.Certificate, hosts []string) bool {
	if time.Until(cert.NotAfter) < renewBefore {
		return false
	}
	if cert.CheckSignatureFrom(l.ca) != nil {
		return false
	}
	for _, h := range withLocalhost(hosts) {
		if cert.VerifyHostname(h) != nil {
			return false
		}
	}
	return true
}

// LoadCustom carica un certificato fornito dall'utente (opzione B).
func LoadCustom(certFile, keyFile string) (tls.Certificate, error) {
	pair, err := tls.LoadX509KeyPair(certFile, keyFile)
	if err != nil {
		return pair, err
	}
	leaf, err := x509.ParseCertificate(pair.Certificate[0])
	if err != nil {
		return pair, err
	}
	if time.Now().After(leaf.NotAfter) {
		return pair, fmt.Errorf("il certificato %s è scaduto il %s", certFile, leaf.NotAfter.Format("02/01/2006"))
	}
	pair.Leaf = leaf
	return pair, nil
}

func withLocalhost(hosts []string) []string {
	out := []string{"localhost", "127.0.0.1"}
	seen := map[string]bool{"localhost": true, "127.0.0.1": true}
	for _, h := range hosts {
		if h != "" && !seen[h] {
			seen[h] = true
			out = append(out, h)
		}
	}
	return out
}

func readPair(certPath, keyPath string) (*x509.Certificate, *ecdsa.PrivateKey, error) {
	cb, err := os.ReadFile(certPath)
	if err != nil {
		return nil, nil, err
	}
	kb, err := os.ReadFile(keyPath)
	if err != nil {
		return nil, nil, err
	}
	cblock, _ := pem.Decode(cb)
	kblock, _ := pem.Decode(kb)
	if cblock == nil || kblock == nil {
		return nil, nil, errors.New("file PEM non valido")
	}
	cert, err := x509.ParseCertificate(cblock.Bytes)
	if err != nil {
		return nil, nil, err
	}
	key, err := x509.ParseECPrivateKey(kblock.Bytes)
	if err != nil {
		return nil, nil, err
	}
	return cert, key, nil
}

func writePair(certPath, keyPath string, der []byte, key *ecdsa.PrivateKey) error {
	kb, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return err
	}
	if err := os.WriteFile(keyPath, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: kb}), 0o600); err != nil {
		return err
	}
	return os.WriteFile(certPath, pemCert(der), 0o644)
}

func pemCert(der []byte) []byte {
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
}

func randomSerial() *big.Int {
	n, _ := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 126))
	return n
}
