// Copyright 2026 Blink Labs Software
// SPDX-License-Identifier: Apache-2.0

package managed

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestManagedCredentialKeysRejectedBeforeFileRead(t *testing.T) {
	b := newAcceptanceBundle(t)
	c := localCustomer("u1")
	c.Destinations = []Destination{{ID: "remote", Plugin: "webhook", Credentials: map[string]CredentialRef{"unsupported": {File: "missing.json", Version: 1}}}}
	b.publish(t, 1, c)
	r, err := New(b.options)
	require.NoError(t, err)
	require.ErrorContains(t, r.StartContext(context.Background()), "credential option not permitted")
	require.NoError(t, r.Stop())
}

func TestManagedRepeatedCredentialReferencesSharePreparation(t *testing.T) {
	b := newAcceptanceBundle(t)
	b.options.MaxRetainedSize = 8 << 20
	b.options.MaxEventSize = 1024
	b.options.InputQueue, b.options.DestinationQueue = 1, 1
	c := localCustomer("u1")
	first := b.webhook(t, "u1", "first", "http://127.0.0.1:1/hook", 1)
	second := b.webhook(t, "u1", "second", "http://127.0.0.1:1/hook", 1)
	ref := CredentialRef{File: "u1-auth.json", Version: 1}
	publishJSON(t, filepath.Join(b.options.CredentialsDir, ref.File), credential{CustomerID: "u1", Version: 1, Value: strings.Repeat("x", 300<<10)})
	for _, d := range []*Destination{&first, &second} {
		d.Credentials["username"], d.Credentials["password"] = ref, ref
	}
	c.Destinations = []Destination{first, second}
	b.publish(t, 1, c)
	r := b.start(t, nil)
	require.Equal(t, int64(2), r.Status().PluginInstances)
	require.Less(t, r.Status().RetainedSize, b.options.MaxRetainedSize)
	require.NoError(t, r.Stop())
	require.Zero(t, r.Status().RetainedSize)
}

func TestManagedCredentialPreparationRejectsDecodedBudget(t *testing.T) {
	b := newAcceptanceBundle(t)
	b.options.MaxRetainedSize = 64 << 10
	b.options.MaxEventSize = 1024
	c := localCustomer("u1")
	d := b.webhook(t, "u1", "remote", "http://127.0.0.1:1/hook", 1)
	ref := CredentialRef{File: "u1-auth.json", Version: 1}
	publishJSON(t, filepath.Join(b.options.CredentialsDir, ref.File), credential{CustomerID: "u1", Version: 1, Value: strings.Repeat("x", 128<<10)})
	d.Credentials["username"] = ref
	c.Destinations = []Destination{d}
	b.publish(t, 1, c)
	r, err := New(b.options)
	require.NoError(t, err)
	budget := &preparationBudget{router: r}
	defer budget.release()
	_, err = r.loadCustomer(CustomerRef{ID: "u1", Revision: 1, File: "u1.json"}, budget)
	require.LessOrEqual(t, r.memory.Load(), b.options.MaxRetainedSize)
	require.ErrorContains(t, err, "budget")
}
