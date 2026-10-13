package immagini

import (
	"bytes"
	"encoding/base64"
	"image"
	"image/jpeg"
	"image/png"
	"testing"
)

func url(mime string, b []byte) string {
	return "data:" + mime + ";base64," + base64.StdEncoding.EncodeToString(b)
}

func TestSaveAndRead(t *testing.T) {
	var jb, pb bytes.Buffer
	img := image.NewRGBA(image.Rect(0, 0, 64, 32))
	_ = jpeg.Encode(&jb, img, nil)
	_ = png.Encode(&pb, img)
	s := Store{Dir: t.TempDir()}
	n1, err := s.Save(url("image/jpeg", jb.Bytes()), MaxIconBytes, MaxIconSide)
	if err != nil || !ValidName(n1) {
		t.Fatalf("salvataggio JPEG: %q %v", n1, err)
	}
	n2, _ := s.Save(url("image/jpeg", jb.Bytes()), MaxIconBytes, MaxIconSide)
	if n1 != n2 {
		t.Fatal("la stessa immagine deve avere lo stesso nome")
	}
	if b, mime, err := s.Read(n1); err != nil || mime != "image/jpeg" || !bytes.Equal(b, jb.Bytes()) {
		t.Fatalf("lettura: %v %s", err, mime)
	}
	if _, err := s.Save(url("image/jpeg", pb.Bytes()), MaxIconBytes, MaxIconSide); err == nil {
		t.Fatal("PNG dichiarato come JPEG deve essere rifiutato")
	}
	if _, err := s.Save(url("image/svg+xml", []byte("<svg/>")), MaxIconBytes, MaxIconSide); err == nil {
		t.Fatal("SVG deve essere rifiutato")
	}
	if _, err := s.Save(url("image/png", pb.Bytes()), MaxIconBytes, 16); err == nil {
		t.Fatal("immagine oltre il lato massimo accettata")
	}
	for _, bad := range []string{"../evento.json", "abc.jpg", "0123456789abcdef0123.gif"} {
		if _, _, err := s.Read(bad); err == nil {
			t.Fatalf("nome %q accettato", bad)
		}
	}
}
