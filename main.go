// CHARLIE26 – Intercom Evoluto: intercom di produzione via WebRTC.
//
// Si lancia sulla macchina di regia; i tecnici si collegano dal browser
// dello smartphone inquadrando il QR code mostrato nella console.
package main

import (
	"crypto/tls"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/battacharlie/charlie26/internal/certs"
	"github.com/battacharlie/charlie26/internal/evento"
	"github.com/battacharlie/charlie26/internal/immagini"
	"github.com/battacharlie/charlie26/internal/netinfo"
	"github.com/battacharlie/charlie26/internal/registro"
	"github.com/battacharlie/charlie26/internal/server"
	"github.com/battacharlie/charlie26/internal/sfu"
	"github.com/battacharlie/charlie26/web"
)

var version = "sviluppo"

func main() {
	var (
		httpsPort = flag.Int("porta", 8443, "porta HTTPS (pagine e signaling)")
		httpPort  = flag.Int("porta-aiuto", 8080, "porta HTTP per la pagina di aiuto e il download della CA (0 = disattiva)")
		udpPort   = flag.Int("porta-udp", 8443, "porta UDP per audio e video WebRTC")
		ipFlag    = flag.String("ip", "", "IP da usare nei QR code (vuoto = rilevamento automatico)")
		dataDir   = flag.String("dati", defaultDataDir(), "cartella dati (certificati, eventi)")
		certFile  = flag.String("cert", "", "certificato del tuo dominio (opzione B), file .crt/.pem con la catena completa")
		keyFile   = flag.String("key", "", "chiave privata del certificato di dominio")
		domain    = flag.String("dominio", "", "dominio per l'opzione B, es. intercom.esempio.it: i QR useranno 192-168-1-10.intercom.esempio.it")
		mainName  = flag.String("principale", "Produzione", "nome del canale principale (solo per un evento nuovo)")
		depts     = flag.String("canali", "Video,Audio,Luci", "reparti separati da virgola (solo per un evento nuovo)")
		noBrowser = flag.Bool("no-browser", false, "non aprire automaticamente la console nel browser")
	)
	flag.Parse()

	// Doppio clic con il programma già acceso: si apre la sua console e basta.
	if alreadyRunning(*httpsPort) {
		return
	}

	// Registro: finestra (se c'è), file intercom.log nella cartella dati e
	// memoria (lo mostra la console di regia nella scheda Registro).
	outs := []io.Writer{os.Stdout}
	if f, err := registro.OpenFile(*dataDir); err == nil {
		defer f.Close()
		outs = append(outs, f)
	}
	reg = registro.New(500, outs...)
	log.SetOutput(reg)
	log.SetFlags(log.Ltime)

	say("\n  CHARLIE26 – Intercom Evoluto %s\n  Andrea Battaglioli – www.andreabattaglioli.it · ABit/s · Made in Bolognina\n  ─────────────────────────────\n", version)

	ips := netinfo.Detect()
	settingsPath := filepath.Join(*dataDir, "impostazioni.json")
	settings := loadSettings(settingsPath)
	host := *ipFlag
	if host == "" && settings.IP != "" {
		// La scheda scelta l'ultima volta dalla console, se è ancora attiva.
		for _, a := range ips {
			if a.IP == settings.IP {
				host = a.IP
			}
		}
	}
	if host == "" && len(ips) > 0 {
		host = ips[0].IP
	}
	if host == "" {
		host = "127.0.0.1"
		say("  ⚠ Nessuna scheda di rete attiva: funziona solo su questo computer.\n")
	}
	say("  Schede di rete trovate:\n")
	for _, a := range ips {
		mark := " "
		if a.IP == host {
			mark = "►"
		}
		say("   %s %-15s  %s\n", mark, a.IP, a.Interface)
	}

	// Certificati HTTPS.
	certDir := filepath.Join(*dataDir, "certificati")
	var (
		tlsCert  tls.Certificate
		certMode = "locale"
		caPEM    []byte
	)
	customCert, customKey := *certFile, *keyFile
	if customCert == "" { // file messi nella cartella dati senza bisogno di opzioni
		c, k := filepath.Join(certDir, "dominio.crt"), filepath.Join(certDir, "dominio.key")
		if fileExists(c) && fileExists(k) {
			customCert, customKey = c, k
		}
	}
	if customCert != "" {
		pair, err := certs.LoadCustom(customCert, customKey)
		if err != nil {
			fatal("certificato di dominio non valido: %v", err)
		}
		certMode = "dominio"
		tlsCert = pair
		if *domain != "" {
			host = domainHost(host, *domain)
		}
		if err := pair.Leaf.VerifyHostname(host); err != nil {
			say("  ⚠ Il certificato di dominio non copre %q: i telefoni vedranno un avviso.\n", host)
		}
		say("  HTTPS: certificato di dominio (%s)\n", filepath.Base(customCert))
	} else {
		hosts := make([]string, 0, len(ips)+1)
		for _, a := range ips {
			hosts = append(hosts, a.IP)
		}
		if *ipFlag != "" {
			hosts = append(hosts, *ipFlag)
		}
		if hn, err := os.Hostname(); err == nil && hn != "" {
			hosts = append(hosts, hn)
		}
		if settings.IP != "" {
			hosts = append(hosts, settings.IP)
		}
		local, err := certs.EnsureLocal(certDir, hosts)
		if err != nil {
			fatal("impossibile creare i certificati: %v", err)
		}
		tlsCert = local.Server
		caPEM = local.CAPEM
		say("  HTTPS: CA locale (cartella %s)\n", certDir)
	}

	// Evento: si riparte da quello salvato, altrimenti se ne crea uno nuovo.
	eventPath := filepath.Join(*dataDir, "evento.json")
	ev, err := evento.Load(eventPath)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			say("  ⚠ %v: ne creo uno nuovo (il vecchio resta in evento.json.rotto)\n", err)
			_ = os.Rename(eventPath, eventPath+".rotto")
		}
		ev = evento.New(*mainName, strings.Split(*depts, ","))
		if err := ev.Save(eventPath); err != nil {
			fatal("impossibile salvare l'evento in %s: %v", eventPath, err)
		}
	}
	room, err := sfu.NewRoom(sfu.Config{
		UDPPort: *udpPort,
		Event:   ev,
		Save:    func(e *evento.Event) error { return e.Save(eventPath) },
	})
	if err != nil {
		if alreadyRunning(*httpsPort) {
			return
		}
		fatal("%v\nUn altro programma usa già la porta. Prova con l'opzione -porta-udp 8500.", err)
	}
	defer room.Close()

	quit := make(chan struct{})
	var quitOnce sync.Once
	var currentCert atomic.Pointer[tls.Certificate] // sostituibile se cambia la rete
	currentCert.Store(&tlsCert)

	srv := server.New(server.Config{
		Host:       host,
		HTTPSPort:  *httpsPort,
		HTTPPort:   *httpPort,
		CertMode:   certMode,
		CAPEM:      caPEM,
		Web:        web.FS,
		Room:       room,
		IPs:        ips,
		Log:        reg.Lines,
		Shutdown:   func() { quitOnce.Do(func() { close(quit) }) },
		DetectIPs:  netinfo.Detect,
		ModelsDir:  filepath.Join(*dataDir, "modelli"),
		RemoteFile: filepath.Join(*dataDir, "regia-remota.json"),
		Images:     immagini.Store{Dir: filepath.Join(*dataDir, "immagini")},
		Version:    version,
		ChangeIP: func(ip string) (string, error) {
			newHost := ip
			if certMode == "dominio" {
				if *domain != "" {
					newHost = domainHost(ip, *domain)
				}
			} else {
				// Il certificato deve coprire anche il nuovo IP (stessa CA: i
				// telefoni che l'hanno installata restano a posto).
				hosts := []string{ip}
				for _, a := range netinfo.Detect() {
					hosts = append(hosts, a.IP)
				}
				local, err := certs.EnsureLocal(certDir, hosts)
				if err != nil {
					return "", err
				}
				currentCert.Store(&local.Server)
			}
			settings.IP = ip
			if err := saveSettings(settingsPath, settings); err != nil {
				log.Printf("impossibile salvare le impostazioni: %v", err)
			}
			log.Printf("rete cambiata dalla console: i QR ora puntano a %s", newHost)
			return newHost, nil
		},
	})

	httpsLn, err := net.Listen("tcp", ":"+strconv.Itoa(*httpsPort))
	if err != nil {
		if alreadyRunning(*httpsPort) {
			return
		}
		fatal("La porta HTTPS %d è occupata da un altro programma (%v).\nProva con l'opzione -porta 9443.", *httpsPort, err)
	}
	hs := &http.Server{
		Handler: srv.HTTPSHandler(),
		TLSConfig: &tls.Config{
			MinVersion:     tls.VersionTLS12,
			GetCertificate: func(*tls.ClientHelloInfo) (*tls.Certificate, error) { return currentCert.Load(), nil },
		},
		ReadHeaderTimeout: 10 * time.Second,
		ErrorLog:          log.New(tlsNoise{}, "", 0),
	}
	go func() {
		if err := hs.ServeTLS(httpsLn, "", ""); err != nil && !errors.Is(err, http.ErrServerClosed) {
			fatal("server HTTPS: %v", err)
		}
	}()

	if *httpPort > 0 {
		hl, err := net.Listen("tcp", ":"+strconv.Itoa(*httpPort))
		if err != nil {
			say("  ⚠ Porta di aiuto %d occupata: pagina di aiuto in HTTP non disponibile.\n", *httpPort)
		} else {
			go http.Serve(hl, srv.HTTPHandler())
		}
	}

	say("\n  Console di regia:  %s\n", srv.ConsoleURL())
	say("  App tecnici:       %s\n", srv.AppURL())
	if *httpPort > 0 {
		say("  Aiuto certificato: %s\n", srv.HelpURL())
	}
	say("  Audio/video WebRTC sulla porta UDP %d\n", *udpPort)
	names := make([]string, len(ev.Channels))
	for i, c := range ev.Channels {
		names[i] = c.Name
	}
	mode := map[string]string{evento.ModeLibero: "libero accesso", evento.ModeCredenziali: "accesso con credenziali"}[ev.Mode]
	say("  Evento %q, %s. Canali: %s\n\n", ev.Name, mode, strings.Join(names, ", "))
	say("  Se Windows chiede di consentire l'accesso alla rete, scegli\n  \"Reti private\" e premi Consenti.\n")
	say("%s\n\n", desktopHint)

	if !*noBrowser {
		openBrowser(srv.ConsoleURL())
	}

	runDesktop(desktop{consoleURL: srv.ConsoleURL(), appURL: srv.AppURL(), dataDir: *dataDir, quit: quit})
	say("\n  Spegnimento…\n")
	_ = hs.Close()
}

// domainHost: 192.168.1.10 + intercom.esempio.it -> 192-168-1-10.intercom.esempio.it
func domainHost(ip, domain string) string {
	return strings.ReplaceAll(ip, ".", "-") + "." + strings.TrimPrefix(domain, "*.")
}

// settings sono le scelte fatte dalla console che valgono anche al riavvio.
type settings struct {
	IP string `json:"ip,omitempty"` // scheda di rete scelta per i QR
}

func loadSettings(path string) *settings {
	s := &settings{}
	if b, err := os.ReadFile(path); err == nil {
		_ = json.Unmarshal(b, s)
	}
	return s
}

func saveSettings(path string, s *settings) error {
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, b, 0o600)
}

// reg è il registro dei messaggi (finestra, file e console di regia).
var reg = registro.New(500, os.Stdout)

func say(format string, a ...any) { fmt.Fprintf(reg, format, a...) }

// alreadyRunning: se un'altra copia del programma è già accesa su questa
// porta, apre la sua console invece di dare errore (capita facendo doppio
// clic due volte, soprattutto ora che su Windows non c'è la finestra).
func alreadyRunning(port int) bool {
	c := http.Client{Timeout: 2 * time.Second, Transport: &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}} //nolint:gosec // solo verso localhost
	resp, err := c.Get(fmt.Sprintf("https://localhost:%d/api/info", port))
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	var info struct{ AppURL string }
	if json.NewDecoder(resp.Body).Decode(&info) != nil || info.AppURL == "" {
		return false
	}
	say("  Il programma è già acceso: apro la sua console.\n")
	openBrowser(fmt.Sprintf("https://localhost:%d/regia", port))
	return true
}

func defaultDataDir() string {
	d, err := os.UserConfigDir()
	if err != nil {
		return "dati-charlie26"
	}
	dir := filepath.Join(d, "Charlie26")
	// Le versioni precedenti usavano "IntercomRegia": la si riprende così com'è
	// (evento, utenti e certificato già installato sui telefoni).
	old := filepath.Join(d, "IntercomRegia")
	if _, err := os.Stat(dir); os.IsNotExist(err) {
		if _, err := os.Stat(old); err == nil {
			if os.Rename(old, dir) != nil {
				return old
			}
		}
	}
	return dir
}

func fileExists(p string) bool {
	st, err := os.Stat(p)
	return err == nil && !st.IsDir()
}

func fatal(format string, a ...any) {
	msg := fmt.Sprintf(format, a...)
	log.Print("ERRORE: " + msg)
	showError(msg)
	os.Exit(1)
}

func openBrowser(url string) {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	case "darwin":
		cmd = exec.Command("open", url)
	default:
		cmd = exec.Command("xdg-open", url)
	}
	_ = cmd.Start()
}

// tlsNoise nasconde i messaggi "TLS handshake error" che compaiono ogni volta
// che un telefono vede l'avviso del certificato: sono normali, non errori.
type tlsNoise struct{}

func (tlsNoise) Write(p []byte) (int, error) {
	if !strings.Contains(string(p), "TLS handshake error") {
		log.Print(string(p))
	}
	return len(p), nil
}
