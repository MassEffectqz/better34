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

// defaultDoHEndpoints — порядок обхода публичных DoH-эндпоинтов (формат
// Google DNS JSON API поддерживают оба). Резервный нужен, когда первый
// блокируется или лагает на пути к сети: после неудачи резолв не должен
// молча деградировать до системного DNS провайдера с его подменами.
var defaultDoHEndpoints = []string{
	"https://cloudflare-dns.com/dns-query",
	"https://dns.google/resolve",
}

// dohNegativeTTL — сколько после неудачи не долбим DoH по хосту:
// сетка превью с недоступного CDN иначе штормит параллельными
// резолвами каждые несколько миллисекунд.
const dohNegativeTTL = 10 * time.Second

type resolveCall struct {
	done chan struct{}
	ip   net.IP
	err  error
}

type DoHResolver struct {
	urls   []string
	client *http.Client
	cache  sync.Map // host -> net.IP

	negative sync.Map // host -> time.Time (не раньше чего повторять)

	mu       sync.Mutex
	inflight map[string]*resolveCall
}

type dnsResponse struct {
	Answer []struct {
		Name string `json:"name"`
		Type int    `json:"type"`
		TTL  int    `json:"TTL"`
		Data string `json:"data"`
	} `json:"Answer"`
}

func NewDoHResolver(dohURLs ...string) *DoHResolver {
	urls := dohURLs
	if len(urls) == 0 {
		urls = defaultDoHEndpoints
	}
	return &DoHResolver{
		urls: urls,
		client: &http.Client{
			Timeout: 5 * time.Second,
			Transport: &http.Transport{
				TLSHandshakeTimeout: 3 * time.Second,
				IdleConnTimeout:     30 * time.Second,
			},
		},
	}
}

func (r *DoHResolver) fallback(host string) (net.IP, error) {
	if ipStr, ok := hardcodedDNS[host]; ok {
		if ip := net.ParseIP(ipStr); ip != nil {
			return ip, nil
		}
	}
	return nil, fmt.Errorf("no A record found for %s", host)
}

// Resolve с дедупликацией: параллельные резолвы одного хоста ждут
// один общий запрос; после неудачи хост на 10с уходит в негативный кэш.
func (r *DoHResolver) Resolve(host string) (net.IP, error) {
	if cached, ok := r.cache.Load(host); ok {
		return cached.(net.IP), nil
	}

	if t, ok := r.negative.Load(host); ok {
		if time.Now().Before(t.(time.Time)) {
			return r.fallback(host)
		}
		r.negative.Delete(host)
	}

	r.mu.Lock()
	if cl, ok := r.inflight[host]; ok {
		r.mu.Unlock()
		<-cl.done
		return cl.ip, cl.err
	}
	cl := &resolveCall{done: make(chan struct{})}
	if r.inflight == nil {
		r.inflight = make(map[string]*resolveCall)
	}
	r.inflight[host] = cl
	r.mu.Unlock()

	ip, err := r.resolveDoH(host)
	if err != nil {
		if fb, ferr := r.fallback(host); ferr == nil {
			ip, err = fb, nil
		} else {
			r.negative.Store(host, time.Now().Add(dohNegativeTTL))
		}
	}

	cl.ip, cl.err = ip, err
	r.mu.Lock()
	delete(r.inflight, host)
	r.mu.Unlock()
	close(cl.done)
	return ip, err
}

func (r *DoHResolver) resolveDoH(host string) (net.IP, error) {
	var lastErr error
	for _, base := range r.urls {
		ip, err := r.resolveEndpoint(base, host)
		if err == nil {
			return ip, nil
		}
		lastErr = err
	}
	return nil, lastErr
}

// resolveEndpoint опрашивает один DoH-эндпоинт.
func (r *DoHResolver) resolveEndpoint(base, host string) (net.IP, error) {
	reqURL := fmt.Sprintf("%s?name=%s&type=A", base, host)
	req, err := http.NewRequest("GET", reqURL, nil)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", base, err)
	}
	req.Header.Set("Accept", "application/dns-json")

	resp, err := r.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%s: DoH request failed: %w", base, err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", base, err)
	}

	var dnsResp dnsResponse
	if err := json.Unmarshal(body, &dnsResp); err != nil {
		return nil, fmt.Errorf("%s: %w", base, err)
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

	return nil, fmt.Errorf("%s: no A record found for %s", base, host)
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

func NewResolveTransport(dohURLs ...string) *http.Transport {
	resolver := NewDoHResolver(dohURLs...)
	return &http.Transport{
		// Свой DialContext без этого флага молча отключает HTTP/2:
		// CDN-превью шли по HTTP/1.1, отдельный TCP+TLS на каждый
		// поток. С флагом — мультиплексирование поверх ALPN.
		ForceAttemptHTTP2:   true,
		MaxIdleConns:        100,
		MaxIdleConnsPerHost: 32,
		IdleConnTimeout:     90 * time.Second,
		DisableCompression:  false,
		DialContext:         resolver.DialContext,
	}
}
