package portacondivisa

import (
	"io"
	"net"
	"testing"
	"time"
)

func TestSmistamento(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	d := Nuovo(ln)
	defer d.TLS().Close()

	prova := func(primo byte, dest net.Listener) {
		t.Helper()
		c, err := net.Dial("tcp", ln.Addr().String())
		if err != nil {
			t.Fatal(err)
		}
		defer c.Close()
		msg := []byte{primo, 1, 2, 3}
		if _, err := c.Write(msg); err != nil {
			t.Fatal(err)
		}
		got := make(chan net.Conn, 1)
		go func() {
			a, _ := dest.Accept()
			got <- a
		}()
		select {
		case a := <-got:
			defer a.Close()
			buf := make([]byte, 4)
			a.SetReadDeadline(time.Now().Add(2 * time.Second))
			if _, err := io.ReadFull(a, buf); err != nil {
				t.Fatal(err)
			}
			for i := range msg {
				if buf[i] != msg[i] {
					t.Fatalf("byte %d: %x invece di %x", i, buf[i], msg[i])
				}
			}
		case <-time.After(3 * time.Second):
			t.Fatalf("connessione con primo byte %#x non smistata", primo)
		}
	}
	prova(0x16, d.TLS()) // inizio di TLS
	prova(0x00, d.ICE()) // lunghezza di un messaggio STUN
}

func TestChiusura(t *testing.T) {
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	d := Nuovo(ln)
	d.ICE().Close()
	if _, err := d.TLS().Accept(); err == nil {
		t.Fatal("Accept dopo la chiusura doveva dare errore")
	}
}
