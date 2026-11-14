// Copyright 2026 Blink Labs Software
// SPDX-License-Identifier: Apache-2.0

// Package managed routes an immutable source stream through independent customer
// lanes configured by operator-owned files.
package managed

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"maps"
	"os"
	"regexp"
	"slices"
	"time"

	"github.com/blinklabs-io/adder/filter/cardano"
)

// Options sets operator policy independently of customer configuration.
type Options struct {
	MaxQueueRetainedSize int64
	ConfigDir            string
	CredentialsDir       string
	LogDir               string
	AllowedHosts         []string
	AllowLocalTargets    bool
	Logger               *slog.Logger
	MaxCustomers         int
	MaxRules             int
	MaxDestinations      int
	InputQueue           int
	DestinationQueue     int
	MaxInstances         int
	MaxFileSize          int64
	MaxEventSize         int64
	MaxRetainedSize      int64
	ReloadTimeout        time.Duration
	SendTimeout          time.Duration
	ShutdownTimeout      time.Duration
}

func (o *Options) defaults() error {
	if o.MaxCustomers == 0 {
		o.MaxCustomers = 32
	}
	if o.MaxRules == 0 {
		o.MaxRules = 32
	}
	if o.MaxDestinations == 0 {
		o.MaxDestinations = 8
	}
	if o.InputQueue == 0 {
		o.InputQueue = 256
	}
	if o.DestinationQueue == 0 {
		o.DestinationQueue = 256
	}
	if o.MaxInstances == 0 {
		o.MaxInstances = 512
	}
	if o.MaxFileSize == 0 {
		o.MaxFileSize = 1 << 20
	}
	if o.MaxEventSize == 0 {
		o.MaxEventSize = 1 << 20
	}
	if o.MaxQueueRetainedSize == 0 {
		o.MaxQueueRetainedSize = 16 << 20
	}
	if o.MaxRetainedSize == 0 {
		o.MaxRetainedSize = 512 << 20
	}
	if o.ReloadTimeout == 0 {
		o.ReloadTimeout = 10 * time.Second
	}
	if o.SendTimeout == 0 {
		o.SendTimeout = 15 * time.Second
	}
	if o.ShutdownTimeout == 0 {
		o.ShutdownTimeout = 20 * time.Second
	}
	if o.Logger == nil {
		o.Logger = slog.Default()
	}
	if o.MaxQueueRetainedSize < 1 || o.MaxQueueRetainedSize > 1<<30 || o.MaxCustomers < 1 || o.MaxCustomers > 1024 || o.MaxRules < 1 || o.MaxRules > 1024 || o.MaxDestinations < 1 || o.MaxDestinations > 64 || o.InputQueue < 1 || o.InputQueue > 4096 || o.DestinationQueue < 1 || o.DestinationQueue > 4096 || o.MaxInstances < 1 || o.MaxInstances > 4096 || o.MaxFileSize < 1 || o.MaxFileSize > 16<<20 || o.MaxEventSize < 1 || o.MaxEventSize > 16<<20 || o.MaxRetainedSize < o.MaxEventSize || o.MaxRetainedSize > 4<<30 || o.ReloadTimeout <= 0 || o.ReloadTimeout > time.Minute || o.SendTimeout <= 0 || o.SendTimeout > time.Minute || o.ShutdownTimeout <= 0 || o.ShutdownTimeout > time.Minute {
		return errors.New("invalid managed resource limits")
	}
	return nil
}

// Manifest is published last, after complete customer and credential files.
type Manifest struct {
	Generation uint64        `json:"generation"`
	Customers  []CustomerRef `json:"customers"`
}

// CustomerRef identifies one versioned customer file inside ConfigDir.
type CustomerRef struct {
	ID       string `json:"id"`
	Revision uint64 `json:"revision"`
	File     string `json:"file"`
}

// Customer contains filters and destinations owned by a single customer.
type Customer struct {
	ID           string        `json:"id"`
	Revision     uint64        `json:"revision"`
	Destinations []Destination `json:"destinations"`
	Filters      []Rule        `json:"filters"`
}

// Destination selects a registered plugin and versioned secret bindings.
type Destination struct {
	ID          string                   `json:"id"`
	Plugin      string                   `json:"plugin"`
	Options     map[string]string        `json:"options,omitempty"`
	Credentials map[string]CredentialRef `json:"credentials,omitempty"`
	LimitGroup  string                   `json:"limit_group,omitempty"`
	IntervalMS  int                      `json:"interval_ms,omitempty"`
}

// CredentialRef binds an immutable credential version to its customer.
type CredentialRef struct {
	File    string `json:"file"`
	Version uint64 `json:"version"`
}

type credential struct {
	CustomerID string `json:"customer_id"`
	Version    uint64 `json:"version"`
	Value      string `json:"value"`
}

// Rule chooses all destinations or one destination ID for a canonical event type.
type Rule struct {
	ID          string           `json:"id"`
	EventType   string           `json:"event_type"`
	Criteria    cardano.Criteria `json:"criteria,omitempty"`
	Destination string           `json:"destination"`
}

type compiledRule struct {
	Rule
	matcher *cardano.Matcher
}
type preparedCustomer struct {
	credentials map[CredentialRef][32]byte
	config      Customer
	digest      [32]byte
	rules       []compiledRule
	specs       map[string]preparedDestination
	size        int64
}
type preparedDestination struct {
	config Destination
	values map[string]any
	digest [32]byte
}

var (
	identifier = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_-]{0,63}$`)
	basename   = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.-]{0,127}$`)
)

func readFile(dir, name string, limit int64, secret bool) ([]byte, error) {
	if !basename.MatchString(name) {
		return nil, errors.New("invalid file basename")
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, errors.New("configuration directory unavailable")
	}
	defer root.Close()
	f, err := openManagedFile(root, name, os.O_RDONLY, 0)
	if err != nil {
		return nil, errors.New("configuration file unavailable")
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil || !st.Mode().IsRegular() || st.Size() > limit {
		return nil, errors.New("invalid configuration file")
	}
	if secret && st.Mode().Perm()&0o077 != 0 {
		return nil, errors.New("credential file must have mode 0600 or stricter")
	}
	data, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil || int64(len(data)) > limit {
		return nil, errors.New("configuration file exceeds limit")
	}
	return data, nil
}

func decode(data []byte, dest any) error {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dest); err != nil {
		return errors.New("invalid configuration JSON")
	}
	if dec.Decode(new(any)) != io.EOF {
		return errors.New("configuration must contain one JSON value")
	}
	return nil
}

func (r *Router) loadManifest() (Manifest, [32]byte, error) {
	var m Manifest
	data, err := readFile(r.options.ConfigDir, "manifest.json", r.options.MaxFileSize, false)
	if err != nil {
		return m, [32]byte{}, err
	}
	digest := sha256.Sum256(data)
	if err = decode(data, &m); err != nil {
		return m, digest, err
	}
	if m.Generation == 0 || len(m.Customers) == 0 || len(m.Customers) > r.options.MaxCustomers {
		return m, digest, errors.New("invalid manifest generation or customer count")
	}
	ids := map[string]bool{}
	files := map[string]bool{}
	for _, c := range m.Customers {
		if !identifier.MatchString(c.ID) || c.Revision == 0 || !basename.MatchString(c.File) || ids[c.ID] || files[c.File] {
			return m, digest, errors.New("invalid or duplicate customer reference")
		}
		ids[c.ID] = true
		files[c.File] = true
	}
	return m, digest, nil
}

func (r *Router) loadCustomer(ref CustomerRef) (*preparedCustomer, error) {
	data, err := readFile(r.options.ConfigDir, ref.File, r.options.MaxFileSize, false)
	if err != nil {
		return nil, err
	}
	p := &preparedCustomer{credentials: map[CredentialRef][32]byte{}, size: int64(len(data)), specs: map[string]preparedDestination{}}
	if err = decode(data, &p.config); err != nil {
		return nil, err
	}
	c := p.config
	if c.ID != ref.ID || c.Revision != ref.Revision {
		return nil, errors.New("customer identity or revision mismatch")
	}
	if len(c.Destinations) < 1 || len(c.Destinations) > r.options.MaxDestinations || len(c.Filters) < 1 || len(c.Filters) > r.options.MaxRules {
		return nil, errors.New("customer rule or destination budget exceeded")
	}
	digest := sha256.New()
	_, _ = digest.Write(data)
	for _, d := range c.Destinations {
		if !identifier.MatchString(d.ID) || p.specs[d.ID].config.ID != "" {
			return nil, errors.New("invalid or duplicate destination ID")
		}
		values := map[string]any{}
		for k, v := range d.Options {
			values[k] = v
		}
		for _, key := range slices.Sorted(maps.Keys(d.Credentials)) {
			ref := d.Credentials[key]
			if _, ok := values[key]; ok {
				return nil, errors.New("credential duplicates option")
			}
			if ref.Version == 0 {
				return nil, errors.New("credential version must be positive")
			}
			b, err := readFile(r.options.CredentialsDir, ref.File, r.options.MaxFileSize, true)
			if err != nil {
				return nil, err
			}
			var cred credential
			if err = decode(b, &cred); err != nil || cred.CustomerID != c.ID || cred.Version != ref.Version || cred.Value == "" {
				return nil, errors.New("credential owner or version mismatch")
			}
			p.credentials[ref] = sha256.Sum256(b)
			p.size += int64(len(b))
			_, _ = digest.Write(b)
			values[key] = cred.Value
		}
		if err := r.validateDestination(c.ID, d, values); err != nil {
			return nil, fmt.Errorf("destination %s: %w", d.ID, err)
		}
		b, err := json.Marshal(struct {
			Config Destination    `json:"config"`
			Values map[string]any `json:"values"`
		}{d, values})
		if err != nil {
			return nil, errors.New("destination encoding failed")
		}
		p.specs[d.ID] = preparedDestination{config: d, values: values, digest: sha256.Sum256(b)}
	}
	rules := map[string]bool{}
	for _, rule := range c.Filters {
		if !identifier.MatchString(rule.ID) || rules[rule.ID] {
			return nil, errors.New("invalid or duplicate rule ID")
		}
		rules[rule.ID] = true
		if rule.Destination != "all" && p.specs[rule.Destination].config.ID == "" {
			return nil, errors.New("rule references an unknown destination")
		}
		matcher, err := cardano.Compile(rule.EventType, rule.Criteria)
		if err != nil {
			return nil, fmt.Errorf("rule %s: invalid event criteria", rule.ID)
		}
		for _, spec := range p.specs {
			if rule.Destination != "all" && spec.config.ID != rule.Destination {
				continue
			}
			if !supports(spec.config.Plugin, rule.EventType) {
				return nil, errors.New("plugin does not support selected event type")
			}
		}
		p.rules = append(p.rules, compiledRule{rule, matcher})
	}
	copy(p.digest[:], digest.Sum(nil))
	return p, nil
}
