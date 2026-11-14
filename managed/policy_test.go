// Copyright 2026 Blink Labs Software
// SPDX-License-Identifier: Apache-2.0

package managed

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestManagedOutboundAddressPolicy(t *testing.T) {
	b := newAcceptanceBundle(t)
	b.options.AllowLocalTargets = false
	b.options.AllowedHosts = []string{
		"127.0.0.1", "10.0.0.1", "192.168.1.1", "169.254.169.254", "::1",
		"fc00::1", "::ffff:127.0.0.1", "0.0.0.0", "224.0.0.1", "100.64.0.1",
		"198.18.0.1", "localhost", "example.com",
	}
	r, err := New(b.options)
	require.NoError(t, err)
	for _, host := range b.options.AllowedHosts[:11] {
		t.Run(host, func(t *testing.T) {
			target := &url.URL{Scheme: "https", Host: net.JoinHostPort(host, "443")}
			require.Error(t, r.allowedURL(target))
		})
	}
	for _, raw := range []string{
		"https://not-allowed.example/", "http://example.com/", "https://user:password@example.com/",
		"https://example.com/#fragment", "https://example.com:0/", "file:///etc/passwd",
	} {
		target, err := url.Parse(raw)
		require.NoError(t, err)
		require.Error(t, r.allowedURL(target))
	}
	public, err := url.Parse("https://example.com/events")
	require.NoError(t, err)
	require.NoError(t, r.allowedURL(public))
	local, err := url.Parse("http://127.0.0.1:43210/events")
	require.NoError(t, err)
	require.Error(t, r.allowedURL(local))
	r.options.AllowLocalTargets = true
	require.NoError(t, r.allowedURL(local))
}

func TestManagedConnectionChecksResolvedAddresses(t *testing.T) {
	b := newAcceptanceBundle(t)
	b.options.AllowedHosts = []string{"localhost"}
	b.options.AllowLocalTargets = false
	r, err := New(b.options)
	require.NoError(t, err)
	target, err := url.Parse("https://localhost:43210/events")
	require.NoError(t, err)
	require.NoError(t, r.allowedURL(target), "an allowed hostname alone does not authorize its resolved addresses")
	transport := r.transport(&limitGroup{interval: time.Millisecond})
	t.Cleanup(transport.base.CloseIdleConnections)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	connection, err := transport.base.DialContext(ctx, "tcp", "localhost:43210")
	if connection != nil {
		_ = connection.Close()
	}
	require.ErrorContains(t, err, "resolved address forbidden")
}

func TestManagedWebhookDoesNotFollowRedirect(t *testing.T) {
	var forbiddenReceipts atomic.Int64
	forbidden := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		forbiddenReceipts.Add(1)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer forbidden.Close()
	redirected := make(chan struct{}, 8)
	redirect := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		_, _ = io.Copy(io.Discard, req.Body)
		redirected <- struct{}{}
		http.Redirect(w, req, forbidden.URL+"/secret", http.StatusTemporaryRedirect)
	}))
	defer redirect.Close()
	b := newAcceptanceBundle(t)
	b.options.SendTimeout = 150 * time.Millisecond
	c := localCustomer("u1")
	c.Destinations = []Destination{b.webhook(t, "u1", "remote", redirect.URL, 1)}
	b.publish(t, 1, c)
	r := b.start(t, nil)
	r.dispatch(block("redirect-must-not-send"))
	select {
	case <-redirected:
	case <-time.After(5 * time.Second):
		t.Fatal("initial webhook was not attempted")
	}
	b.diagnostics.await(t, func(records []routeRecord) bool {
		return countAction(records, "submission_uncertain", "u1") == 1 || countAction(records, "plugin_error", "u1") > 0
	})
	require.NoError(t, r.Stop())
	require.Zero(t, forbiddenReceipts.Load(), "even a redirect to another allowed host must be rejected")
}

func TestManagedCredentialFilePermissions(t *testing.T) {
	b := newAcceptanceBundle(t)
	c := localCustomer("u1")
	d := b.webhook(t, "u1", "remote", "http://127.0.0.1:43210/secret", 1)
	c.Destinations = []Destination{d}
	b.publish(t, 1, c)
	path := filepath.Join(b.options.CredentialsDir, d.Credentials["url"].File)
	require.NoError(t, os.Chmod(path, 0o644))
	r, err := New(b.options)
	require.NoError(t, err)
	require.ErrorContains(t, r.StartContext(context.Background()), "0600")
	require.NoError(t, r.Stop())
	require.Zero(t, r.Status().PluginInstances)
	require.False(t, r.Status().Ready)
}

func TestManagedCredentialVersionsAreImmutable(t *testing.T) {
	for _, changedRevision := range []bool{false, true} {
		t.Run(map[bool]string{false: "same customer revision", true: "new customer revision"}[changedRevision], func(t *testing.T) {
			b := newAcceptanceBundle(t)
			c := localCustomer("u1")
			d := b.webhook(t, "u1", "remote", "http://127.0.0.1:43210/original", 1)
			c.Destinations = []Destination{d}
			b.publish(t, 1, c)
			r := b.start(t, nil)
			publishJSON(t, filepath.Join(b.options.CredentialsDir, d.Credentials["url"].File), map[string]any{
				"customer_id": "u1", "version": 1, "value": "http://127.0.0.1:43210/CHANGED_CREDENTIAL_SENTINEL",
			})
			if changedRevision {
				c.Revision = 2
				b.publish(t, 2, c)
			}
			_, err := r.ReloadCustomer(context.Background(), "u1", c.Revision)
			require.Error(t, err, "a credential version must not acquire different content")
			require.Equal(t, uint64(1), r.Status().Customers["u1"].Revision)
			require.NotContains(t, err.Error(), "CHANGED_CREDENTIAL_SENTINEL")
			require.NotContains(t, r.Status().ValidationError, "CHANGED_CREDENTIAL_SENTINEL")
		})
	}
}

func TestManagedMultipleCredentialDigestIsDeterministic(t *testing.T) {
	b := newAcceptanceBundle(t)
	c := localCustomer("u1")
	d := b.webhook(t, "u1", "remote", "http://127.0.0.1:43210/private", 1)
	for key, value := range map[string]string{"username": "USERNAME_SENTINEL", "password": "PASSWORD_SENTINEL"} {
		file := "u1-" + key + "-1.json"
		publishJSON(t, filepath.Join(b.options.CredentialsDir, file), map[string]any{"customer_id": "u1", "version": 1, "value": value})
		d.Credentials[key] = CredentialRef{File: file, Version: 1}
	}
	c.Destinations = []Destination{d}
	b.publish(t, 1, c)
	r := b.start(t, nil)
	for range 64 {
		result, err := r.ReloadCustomer(context.Background(), "u1", 1)
		require.NoError(t, err)
		require.Equal(t, uint64(1), result.Revisions["u1"])
		require.Equal(t, int64(1), r.Status().PluginInstances)
	}
	require.NotContains(t, b.diagnostics.text(), "USERNAME_SENTINEL")
	require.NotContains(t, b.diagnostics.text(), "PASSWORD_SENTINEL")
}

func TestManagedFileRootsRejectEscapingSymlinks(t *testing.T) {
	for _, location := range []string{"configuration", "credentials", "output"} {
		t.Run(location, func(t *testing.T) {
			b := newAcceptanceBundle(t)
			c := localCustomer("u1")
			outside := filepath.Join(t.TempDir(), "outside.json")
			publishJSON(t, outside, c)
			link := filepath.Join(b.options.ConfigDir, "u1.json")
			if location == "credentials" {
				d := b.webhook(t, "u1", "remote", "http://127.0.0.1:43210/private", 1)
				c.Destinations = []Destination{d}
				link = filepath.Join(b.options.CredentialsDir, d.Credentials["url"].File)
				publishJSON(t, outside, map[string]any{"customer_id": "u1", "version": 1, "value": "http://127.0.0.1:43210/private"})
			} else if location == "output" {
				link = filepath.Join(b.options.LogDir, "u1.jsonl")
			}
			b.publish(t, 1, c)
			before, err := os.ReadFile(outside)
			require.NoError(t, err)
			if err := os.Remove(link); err != nil {
				require.True(t, os.IsNotExist(err))
			}
			require.NoError(t, os.Symlink(outside, link))
			r, err := New(b.options)
			require.NoError(t, err)
			require.Error(t, r.StartContext(context.Background()))
			require.NoError(t, r.Stop())
			after, err := os.ReadFile(outside)
			require.NoError(t, err)
			require.Equal(t, string(before), string(after))
		})
	}
}

func TestManagedWebhookUnsafeOptionsRejected(t *testing.T) {
	for key, value := range map[string]string{
		"state-path": "durable.sqlite", "max-retries": "9999", "headers": "secret",
		"url": "http://127.0.0.1:43210/plaintext-secret", "event-types": "all",
	} {
		t.Run(key, func(t *testing.T) {
			b := newAcceptanceBundle(t)
			c := localCustomer("u1")
			d := b.webhook(t, "u1", "remote", "http://127.0.0.1:43210/private", 1)
			d.Options[key] = value
			c.Destinations = []Destination{d}
			b.publish(t, 1, c)
			r, err := New(b.options)
			require.NoError(t, err)
			err = r.StartContext(context.Background())
			require.Error(t, err)
			require.NotContains(t, err.Error(), "plaintext-secret")
			require.NoError(t, r.Stop())
		})
	}
}

func TestManagedLogNamesCannotCrossCustomerPrefixes(t *testing.T) {
	b := newAcceptanceBundle(t)
	u1, other := localCustomer("u1"), localCustomer("u1-other")
	u1.Destinations[0].Options["path"] = "u1-other.jsonl"
	b.publish(t, 1, u1, other)
	r, err := New(b.options)
	require.NoError(t, err)
	require.Error(t, r.StartContext(context.Background()), "a short customer ID must not claim another customer's destination file")
	require.NoError(t, r.Stop())
}

func TestManagedAuthorityAliasesShareQuota(t *testing.T) {
	b := newAcceptanceBundle(t)
	b.options.AllowedHosts = []string{"example.com"}
	r, err := New(b.options)
	require.NoError(t, err)
	for _, target := range []string{"https://example.com/hook", "https://EXAMPLE.COM/hook", "https://example.com:443/hook", "https://example.com:0443/hook"} {
		d := Destination{ID: "remote", Plugin: "webhook", LimitGroup: "example.com", IntervalMS: 100}
		require.NoError(t, r.validateDestination("u1", d, map[string]any{"url": target}))
		d.LimitGroup = "EXAMPLE.COM:443"
		require.Error(t, r.validateDestination("u1", d, map[string]any{"url": target}))
	}
}

func TestManagedNumericPortAliasesShareQuota(t *testing.T) {
	for target, expected := range map[string]string{
		"http://127.0.0.1:0080/hook":     "127.0.0.1",
		"https://example.com:00443/hook": "example.com",
		"https://example.com:08443/hook": "example.com:8443",
	} {
		parsed, err := url.Parse(target)
		require.NoError(t, err)
		require.Equal(t, expected, quotaAuthority(parsed))
	}
}
