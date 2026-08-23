package internal

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"sync"
	"time"
)

var hardcodedDNS = map[string]string{
	"rule34.xxx":         "8.6.112.0",
	"api.rule34.xxx":     "8.6.112.0",
	"api-cdn.rule34.xxx": "8.6.112.0",
}

type DoHResolver struct {
	url    string
	client *http.Client
	cache  sync.Map
}

type dnsResponse struct {
	Answer []struct {
		Name string `json:"name"`
		Type int    `json:"type"`
		TTL  int    `json:"TTL"`
		Data string `json:"data"`
	} `json:"Answer"`
}

func NewDoHResolver(dohURL string) *DoHResolver {
	return &DoHResolver{
		url: dohURL,
		client: &http.Client{
			Timeout: 5 * time.Second,
			Transport: &http.Transport{
				TLSHandshakeTimeout: 3 * time.Second,
				IdleConnTimeout:     30 * time.Second,
			},
		},
	}
}

func (r *DoHResolver) Resolve(host string) (net.IP, error) {
	if cached, ok := r.cache.Load(host); ok {
		return cached.(net.IP), nil
	}

	if ip, err := r.resolveDoH(host); err == nil {
		return ip, nil
	}

	if ipStr, ok := hardcodedDNS[host]; ok {
		if ip := net.ParseIP(ipStr); ip != nil {
			return ip, nil
		}
	}

	return nil, fmt.Errorf("no A record found for %s", host)
}

func (r *DoHResolver) resolveDoH(host string) (net.IP, error) {
	reqURL := fmt.Sprintf("%s?name=%s&type=A", r.url, host)
	req, err := http.NewRequest("GET", reqURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/dns-json")

	resp, err := r.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("DoH request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	var dnsResp dnsResponse
	if err := json.Unmarshal(body, &dnsResp); err != nil {
		return nil, err
	}

	for _, ans := range dnsResp.Answer {
		if ans.Type == 1 {
			ip := net.ParseIP(ans.Data)
			if ip != nil {
				ttl := ans.TTL
				if ttl < 60 {
					ttl = 60
				}
				r.cache.Store(host, ip)
				time.AfterFunc(time.Duration(ttl)*time.Second, func() {
					r.cache.Delete(host)
				})
				return ip, nil
			}
		}
	}

	return nil, fmt.Errorf("no A record found for %s", host)
}

func (r *DoHResolver) DialContext(ctx context.Context, network, addr string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, err
	}

	var d net.Dialer
	if !isIP(host) {
		ip, err := r.Resolve(host)
		if err != nil {

			return d.DialContext(ctx, network, addr)
		}
		addr = net.JoinHostPort(ip.String(), port)
	}

	return d.DialContext(ctx, network, addr)
}

func isIP(host string) bool {
	return net.ParseIP(host) != nil
}

func NewResolveTransport(dohURL string) *http.Transport {
	resolver := NewDoHResolver(dohURL)
	return &http.Transport{
		MaxIdleConns:       20,
		IdleConnTimeout:    90 * time.Second,
		DisableCompression: false,
		DialContext:        resolver.DialContext,
	}
}
