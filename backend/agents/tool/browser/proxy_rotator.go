package browser

import (
	"fmt"
	"math/rand"
	"net/url"
	"strings"
	"sync"
)

// ProxyConfig represents a parsed proxy configuration.
type ProxyConfig struct {
	Server   string `json:"server"`
	Username string `json:"username,omitempty"`
	Password string `json:"password,omitempty"`
}

// RotationStrategy defines a pluggable proxy selection strategy.
// Returns the selected proxy and the next index state.
type RotationStrategy func(proxies []ProxyConfig, currentIndex int) (ProxyConfig, int)

// CyclicRotation selects proxies in round-robin order.
func CyclicRotation(proxies []ProxyConfig, idx int) (ProxyConfig, int) {
	i := idx % len(proxies)
	return proxies[i], (i + 1) % len(proxies)
}

// RandomRotation selects a random proxy each time.
func RandomRotation(proxies []ProxyConfig, _ int) (ProxyConfig, int) {
	i := rand.Intn(len(proxies))
	return proxies[i], i
}

// ProxyRotator is a thread-safe proxy rotator with pluggable strategies.
// Source concept: Scrapling toolbelt/proxy_rotation.py:ProxyRotator
type ProxyRotator struct {
	mu       sync.Mutex
	proxies  []ProxyConfig
	strategy RotationStrategy
	current  int
}

// NewProxyRotator creates a new ProxyRotator with the given proxies and strategy.
// Returns error if no proxies are provided. Strategy defaults to CyclicRotation if nil.
func NewProxyRotator(proxies []ProxyConfig, strategy RotationStrategy) (*ProxyRotator, error) {
	if len(proxies) == 0 {
		return nil, fmt.Errorf("at least one proxy must be provided")
	}
	if strategy == nil {
		strategy = CyclicRotation
	}
	return &ProxyRotator{proxies: proxies, strategy: strategy}, nil
}

// GetProxy returns the next proxy according to the rotation strategy.
func (r *ProxyRotator) GetProxy() ProxyConfig {
	r.mu.Lock()
	defer r.mu.Unlock()
	proxy, next := r.strategy(r.proxies, r.current)
	r.current = next
	return proxy
}

// Count returns the number of proxies in the rotator.
func (r *ProxyRotator) Count() int {
	return len(r.proxies)
}

// proxyErrorIndicators lists error message substrings indicating proxy failures.
// Source: Scrapling _PROXY_ERROR_INDICATORS
var proxyErrorIndicators = []string{
	"net::err_proxy",
	"net::err_tunnel",
	"connection refused",
	"connection reset",
	"connection timed out",
	"failed to connect",
	"could not resolve proxy",
}

// IsProxyError checks if an error is caused by a proxy failure.
func IsProxyError(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	for _, ind := range proxyErrorIndicators {
		if strings.Contains(msg, ind) {
			return true
		}
	}
	return false
}

// ParseProxyString parses a proxy URL string into a ProxyConfig.
// Supports formats: http://host:port, http://user:pass@host:port, socks5://host:port
func ParseProxyString(proxy string) (ProxyConfig, error) {
	proxy = strings.TrimSpace(proxy)
	if proxy == "" {
		return ProxyConfig{}, fmt.Errorf("empty proxy string")
	}

	// Add scheme if missing
	if !strings.Contains(proxy, "://") {
		proxy = "http://" + proxy
	}

	u, err := url.Parse(proxy)
	if err != nil {
		return ProxyConfig{}, fmt.Errorf("invalid proxy URL %q: %w", proxy, err)
	}

	cfg := ProxyConfig{
		Server: fmt.Sprintf("%s://%s", u.Scheme, u.Host),
	}
	if u.User != nil {
		cfg.Username = u.User.Username()
		cfg.Password, _ = u.User.Password()
	}
	return cfg, nil
}

// ProxyURL returns the full proxy URL including credentials if present.
func (pc ProxyConfig) ProxyURL() string {
	if pc.Username == "" {
		return pc.Server
	}
	u, err := url.Parse(pc.Server)
	if err != nil {
		return pc.Server
	}
	if pc.Password != "" {
		u.User = url.UserPassword(pc.Username, pc.Password)
	} else {
		u.User = url.User(pc.Username)
	}
	return u.String()
}
