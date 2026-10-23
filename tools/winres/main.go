// Comando winres: crea rsrc_windows_amd64.syso con icona e informazioni di
// versione dell'eseguibile Windows. Go lo include da solo nella compilazione.
//
//	go run ./tools/winres -version 1.2.0
package main

import (
	"flag"
	"log"
	"os"
	"strings"

	"github.com/tc-hib/winres"
	"github.com/tc-hib/winres/version"

	"github.com/battacharlie/charlie26/internal/icona"
)

func main() {
	ver := flag.String("version", "0.0.0", "versione (es. 1.2.0 o v1.2.0)")
	out := flag.String("out", "rsrc_windows_amd64.syso", "file da creare")
	flag.Parse()
	v := strings.TrimPrefix(*ver, "v")

	rs := winres.ResourceSet{}
	ic, err := winres.NewIconFromResizedImage(icona.App(256), []int{16, 24, 32, 48, 64, 128, 256})
	if err != nil {
		log.Fatal(err)
	}
	if err := rs.SetIcon(winres.Name("APP"), ic); err != nil {
		log.Fatal(err)
	}
	vi := version.Info{}
	vi.SetFileVersion(v)
	vi.SetProductVersion(v)
	for k, val := range map[string]string{
		version.ProductName:      "CHARLIE26 – Intercom Evoluto",
		version.FileDescription:  "CHARLIE26 – Intercom Evoluto",
		version.OriginalFilename: "charlie26.exe",
		version.CompanyName:      "Andrea Battaglioli – MyWebTvLive.com",
		version.LegalCopyright:   "Andrea Battaglioli – www.andreabattaglioli.it – MyWebTvLive.com – Made in Bolognina",
	} {
		_ = vi.Set(0x0410, k, val) // italiano
	}
	rs.SetVersionInfo(vi)

	f, err := os.Create(*out)
	if err != nil {
		log.Fatal(err)
	}
	defer f.Close()
	if err := rs.WriteObject(f, winres.ArchAMD64); err != nil {
		log.Fatal(err)
	}
	log.Printf("creato %s (versione %s)", *out, v)
}
