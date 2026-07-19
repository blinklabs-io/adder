// Copyright 2026 Blink Labs Software
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package config

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	yamlv3 "gopkg.in/yaml.v3"
)

// failMarshaler triggers yamlv3.Encoder.Encode to return an error
// (instead of panicking on a channel/function) by implementing yaml's
// Marshaler interface and returning an error directly.
type failMarshaler struct{}

func (failMarshaler) MarshalYAML() (any, error) {
	return nil, errors.New("intentional marshal failure")
}

// assertNoTmpResidue verifies SaveAtomic left no `.tmp.*` file behind
// for the given destination path. SaveAtomic uses unique tmp suffixes
// (pid + nanoseconds), so a per-test glob is the correct way to check
// cleanup — the previously-used `path + ".tmp"` literal check would
// pass vacuously now.
func assertNoTmpResidue(t *testing.T, path string) {
	t.Helper()
	matches, err := filepath.Glob(path + ".tmp.*")
	require.NoError(t, err)
	assert.Empty(t, matches, "no .tmp.* residue should remain on disk")
}

func TestSaveAtomicWritesAndRenames(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sub", "out.yaml")
	type payload struct {
		Name string `yaml:"name"`
		Age  int    `yaml:"age"`
	}
	require.NoError(t, SaveAtomic(path, payload{Name: "x", Age: 1}))

	// Final file exists; no .tmp leftover.
	buf, err := os.ReadFile(path)
	require.NoError(t, err)
	var got payload
	require.NoError(t, yamlv3.Unmarshal(buf, &got))
	assert.Equal(t, "x", got.Name)
	assert.Equal(t, 1, got.Age)

	assertNoTmpResidue(t, path)
}

func TestSaveAtomicOverwritesExisting(t *testing.T) {
	path := filepath.Join(t.TempDir(), "out.yaml")
	require.NoError(t, os.WriteFile(path, []byte("old: true\n"), 0o600))
	require.NoError(t, SaveAtomic(path, map[string]int{"x": 42}))

	buf, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Contains(t, string(buf), "x: 42")
	// Catch a future regression that drops O_TRUNC: old contents must be
	// gone, not just appended-to.
	assert.NotContains(
		t,
		string(buf),
		"old: true",
		"old contents must be truncated, not preserved",
	)
}

func TestSaveAtomicMkdirError(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("perm semantics differ on windows")
	}
	parent := t.TempDir()
	blocker := filepath.Join(parent, "blocker")
	require.NoError(t, os.WriteFile(blocker, []byte("x"), 0o600))
	// Attempting to create a directory under a regular file fails with
	// ENOTDIR on POSIX systems.
	target := filepath.Join(blocker, "sub", "out.yaml")
	err := SaveAtomic(target, map[string]int{"x": 1})
	require.Error(t, err)
	// Exact prefix match so a future refactor that reorders MkdirAll
	// vs CreateTemp would fail this test (both error wrappers start
	// with "creating"; differentiate the source).
	assert.ErrorContains(t, err, "creating directory")
	assert.NotContains(t, err.Error(), "creating temporary file")
}

func TestSaveAtomicEncodeError(t *testing.T) {
	path := filepath.Join(t.TempDir(), "out.yaml")
	err := SaveAtomic(path, failMarshaler{})
	require.Error(t, err)
	assert.ErrorContains(t, err, "encoding")
	_, statErr := os.Stat(path)
	assert.True(t, os.IsNotExist(statErr), "no partial final file")
	// Encode-error path used to leak the .tmp file. Lock down cleanup.
	assertNoTmpResidue(t, path)
}

func TestSaveAtomicRenameOverDirectory(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("rename-over-dir semantics differ on windows")
	}
	dir := t.TempDir()
	// Pre-create `out.yaml` as a non-empty *directory* so rename fails.
	target := filepath.Join(dir, "out.yaml")
	require.NoError(t, os.Mkdir(target, 0o700))
	require.NoError(
		t,
		os.WriteFile(filepath.Join(target, "x"), []byte("y"), 0o600),
	)

	err := SaveAtomic(target, map[string]int{"x": 1})
	require.Error(t, err)
	assert.ErrorContains(t, err, "renaming")
	assertNoTmpResidue(t, target)
}

// TestSaveAtomicConcurrentSamePath stresses the unique-tmp-suffix
// guarantee: many goroutines saving to the same destination must not
// truncate each other's in-flight tmp file, the final file must
// contain exactly one of the payloads (last rename wins), and no
// .tmp.* residue may remain. Without the unique suffix, concurrent
// O_TRUNC opens would mangle the encoded YAML and assert.Contains
// below would intermittently fail.
func TestSaveAtomicConcurrentSamePath(t *testing.T) {
	path := filepath.Join(t.TempDir(), "out.yaml")
	const n = 32
	var wg sync.WaitGroup
	errs := make([]error, n)
	for i := range n {
		wg.Go(func() {
			errs[i] = SaveAtomic(path, map[string]int{"x": i})
		})
	}
	wg.Wait()
	for i, err := range errs {
		require.NoErrorf(t, err, "goroutine %d", i)
	}

	buf, err := os.ReadFile(path)
	require.NoError(t, err)
	var got map[string]int
	require.NoError(
		t,
		yamlv3.Unmarshal(buf, &got),
		"final file must be valid YAML, not interleaved bytes",
	)
	x, ok := got["x"]
	require.True(t, ok, "final file must contain key x: got %q", buf)
	assert.GreaterOrEqual(t, x, 0)
	assert.Less(t, x, n)
	assertNoTmpResidue(t, path)
}

// TestSaveAtomicFsyncOnSuccess does NOT directly assert f.Sync /
// dir.Sync were called (that requires injecting a fake filesystem),
// but it does exercise the post-fix success path end-to-end on a
// real filesystem under -race to confirm no panics, no fd leaks,
// and a readable file. Acts as a regression smoke test for the
// fsync block.
func TestSaveAtomicFsyncOnSuccess(t *testing.T) {
	path := filepath.Join(t.TempDir(), "out.yaml")
	require.NoError(t, SaveAtomic(path, map[string]string{"k": "v"}))
	buf, err := os.ReadFile(path)
	require.NoError(t, err)
	var got map[string]string
	require.NoError(t, yamlv3.Unmarshal(buf, &got))
	assert.Equal(t, "v", got["k"])
	assertNoTmpResidue(t, path)
}

// TestSaveAtomicSerializesPerPath proves the per-path mutex serializes
// concurrent saves: at most one MarshalYAML call may be running at
// any moment. We measure inside MarshalYAML (which executes under
// SaveAtomic's mutex), not at the goroutine wrapper level (which
// would observe goroutines blocked-waiting on the mutex as
// "in-flight").
func TestSaveAtomicSerializesPerPath(t *testing.T) {
	path := filepath.Join(t.TempDir(), "out.yaml")
	const n = 16
	var inFlight, maxInFlight atomic.Int32
	errs := make(chan error, n)
	var wg sync.WaitGroup
	for range n {
		wg.Go(func() {
			// require.* in goroutines is unsafe (FailNow on the
			// wrong goroutine). Collect errors and assert below.
			errs <- SaveAtomic(path, &spyPayload{
				inFlight:    &inFlight,
				maxInFlight: &maxInFlight,
			})
		})
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		require.NoError(t, err)
	}
	assert.Equal(
		t,
		int32(1),
		maxInFlight.Load(),
		"per-path mutex must allow at most one MarshalYAML in flight",
	)
}

// spyPayload's MarshalYAML records the live concurrency observed
// inside SaveAtomic's critical section. The sleep widens the window
// so a missing mutex would deterministically expose >1 concurrent
// calls.
type spyPayload struct {
	inFlight    *atomic.Int32
	maxInFlight *atomic.Int32
}

func (p *spyPayload) MarshalYAML() (any, error) {
	cur := p.inFlight.Add(1)
	defer p.inFlight.Add(-1)
	for {
		m := p.maxInFlight.Load()
		if cur <= m {
			break
		}
		if p.maxInFlight.CompareAndSwap(m, cur) {
			break
		}
	}
	time.Sleep(2 * time.Millisecond)
	return map[string]int{"x": 1}, nil
}

func TestSaveAtomicSerializesDifferentPathsConcurrent(t *testing.T) {
	dir := t.TempDir()
	const n = 8
	errs := make(chan error, n)
	var wg sync.WaitGroup
	for i := range n {
		wg.Go(func() {
			path := filepath.Join(dir, "out-"+strconv.Itoa(i)+".yaml")
			errs <- SaveAtomic(path, map[string]int{"i": i})
		})
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		require.NoError(t, err)
	}
	// Different paths must not be serialized by the per-path mutex
	// (otherwise the design would create needless contention). We
	// cannot assert parallelism directly without timing flakiness;
	// the assertion here is that ALL N files exist with correct
	// contents, proving the per-path keying actually distinguishes
	// paths.
	for i := range n {
		path := filepath.Join(dir, "out-"+strconv.Itoa(i)+".yaml")
		buf, err := os.ReadFile(path)
		require.NoError(t, err)
		var got map[string]int
		require.NoError(t, yamlv3.Unmarshal(buf, &got))
		assert.Equal(t, i, got["i"])
	}
}
