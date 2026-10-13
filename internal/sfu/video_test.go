package sfu

import (
	"testing"

	"github.com/pion/webrtc/v4"
)

func TestKeyframeDetection(t *testing.T) {
	cases := []struct {
		name    string
		mime    string
		payload []byte
		want    bool
	}{
		{"H264 IDR", webrtc.MimeTypeH264, []byte{0x65, 1, 2}, true},
		{"H264 SPS", webrtc.MimeTypeH264, []byte{0x67, 0x42}, true},
		{"H264 P-frame", webrtc.MimeTypeH264, []byte{0x41, 1, 2}, false},
		{"H264 STAP-A con SPS", webrtc.MimeTypeH264, []byte{0x78, 0, 2, 0x67, 0x42, 0, 2, 0x68, 0xce}, true},
		{"H264 STAP-A senza SPS", webrtc.MimeTypeH264, []byte{0x78, 0, 2, 0x41, 0x42}, false},
		{"H264 FU-A inizio IDR", webrtc.MimeTypeH264, []byte{0x7c, 0x85, 1}, true},
		{"H264 FU-A seguito IDR", webrtc.MimeTypeH264, []byte{0x7c, 0x05, 1}, false},
		{"VP8 keyframe", webrtc.MimeTypeVP8, []byte{0x10, 0x00, 0x9d, 0x01}, true},
		{"VP8 interframe", webrtc.MimeTypeVP8, []byte{0x10, 0x01, 0x9d, 0x01}, false},
		{"VP8 continuazione", webrtc.MimeTypeVP8, []byte{0x00, 0x00, 0x9d}, false},
	}
	for _, c := range cases {
		if got := isKeyframeStart(c.mime, c.payload); got != c.want {
			t.Errorf("%s: %v, atteso %v", c.name, got, c.want)
		}
	}
}
