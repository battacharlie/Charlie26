// Package registro tiene gli ultimi messaggi del programma in memoria
// (per mostrarli nella console di regia) e li scrive anche su file.
package registro

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// Registro è un io.Writer che conserva le ultime righe scritte.
type Registro struct {
	mu    sync.Mutex
	lines []string
	max   int
	part  string
	out   []io.Writer
}

// New crea un registro che conserva max righe e le inoltra anche a out.
func New(max int, out ...io.Writer) *Registro {
	return &Registro{max: max, out: out}
}

// Write implementa io.Writer.
func (r *Registro) Write(p []byte) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, w := range r.out {
		_, _ = w.Write(p) // un'uscita guasta (es. nessuna console) non deve bloccare il resto
	}
	text := r.part + string(p)
	parts := strings.Split(text, "\n")
	r.part = parts[len(parts)-1]
	for _, l := range parts[:len(parts)-1] {
		r.lines = append(r.lines, l)
	}
	if over := len(r.lines) - r.max; over > 0 {
		r.lines = append([]string(nil), r.lines[over:]...)
	}
	return len(p), nil
}

// Lines restituisce una copia delle ultime righe.
func (r *Registro) Lines() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.lines...)
}

// OpenFile apre il file di registro nella cartella dati. Il registro della
// volta precedente viene conservato come intercom.log.1.
func OpenFile(dir string) (*os.File, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	path := filepath.Join(dir, "intercom.log")
	_ = os.Rename(path, path+".1")
	return os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
}
