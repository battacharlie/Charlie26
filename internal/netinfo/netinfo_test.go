package netinfo

import (
	"net"
	"testing"
)

func TestScorePrefersPrivateEthernet(t *testing.T) {
	eth := Score(net.ParseIP("192.168.1.10").To4(), "Ethernet")
	wifi := Score(net.ParseIP("192.168.0.20").To4(), "Wi-Fi")
	virt := Score(net.ParseIP("192.168.56.1").To4(), "VirtualBox Host-Only Network")
	apipa := Score(net.ParseIP("169.254.3.4").To4(), "Ethernet 2")
	vpn := Score(net.ParseIP("100.80.1.2").To4(), "Tailscale")

	if !(eth > wifi && wifi > virt && virt > apipa) {
		t.Fatalf("ordine inatteso: eth=%d wifi=%d virt=%d apipa=%d", eth, wifi, virt, apipa)
	}
	if vpn >= wifi {
		t.Fatalf("la VPN non dovrebbe battere il Wi-Fi: vpn=%d wifi=%d", vpn, wifi)
	}
}

func TestDetectDoesNotPanic(t *testing.T) {
	for _, a := range Detect() {
		if net.ParseIP(a.IP) == nil {
			t.Fatalf("IP non valido: %q", a.IP)
		}
	}
}
