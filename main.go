// Intercom di produzione via WebRTC.
//
// Si lancia sulla macchina di regia; i tecnici si collegano dal browser
// dello smartphone inquadrando il QR code mostrato nella console.
package main

import (
	"crypto/tls"
	"errors"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/battacharlie/charlie26/internal/certs"
	"github.com/battacharlie/charlie26/internal/netinfo"
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
		mainName  = flag.String("principale", "Produzione", "nome del canale principale")
		depts     = flag.String("canali", "Video,Audio,Luci", "reparti separati da virgola")
		noBrowser = flag.Bool("no-browser", false, "non aprire automaticamente la console nel browser")
	)
	flag.Parse()
	log.SetFlags(log.Ltime)

	fmt.Printf("\n  INTERCOM REGIA %s\n  ─────────────────────────────\n", version)

	ips := netinfo.Detect()
	host := *ipFlag
	if host == "" && len(ips) > 0 {
		host = ips[0].IP
	}
	if host == "" {
		host = "127.0.0.1"
		fmt.Println("  ⚠ Nessuna scheda di rete attiva: funziona solo su questo computer.")
	}
	fmt.Println("  Schede di rete trovate:")
	for _, a := range ips {
		mark := " "
		if a.IP == host {
			mark = "►"
		}
		fmt.Printf("   %s %-15s  %s\n", mark, a.IP, a.Interface)
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
			// 192.168.1.10 -> 192-168-1-10.intercom.esempio.it
			host = strings.ReplaceAll(host, ".", "-") + "." + strings.TrimPrefix(*domain, "*.")
		}
		if err := pair.Leaf.VerifyHostname(host); err != nil {
			fmt.Printf("  ⚠ Il certificato di dominio non copre %q: i telefoni vedranno un avviso.\n", host)
		}
		fmt.Printf("  HTTPS: certificato di dominio (%s)\n", filepath.Base(customCert))
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
		local, err := certs.EnsureLocal(certDir, hosts)
		if err != nil {
			fatal("impossibile creare i certificati: %v", err)
		}
		tlsCert = local.Server
		caPEM = local.CAPEM
		fmt.Println("  HTTPS: CA locale (cartella " + certDir + ")")
	}

	channels := sfu.ChannelsFromNames(*mainName, strings.Split(*depts, ","))
	room, err := sfu.NewRoom(sfu.Config{UDPPort: *udpPort, Channels: channels})
	if err != nil {
		fatal("%v\n  Un altro programma (o un'altra copia di questo) usa già la porta. Prova con -porta-udp 8500.", err)
	}
	defer room.Close()

	srv := server.New(server.Config{
		Host:      host,
		HTTPSPort: *httpsPort,
		HTTPPort:  *httpPort,
		CertMode:  certMode,
		CAPEM:     caPEM,
		Web:       web.FS,
		Room:      room,
		IPs:       ips,
	})

	httpsLn, err := net.Listen("tcp", ":"+strconv.Itoa(*httpsPort))
	if err != nil {
		fatal("porta HTTPS %d occupata: %v\n  Prova con -porta 9443.", *httpsPort, err)
	}
	hs := &http.Server{
		Handler:           srv.HTTPSHandler(),
		TLSConfig:         server.TLSConfig(tlsCert),
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
			fmt.Printf("  ⚠ Porta di aiuto %d occupata: pagina di aiuto in HTTP non disponibile.\n", *httpPort)
		} else {
			go http.Serve(hl, srv.HTTPHandler())
		}
	}

	fmt.Println()
	fmt.Println("  Console di regia:  " + srv.ConsoleURL())
	fmt.Println("  App tecnici:       " + srv.AppURL())
	if *httpPort > 0 {
		fmt.Println("  Aiuto certificato: " + srv.HelpURL())
	}
	fmt.Printf("  Audio/video WebRTC sulla porta UDP %d\n", *udpPort)
	names := make([]string, len(channels))
	for i, c := range channels {
		names[i] = c.Name
	}
	fmt.Println("  Canali: " + strings.Join(names, ", "))
	fmt.Println()
	fmt.Println("  Se Windows chiede di consentire l'accesso alla rete, scegli")
	fmt.Println("  \"Reti private\" e premi Consenti. Chiudi questa finestra per spegnere.")
	fmt.Println()

	if !*noBrowser {
		openBrowser(srv.ConsoleURL())
	}

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt)
	<-stop
	fmt.Println("\n  Spegnimento…")
	_ = hs.Close()
}

func defaultDataDir() string {
	if d, err := os.UserConfigDir(); err == nil {
		return filepath.Join(d, "IntercomRegia")
	}
	return "dati-intercom"
}

func fileExists(p string) bool {
	st, err := os.Stat(p)
	return err == nil && !st.IsDir()
}

func fatal(format string, a ...any) {
	fmt.Printf("\n  ERRORE: "+format+"\n\n", a...)
	if runtime.GOOS == "windows" {
		fmt.Println("  Premi Invio per chiudere.")
		_, _ = fmt.Scanln()
	}
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
