package main

import (
	"briefly/internal"
	"net"
	"net/http"
	"os"
	"slices"
	"strings"

	"github.com/gin-gonic/gin"
)

var allowedHosts = func() []string {
	hosts := []string{"localhost", "127.0.0.1", "::1"}
	extra := os.Getenv("BRIEFLY_ALLOWED_HOSTS")
	if extra == "" {
		// Список не задан — разрешаем адреса всех сетевых интерфейсов
		// машины: доступ с телефона/другого ПК по IP хоста работает
		// без ручных настроек. Явный BRIEFLY_ALLOWED_HOSTS сужает круг.
		ifaces, err := net.Interfaces()
		if err != nil {
			return hosts
		}
		for _, ifc := range ifaces {
			if ifc.Flags&net.FlagUp == 0 || ifc.Flags&net.FlagLoopback != 0 {
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
				s := ip.String()
				if !slices.Contains(hosts, s) {
					hosts = append(hosts, s)
				}
			}
		}
		return hosts
	}
	for _, h := range strings.Split(extra, ",") {
		if h = strings.ToLower(strings.TrimSpace(h)); h != "" {
			hosts = append(hosts, h)
		}
	}
	return hosts
}()

var authToken = strings.TrimSpace(os.Getenv("BRIEFLY_TOKEN"))

func hostAllowed(r *http.Request) bool {
	h := r.Host
	if hh, _, err := net.SplitHostPort(h); err == nil {
		h = hh
	}
	h = strings.ToLower(strings.Trim(strings.TrimSpace(h), "[]"))
	h = strings.TrimSuffix(h, ".")
	for _, a := range allowedHosts {
		if h == a {
			return true
		}
	}
	return false
}

func tokenMatches(c *gin.Context) bool {
	if internal.TokenMatches(c) {
		return true
	}
	return authToken == ""
}
