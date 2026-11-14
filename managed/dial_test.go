// Copyright 2026 Blink Labs Software
// SPDX-License-Identifier: Apache-2.0

package managed

import (
	"context"
	"errors"
	"net"
	"net/netip"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestManagedDialTriesValidatedAddresses(t *testing.T) {
	r, err := New(Options{})
	require.NoError(t, err)
	ips := []netip.Addr{netip.MustParseAddr("2606:4700:4700::1111"), netip.MustParseAddr("1.1.1.1")}
	var calls []string
	client, server := net.Pipe()
	defer client.Close()
	defer server.Close()
	conn, err := r.dialResolved(t.Context(), "tcp", "443", ips, func(ctx context.Context, network, address string) (net.Conn, error) {
		require.Equal(t, "tcp", network)
		calls = append(calls, address)
		if len(calls) == 1 {
			return nil, errors.New("network unreachable")
		}
		return client, nil
	})
	require.NoError(t, err)
	require.Same(t, client, conn)
	require.Equal(t, []string{"[2606:4700:4700::1111]:443", "1.1.1.1:443"}, calls)
}

func TestManagedDialRejectsEntireMixedAnswerBeforeConnecting(t *testing.T) {
	r, err := New(Options{})
	require.NoError(t, err)
	_, err = r.dialResolved(t.Context(), "tcp", "443", []netip.Addr{netip.MustParseAddr("1.1.1.1"), netip.MustParseAddr("127.0.0.1")}, func(context.Context, string, string) (net.Conn, error) {
		t.Fatal("must validate the entire answer before connecting")
		return nil, nil
	})
	require.ErrorContains(t, err, "resolved address forbidden")
}

func TestManagedDialLeavesTimeForRemainingAddresses(t *testing.T) {
	r, err := New(Options{})
	require.NoError(t, err)
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	calls := 0
	_, err = r.dialResolved(ctx, "tcp", "443", []netip.Addr{netip.MustParseAddr("1.1.1.1"), netip.MustParseAddr("1.0.0.1")}, func(attempt context.Context, _, _ string) (net.Conn, error) {
		calls++
		if calls == 1 {
			deadline, ok := attempt.Deadline()
			require.True(t, ok)
			outer, _ := ctx.Deadline()
			require.True(t, deadline.Before(outer), "one address must not consume the entire dial budget")
			return nil, context.DeadlineExceeded
		}
		cancel()
		return nil, context.Canceled
	})
	require.Error(t, err)
	require.Equal(t, 2, calls)
}
