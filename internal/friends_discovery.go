package internal

import (
	"net"
	"os"
	"strings"
)

// ── Определение собственного адреса ──────────────────────────────────────────

// selfURLProvider вычисляет адрес, по которому нас видят друзья. Задаётся из
// main, где известны scheme/порт и есть доступ к автоопределению по сетевым
// картам. Значение нужно ДВУМ местам: фоновому циклу синка и friend-коду.
var selfURLProvider func() string

// SetSelfURLProvider задаёт источник адреса (см. DetectSelfURL).
func SetSelfURLProvider(f func() string) { selfURLProvider = f }

// SelfURL возвращает адрес для друзей, а если он не определён — пустую строку.
func SelfURL() string {
	if selfURLProvider == nil {
		return ""
	}
	return selfURLProvider()
}

// DetectSelfURL возвращает адрес, по которому нас видят друзья. Адрес нужен
// в двух местах: в friend-коде (его человек передаёт вручную) и в payload
// (instance — ключ для чужих комментариев).
//
// Выбираем НЕ 127.0.0.1: друг по нему нас не увидит. Приоритет:
//  1. адрес из BRIEFLY_FRIENDS_URL — явное переопределение пользователем;
//  2. адрес виртуальной VPN-сети (Radmin 26.x, Tailscale 100.x) — именно
//     так друзья соединяются в описанной схеме;
//  3. любой не-loopback адрес сетевой карты (обычная локальная сеть);
//  4. LAN-адрес, каким нас уже видно (запасной вариант).
func DetectSelfURL(scheme, port, listenAddr string) string {
	if v := strings.TrimSpace(os.Getenv("BRIEFLY_FRIENDS_URL")); v != "" {
		if norm, err := normalizeFriendURL(v); err == nil {
			return norm
		}
	}
	iface := os.Getenv("BRIEFLY_FRIENDS_IFACE")
	candidates := externalIPv4(iface)
	if len(candidates) == 0 {
		// Ничего не нашли: отдаём как есть, пусть другу видно локальный адрес
		// из настроек. Лучше неудачный адрес, чем пустой — цикл это учтёт.
		if listenAddr != "" {
			h, _, err := net.SplitHostPort(listenAddr)
			if err == nil {
				return scheme + "://" + net.JoinHostPort(h, port)
			}
		}
		return ""
	}
	return scheme + "://" + net.JoinHostPort(candidates[0], port)
}

// externalIPv4 собирает адреса сетевых карт: сначала VPN-подобные, потом
// остальные. Loopback и link-local пропускаем — по ним друга не достать.
func externalIPv4(wantIface string) []string {
	ifaces, err := net.Interfaces()
	if err != nil {
		return nil
	}
	var vpn, rest []string
	for _, ifc := range ifaces {
		if ifc.Flags&net.FlagUp == 0 || ifc.Flags&net.FlagLoopback != 0 {
			continue
		}
		if wantIface != "" && ifc.Name != wantIface {
			continue
		}
		addrs, err := ifc.Addrs()
		if err != nil {
			continue
		}
		for _, a := range addrs {
			var ip net.IP
			switch v := a.(type) {
			case *net.IPNet:
				ip = v.IP
			case *net.IPAddr:
				ip = v.IP
			}
			if ip == nil || ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsMulticast() {
				continue
			}
			v4 := ip.To4()
			if v4 == nil {
				continue
			}
			s := v4.String()
			// 26.x — Radmin VPN, 100.64–100.127 — CGNAT-диапазон, который
			// используют Tailscale/ZeroTier. Такие адреса заведомо «наши»
			// в схеме с VPN, поэтому они приоритетнее домашнего 192.168.x.
			if strings.HasPrefix(s, "26.") || (strings.HasPrefix(s, "100.") && s >= "100.64." && s <= "100.127.") {
				vpn = append(vpn, s)
			} else {
				rest = append(rest, s)
			}
		}
	}
	return append(vpn, rest...)
}
