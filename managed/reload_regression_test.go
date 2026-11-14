// Copyright 2026 Blink Labs Software
// SPDX-License-Identifier: Apache-2.0

package managed

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestManagedIdempotentReloadKeepsOriginalCutover(t *testing.T) {
	b := newAcceptanceBundle(t)
	c := localCustomer("u1")
	b.publish(t, 1, c)
	r := b.start(t, nil)
	r.dispatch(block("initial"))
	all, err := r.ReloadAll(context.Background(), 1)
	require.NoError(t, err)
	require.Zero(t, all.Cutover, "repeating startup generation cannot invent a new switch")
	c.Revision = 2
	c.Filters[0].ID = "changed"
	b.publish(t, 2, c)
	changed, err := r.ReloadCustomer(context.Background(), "u1", 2)
	require.NoError(t, err)
	require.Equal(t, uint64(1), changed.Cutover)
	r.dispatch(block("later"))
	repeated, err := r.ReloadCustomer(context.Background(), "u1", 2)
	require.NoError(t, err)
	require.Equal(t, changed.Cutover, repeated.Cutover)
	require.Equal(t, changed.Cutover, r.Status().Customers["u1"].Cutover)
	all, err = r.ReloadAll(context.Background(), 2)
	require.NoError(t, err)
	require.Equal(t, uint64(2), all.Cutover)
	r.dispatch(block("still-later"))
	repeated, err = r.ReloadAll(context.Background(), 2)
	require.NoError(t, err)
	require.Equal(t, all.Cutover, repeated.Cutover)
}

func TestManagedRemovedCustomerCannotResetRevision(t *testing.T) {
	b := newAcceptanceBundle(t)
	u1, u2 := localCustomer("u1"), localCustomer("u2")
	u1.Revision = 5
	b.publish(t, 1, u1, u2)
	r := b.start(t, nil)
	b.publish(t, 2, u2)
	_, err := r.ReloadAll(context.Background(), 2)
	require.NoError(t, err)
	u1.Revision = 1
	b.publish(t, 3, u1, u2)
	_, err = r.ReloadAll(context.Background(), 3)
	require.ErrorContains(t, err, "stale customer revision")
	require.NotContains(t, r.Status().Customers, "u1")
	u1.Revision = 6
	b.publish(t, 3, u1, u2)
	_, err = r.ReloadAll(context.Background(), 3)
	require.NoError(t, err)
	require.Equal(t, uint64(6), r.Status().Customers["u1"].Revision)
}

func TestManagedRemovedDestinationCannotResetCredentialVersion(t *testing.T) {
	b := newAcceptanceBundle(t)
	c := localCustomer("u1")
	c.Destinations = append(c.Destinations, b.webhook(t, "u1", "remote", "http://127.0.0.1:1/old", 5))
	b.publish(t, 1, c)
	r := b.start(t, nil)
	c.Revision = 2
	c.Destinations = c.Destinations[:1]
	b.publish(t, 2, c)
	_, err := r.ReloadCustomer(context.Background(), "u1", 2)
	require.NoError(t, err)
	c.Revision = 3
	c.Destinations = append(c.Destinations, b.webhook(t, "u1", "remote", "http://127.0.0.1:1/stale", 1))
	b.publish(t, 3, c)
	_, err = r.ReloadCustomer(context.Background(), "u1", 3)
	require.ErrorContains(t, err, "stale credential version")
	require.Equal(t, uint64(2), r.Status().Customers["u1"].Revision)
	c.Destinations[1] = b.webhook(t, "u1", "remote", "http://127.0.0.1:1/new", 6)
	b.publish(t, 3, c)
	_, err = r.ReloadCustomer(context.Background(), "u1", 3)
	require.NoError(t, err)
}
