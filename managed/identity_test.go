// Copyright 2026 Blink Labs Software
// SPDX-License-Identifier: Apache-2.0

package managed

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestManagedRejectsCaseAliasedCustomers(t *testing.T) {
	b := newAcceptanceBundle(t)
	// Distinct files keep the identity collision observable on every filesystem.
	first, second := localCustomer("Alice"), localCustomer("alice")
	publishJSON(t, filepath.Join(b.options.ConfigDir, "first.json"), first)
	publishJSON(t, filepath.Join(b.options.ConfigDir, "second.json"), second)
	publishJSON(t, filepath.Join(b.options.ConfigDir, "manifest.json"), Manifest{Generation: 1, Customers: []CustomerRef{
		{ID: first.ID, Revision: 1, File: "first.json"},
		{ID: second.ID, Revision: 1, File: "second.json"},
	}})
	r, err := New(b.options)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, r.Stop()) })
	require.ErrorContains(t, r.StartContext(t.Context()), "duplicate customer reference")
	require.Zero(t, r.instances.Load())
}

func TestManagedRemovedCustomerCannotChangeCase(t *testing.T) {
	b := newAcceptanceBundle(t)
	alice, other := localCustomer("Alice"), localCustomer("other")
	b.publish(t, 1, alice, other)
	r := b.start(t, nil)
	require.Contains(t, r.Status().Customers, "Alice", "mixed-case identities remain supported")
	b.publish(t, 2, other)
	_, err := r.ReloadAll(t.Context(), 2)
	require.NoError(t, err)
	b.publish(t, 3, localCustomer("alice"), other)
	_, err = r.ReloadAll(t.Context(), 3)
	require.ErrorContains(t, err, "customer identity differs only by case")
	require.NotContains(t, r.Status().Customers, "alice")
	b.publish(t, 3, alice, other)
	_, err = r.ReloadAll(t.Context(), 3)
	require.NoError(t, err)
}

func TestManagedRejectsCaseAliasedConfigurationFiles(t *testing.T) {
	b := newAcceptanceBundle(t)
	publishJSON(t, filepath.Join(b.options.ConfigDir, "manifest.json"), Manifest{Generation: 1, Customers: []CustomerRef{
		{ID: "first", Revision: 1, File: "Customer.json"},
		{ID: "second", Revision: 1, File: "customer.json"},
	}})
	r, err := New(b.options)
	require.NoError(t, err)
	_, _, err = r.loadManifest()
	require.ErrorContains(t, err, "duplicate customer reference")
}
