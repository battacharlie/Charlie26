// Package portacondivisa fa convivire HTTPS e ICE-TCP sulla stessa porta TCP.
//
// Negli alberghi e nelle reti aziendali il traffico UDP è spesso bloccato,
// mentre la porta 443/TCP è quasi sempre aperta. WebRTC può ripiegare su TCP
// ("ICE-TCP"), ma la porta 443 è già usata dalle pagine HTTPS. Qui si guarda il
// primo byte di ogni connessione: una connessione TLS (HTTPS) comincia sempre
// con 0x16, mentre ICE-TCP comincia con la lunghezza (2 byte) del primo
// messaggio STUN, che è piccolo, quindi con 0x00. Le due strade ricevono ciascuna
// il proprio net.Listener.
package portacondivisa

import (
	"bufio"
	"errors"
	"net"
	"sync"
	"time"
)

const (
	tlsHandshake = 0x16
	attesaPrimo  = 5 * time.Second
)

// Divisore smista le connessioni in arrivo su un unico listener.
type Divisore struct {
	ln     net.Listener
	tls    *filtro
	ice    *filtro
	chiuso chan struct{}
	once   sync.Once
	mu     sync.Mutex
	errAcc error
}

type filtro struct {
	d  *Divisore
	ch chan net.Conn
}

// Nuovo prende in carico il listener e avvia lo smistamento.
func Nuovo(ln net.Listener) *Divisore {
	d := &Divisore{ln: ln, chiuso: make(chan struct{})}
	d.tls = &filtro{d: d, ch: make(chan net.Conn)}
	d.ice = &filtro{d: d, ch: make(chan net.Conn)}
	go d.accetta()
	return d
}

// TLS è il listener per HTTPS.
func (d *Divisore) TLS() net.Listener { return d.tls }

// ICE è il listener per le connessioni ICE-TCP di WebRTC.
func (d *Divisore) ICE() net.Listener { return d.ice }

func (d *Divisore) accetta() {
	for {
		c, err := d.ln.Accept()
		if err != nil {
			d.mu.Lock()
			if d.errAcc == nil {
				d.errAcc = err
			}
			d.mu.Unlock()
			d.chiudi()
			return
		}
		go d.smista(c)
	}
}

func (d *Divisore) smista(c net.Conn) {
	_ = c.SetReadDeadline(time.Now().Add(attesaPrimo))
	br := bufio.NewReaderSize(c, 4096)
	b, err := br.Peek(1)
	if err != nil {
		c.Close()
		return
	}
	_ = c.SetReadDeadline(time.Time{})
	dest := d.ice
	if b[0] == tlsHandshake {
		dest = d.tls
	}
	pc := &connLetta{Conn: c, r: br}
	select {
	case dest.ch <- pc:
	case <-d.chiuso:
		c.Close()
	case <-time.After(attesaPrimo):
		c.Close() // nessuno sta ascoltando da questa parte
	}
}

func (d *Divisore) chiudi() {
	d.once.Do(func() {
		close(d.chiuso)
		d.ln.Close()
	})
}

func (f *filtro) Accept() (net.Conn, error) {
	select {
	case c := <-f.ch:
		return c, nil
	case <-f.d.chiuso:
		f.d.mu.Lock()
		err := f.d.errAcc
		f.d.mu.Unlock()
		if err != nil {
			return nil, err
		}
		return nil, errors.New("listener chiuso")
	}
}

func (f *filtro) Close() error   { f.d.chiudi(); return nil }
func (f *filtro) Addr() net.Addr { return f.d.ln.Addr() }

// connLetta rilegge anche il byte già sbirciato.
type connLetta struct {
	net.Conn
	r *bufio.Reader
}

func (c *connLetta) Read(p []byte) (int, error) { return c.r.Read(p) }
