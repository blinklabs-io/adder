// Copyright 2026 Blink Labs Software
// SPDX-License-Identifier: Apache-2.0

package managed

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/blinklabs-io/adder/event"
	"github.com/blinklabs-io/adder/plugin"
)

// Capability explains whether a registered output can be routed safely.
type Capability struct {
	Plugin  string `json:"plugin"`
	Enabled bool   `json:"enabled"`
	Reason  string `json:"reason"`
}

// Capabilities returns an explicit disposition for every registered output.
func Capabilities() []Capability {
	plugins := plugin.GetPlugins(plugin.PluginTypeOutput)
	result := make([]Capability, 0, len(plugins))
	reasons := map[string]string{
		"log":           "trusted local files only; confined JSON append; no provider quota",
		"webhook":       "single target; restricted HTTP client; shared origin limit; bounded retries; external outcomes unknown",
		"slack":         "multi-target sending and shared account quota need an adapter",
		"telegram":      "bot command lifecycle and chat quota need an adapter",
		"discord":       "bot gateway and commands need an adapter",
		"matrix":        "room discovery and server quotas need an adapter",
		"signal":        "external service target policy and account quota need an adapter",
		"push":          "subscriber fanout and HTTP routes need per-target accounting",
		"sns":           "account/topic quotas and SDK retry budgets need an adapter",
		"sqs":           "account/queue quotas and SDK retry budgets need an adapter",
		"pubsub":        "client batching, credentials and quota need an adapter",
		"s3":            "object naming, client retries and target policy need an adapter",
		"kafka":         "broker discovery, producer buffers and retries need an adapter",
		"nats":          "broker reconnect and pending buffers need an adapter",
		"mqtt":          "session reconnect and pending acknowledgments need an adapter",
		"rabbitmq":      "broker recovery and confirmation accounting need an adapter",
		"redis":         "connection target policy and pending work need an adapter",
		"postgres":      "connection and transactional work bounds need an adapter",
		"clickhouse":    "connection target policy and write buffers need an adapter",
		"elasticsearch": "bulk client buffers and retries need an adapter",
		"notify":        "desktop side effects are not customer-isolated",
		"notify-json":   "desktop/file side effects need an adapter",
	}
	for _, p := range plugins {
		reason := reasons[p.Name]
		if reason == "" {
			reason = "no audited managed adapter"
		}
		result = append(result, Capability{p.Name, p.Name == "log" || p.Name == "webhook", reason})
	}
	slices.SortFunc(result, func(a, b Capability) int { return strings.Compare(a.Plugin, b.Plugin) })
	return result
}

func supports(name, typ string) bool {
	if name == "log" {
		return true
	}
	return name == "webhook" && (typ == event.TypeBlock || typ == event.TypeTransaction || typ == event.TypeRollback || typ == event.TypeGovernance)
}

func validateDestinationFields(d Destination) error {
	switch d.Plugin {
	case "log":
		if len(d.Credentials) != 0 {
			return errors.New("log credentials are not permitted")
		}
		for key := range d.Options {
			if key != "path" && key != "format" {
				return errors.New("log option not permitted")
			}
		}
	case "webhook":
		if len(d.Credentials) > 3 {
			return errors.New("credential option budget exceeded")
		}
		for key := range d.Credentials {
			if key != "url" && key != "username" && key != "password" {
				return errors.New("credential option not permitted")
			}
		}
		for key := range d.Options {
			if key != "format" {
				return errors.New("webhook secrets require credential references; only format is a plain option")
			}
		}
	default:
		return errors.New("plugin has no supported managed adapter")
	}
	return nil
}

func (r *Router) validateDestination(customer string, d Destination, values map[string]any) error {
	if err := validateDestinationFields(d); err != nil {
		return err
	}
	switch d.Plugin {
	case "log":
		if len(d.Credentials) > 0 || d.LimitGroup != "" || d.IntervalMS != 0 {
			return errors.New("log destination has network options")
		}
		for k := range values {
			if k != "path" && k != "format" {
				return errors.New("log option not permitted")
			}
		}
		name, _ := values["path"].(string)
		if !basename.MatchString(name) || !strings.HasPrefix(name, customer+".") {
			return errors.New("log filename must begin with customer ID followed by a dot")
		}
		if format, ok := values["format"]; ok && format != "json" {
			return errors.New("managed log format must be json")
		}
		if r.options.LogDir == "" {
			return errors.New("trusted local log directory required")
		}
		values["format"] = "json"
		values["level"] = "info"
	case "webhook":
		for k := range values {
			if k != "url" && k != "format" && k != "username" && k != "password" {
				return errors.New("webhook option not permitted")
			}
		}
		for _, k := range []string{"url", "username", "password"} {
			if _, ok := d.Options[k]; ok {
				return errors.New("webhook secrets require credential references")
			}
		}
		raw, _ := values["url"].(string)
		u, err := url.Parse(raw)
		if err != nil || r.allowedURL(u) != nil {
			return errors.New("webhook target forbidden")
		}
		// A customer cannot split one origin's quota into independent groups.
		if d.LimitGroup != quotaAuthority(u) || d.IntervalMS < 1 || d.IntervalMS > 60000 {
			return errors.New("limit_group must equal canonical target authority and interval_ms must be 1..60000")
		}
		if format, ok := values["format"]; ok && format != "adder" && format != "discord" {
			return errors.New("invalid webhook format")
		}
		if values["format"] == "discord" && d.IntervalMS < 2000 {
			return errors.New("discord interval_ms must be at least 2000; Retry-After remains authoritative")
		}
	default:
		return errors.New("plugin has no supported managed adapter")
	}
	return nil
}

func quotaAuthority(u *url.URL) string {
	host := strings.ToLower(u.Hostname())
	port := u.Port()
	if numeric, err := strconv.Atoi(port); err == nil {
		port = strconv.Itoa(numeric)
	}
	if port == "443" && u.Scheme == "https" || port == "80" && u.Scheme == "http" {
		port = ""
	}
	if port != "" {
		return net.JoinHostPort(host, port)
	}
	if strings.Contains(host, ":") {
		return "[" + host + "]"
	}
	return host
}

func (r *Router) allowedURL(u *url.URL) error {
	if u == nil || u.Hostname() == "" || u.User != nil || u.Fragment != "" {
		return errors.New("target forbidden")
	}
	host := strings.ToLower(u.Hostname())
	if !slices.Contains(r.options.AllowedHosts, host) {
		return errors.New("target forbidden")
	}
	ip, iperr := netip.ParseAddr(host)
	if u.Scheme != "https" && (u.Scheme != "http" || !r.options.AllowLocalTargets || iperr != nil || !ip.IsLoopback()) {
		return errors.New("target scheme forbidden")
	}
	if iperr == nil && !r.allowedIP(ip) {
		return errors.New("target address forbidden")
	}
	if u.Port() != "" {
		p, err := strconv.Atoi(u.Port())
		if err != nil || p < 1 || p > 65535 {
			return errors.New("target port forbidden")
		}
	}
	return nil
}

var deniedNetworks = []netip.Prefix{
	netip.MustParsePrefix("100.64.0.0/10"), netip.MustParsePrefix("192.0.0.0/24"),
	netip.MustParsePrefix("192.0.2.0/24"), netip.MustParsePrefix("198.18.0.0/15"),
	netip.MustParsePrefix("198.51.100.0/24"), netip.MustParsePrefix("203.0.113.0/24"),
	netip.MustParsePrefix("240.0.0.0/4"), netip.MustParsePrefix("2001:db8::/32"),
}

func (r *Router) allowedIP(ip netip.Addr) bool {
	ip = ip.Unmap()
	if ip.IsLoopback() {
		return r.options.AllowLocalTargets
	}
	if !ip.IsGlobalUnicast() || ip.IsPrivate() || ip.IsLinkLocalUnicast() {
		return false
	}
	for _, prefix := range deniedNetworks {
		if prefix.Contains(ip) {
			return false
		}
	}
	return true
}

type limitGroup struct {
	mu       sync.Mutex
	next     time.Time
	last     time.Time
	interval time.Duration
	refs     int
}

func (g *limitGroup) wait(ctx context.Context) error {
	for {
		g.mu.Lock()
		now := time.Now()
		wait := g.next.Sub(now)
		if wait <= 0 {
			g.last = now
			g.next = now.Add(g.interval)
			g.mu.Unlock()
			return nil
		}
		g.mu.Unlock()
		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
}

func (g *limitGroup) pause(header string) {
	var until time.Time
	if seconds, err := strconv.ParseUint(strings.TrimSpace(header), 10, 32); err == nil {
		until = time.Now().Add(time.Duration(seconds) * time.Second)
	} else if date, err := http.ParseTime(header); err == nil {
		until = date
	} else {
		until = time.Now().Add(time.Second)
	}
	g.mu.Lock()
	if until.After(g.next) {
		g.next = until
	}
	g.mu.Unlock()
}

type limitedTransport struct {
	base        *http.Transport
	group       *limitGroup
	router      *Router
	waitMu      sync.Mutex
	waitStarted time.Time
	waited      time.Duration
	changed     chan struct{}
}

func (t *limitedTransport) quotaTime() (time.Duration, bool) {
	t.waitMu.Lock()
	defer t.waitMu.Unlock()
	if !t.waitStarted.IsZero() {
		return t.waited + time.Since(t.waitStarted), true
	}
	return t.waited, false
}

func (t *limitedTransport) wait(ctx context.Context) error {
	t.waitMu.Lock()
	t.waitStarted = time.Now()
	t.waitMu.Unlock()
	t.notifyWait()
	err := t.group.wait(ctx)
	t.waitMu.Lock()
	t.waited += time.Since(t.waitStarted)
	t.waitStarted = time.Time{}
	t.waitMu.Unlock()
	t.notifyWait()
	return err
}

func (t *limitedTransport) notifyWait() {
	select {
	case t.changed <- struct{}{}:
	default:
	}
}

type cancelBody struct {
	io.ReadCloser
	cancel context.CancelFunc
}

func (b *cancelBody) Close() error {
	defer b.cancel()
	return b.ReadCloser.Close()
}

func (t *limitedTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if err := t.router.allowedURL(req.URL); err != nil {
		return nil, err
	}
	if err := t.wait(req.Context()); err != nil {
		return nil, err
	}
	// Provider quota waits do not spend the actual HTTP attempt's deadline.
	ctx, cancel := context.WithTimeout(req.Context(), 5*time.Second)
	response, err := t.base.RoundTrip(req.Clone(ctx))
	if err != nil {
		cancel()
	} else {
		response.Body = &cancelBody{ReadCloser: response.Body, cancel: cancel}
	}
	if response != nil && response.StatusCode == http.StatusTooManyRequests {
		t.group.pause(response.Header.Get("Retry-After"))
		// This shared limiter owns the provider pause. The plugin still applies
		// its bounded retry backoff, but must not wait for this quota twice.
		response.Header.Del("Retry-After")
	}
	return response, err
}

func (r *Router) transport(group *limitGroup) *limitedTransport {
	transport := &http.Transport{TLSHandshakeTimeout: 5 * time.Second, ResponseHeaderTimeout: 5 * time.Second, IdleConnTimeout: 10 * time.Second, MaxIdleConnsPerHost: 1}
	transport.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(address)
		if err != nil || !slices.Contains(r.options.AllowedHosts, strings.ToLower(host)) {
			return nil, errors.New("connection target forbidden")
		}
		ips, err := net.DefaultResolver.LookupNetIP(ctx, "ip", host)
		if err != nil || len(ips) == 0 {
			return nil, errors.New("target resolution failed")
		}
		dialer := net.Dialer{}
		return r.dialResolved(ctx, network, port, ips, dialer.DialContext)
	}
	return &limitedTransport{base: transport, group: group, router: r, changed: make(chan struct{}, 1)}
}

func (r *Router) dialResolved(ctx context.Context, network, port string, ips []netip.Addr, dial func(context.Context, string, string) (net.Conn, error)) (net.Conn, error) {
	for _, ip := range ips {
		if !r.allowedIP(ip) {
			return nil, errors.New("resolved address forbidden")
		}
	}
	if len(ips) == 0 {
		return nil, errors.New("target resolution failed")
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	deadline, _ := ctx.Deadline()
	var lastErr error
	for i, ip := range ips {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		// Share the remaining budget so one blackholed address cannot starve
		// the other validated addresses. Never resolve the name again here.
		attempt, cancelAttempt := context.WithTimeout(ctx, time.Until(deadline)/time.Duration(len(ips)-i))
		conn, err := dial(attempt, network, net.JoinHostPort(ip.String(), port))
		cancelAttempt()
		if err == nil {
			return conn, nil
		}
		lastErr = err
	}
	return nil, lastErr
}
