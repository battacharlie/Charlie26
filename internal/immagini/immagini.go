// Package immagini controlla e conserva le immagini caricate dalla console e
// dai telefoni (sfondi, icone dei canali), nella cartella dati.
package immagini

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"image"
	_ "image/jpeg" // formati accettati
	_ "image/png"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// Limiti per tipo di immagine.
const (
	MaxBackgroundBytes = 900 << 10
	MaxBackgroundSide  = 2600
	MaxIconBytes       = 96 << 10
	MaxIconSide        = 512
)

var nameRE = regexp.MustCompile(`^[0-9a-f]{20}\.(jpg|png)$`)

// ValidName: il nome è uno di quelli generati da Save (niente percorsi strani).
func ValidName(name string) bool { return nameRE.MatchString(name) }

// Parse controlla un'immagine inviata come data URL: solo JPEG e PNG,
// davvero decodificabili, entro i limiti indicati.
func Parse(dataURL string, maxBytes, maxSide int) ([]byte, string, error) {
	var ext string
	switch {
	case strings.HasPrefix(dataURL, "data:image/jpeg;base64,"):
		ext = "jpg"
	case strings.HasPrefix(dataURL, "data:image/png;base64,"):
		ext = "png"
	default:
		return nil, "", errors.New("formato immagine non supportato (servono JPEG o PNG)")
	}
	raw, err := base64.StdEncoding.DecodeString(dataURL[strings.Index(dataURL, ",")+1:])
	if err != nil {
		return nil, "", errors.New("immagine non leggibile")
	}
	if len(raw) > maxBytes {
		return nil, "", errors.New("immagine troppo grande")
	}
	cfg, format, err := image.DecodeConfig(bytes.NewReader(raw))
	if err != nil || (format == "jpeg") != (ext == "jpg") || (format == "png") != (ext == "png") {
		return nil, "", errors.New("il file non è un'immagine valida")
	}
	if cfg.Width == 0 || cfg.Height == 0 || cfg.Width > maxSide || cfg.Height > maxSide {
		return nil, "", errors.New("immagine troppo grande")
	}
	return raw, ext, nil
}

// Store è la cartella delle immagini.
type Store struct{ Dir string }

// Save controlla e salva un'immagine; il nome dipende dal contenuto, quindi
// la stessa immagine caricata due volte occupa spazio una volta sola.
func (s Store) Save(dataURL string, maxBytes, maxSide int) (string, error) {
	raw, ext, err := Parse(dataURL, maxBytes, maxSide)
	if err != nil {
		return "", err
	}
	return s.SaveRaw(raw, ext)
}

// SaveRaw salva byte già controllati.
func (s Store) SaveRaw(raw []byte, ext string) (string, error) {
	sum := sha256.Sum256(raw)
	name := hex.EncodeToString(sum[:10]) + "." + ext
	if err := os.MkdirAll(s.Dir, 0o700); err != nil {
		return "", err
	}
	path := filepath.Join(s.Dir, name)
	if _, err := os.Stat(path); err == nil {
		return name, nil
	}
	return name, os.WriteFile(path, raw, 0o600)
}

// Read restituisce un'immagine salvata.
func (s Store) Read(name string) ([]byte, string, error) {
	if !ValidName(name) {
		return nil, "", os.ErrNotExist
	}
	b, err := os.ReadFile(filepath.Join(s.Dir, name))
	mime := "image/png"
	if strings.HasSuffix(name, ".jpg") {
		mime = "image/jpeg"
	}
	return b, mime, err
}

// Exists dice se l'immagine è nell'archivio.
func (s Store) Exists(name string) bool {
	if !ValidName(name) {
		return false
	}
	_, err := os.Stat(filepath.Join(s.Dir, name))
	return err == nil
}
