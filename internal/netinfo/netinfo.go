// Package netinfo rileva gli indirizzi IP delle schede di rete attive
// e sceglie quello più probabile per la rete dell'evento.
package netinfo

import (
	"net"
	"sort"
	"strings"
)

// Address è un indirizzo IPv4 utilizzabile, con la scheda di rete a cui appartiene.
type Address struct {
	IP        string `json:"ip"`
	Interface string `json:"interface"`
	Score     int    `json:"score"`
}

// Detect restituisce gli IPv4 delle schede attive, ordinati dal più probabile
// al meno probabile (il primo è quello proposto di default).
func Detect() []Address {
	ifaces, err := net.Interfaces()
	if err != nil {
		return nil
	}
	var out []Address
	for _, ifc := range ifaces {
		if ifc.Flags&net.FlagUp == 0 || ifc.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, err := ifc.Addrs()
		if err != nil {
			continue
		}
		for _, a := range addrs {
			ipnet, ok := a.(*net.IPNet)
			if !ok {
				continue
			}
			ip4 := ipnet.IP.To4()
			if ip4 == nil {
				continue
			}
			out = append(out, Address{
				IP:        ip4.String(),
				Interface: ifc.Name,
				Score:     Score(ip4, ifc.Name),
			})
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Score > out[j].Score })
	return out
}

// Score assegna un punteggio: più è alto, più è probabile che sia la rete dell'evento.
// Preferisce reti private e schede ethernet; penalizza schede virtuali e VPN.
func Score(ip net.IP, ifName string) int {
	s := 0
	switch {
	case ip.IsLinkLocalUnicast(): // 169.254.x.x: nessun DHCP, quasi mai quella giusta
		s -= 50
	case ip.IsPrivate():
		s += 50
		if ip[0] == 192 && ip[1] == 168 {
			s += 10 // le reti da evento sono quasi sempre 192.168.x.x
		}
	case ip[0] == 100 && ip[1] >= 64 && ip[1] < 128: // CGNAT / Tailscale
		s -= 20
	}
	n := strings.ToLower(ifName)
	for _, v := range []string{"virtual", "vmware", "vbox", "virtualbox", "hyper-v", "vethernet", "docker", "veth", "br-", "utun", "tun", "tap", "tailscale", "zerotier", "wireguard", "wg", "awdl", "llw", "bridge", "loopback"} {
		if strings.Contains(n, v) {
			s -= 40
			break
		}
	}
	for _, e := range []string{"ethernet", "eth", "en0", "enp", "eno", "ens", "lan"} {
		if strings.HasPrefix(n, e) || strings.Contains(n, e) {
			s += 15
			break
		}
	}
	if strings.Contains(n, "wi-fi") || strings.Contains(n, "wlan") || strings.Contains(n, "wifi") || strings.HasPrefix(n, "wl") {
		s += 5
	}
	return s
}
