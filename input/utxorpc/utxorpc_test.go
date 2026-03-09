package utxorpc

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewAppliesOptions(t *testing.T) {
	u := New(
		WithURL("https://example.utxorpc"),
		WithMode(modeWatchTx),
		WithAPIKeyHeader("dmtr-api-key"),
		WithAPIKey("secret"),
		WithIntersectTip(false),
		WithIntersectPoint("1.abc"),
		WithAutoReconnect(false),
		WithIncludeCbor(true),
	)

	assert.Equal(t, "https://example.utxorpc", u.url)
	assert.Equal(t, modeWatchTx, u.mode)
	assert.Equal(t, "dmtr-api-key", u.apiKeyHeader)
	assert.Equal(t, "secret", u.apiKey)
	assert.False(t, u.intersectTip)
	assert.Equal(t, "1.abc", u.intersectPoint)
	assert.False(t, u.autoReconnect)
	assert.True(t, u.includeCbor)
}

func TestStartWithoutURLFails(t *testing.T) {
	u := New()

	err := u.Start()
	assert.Error(t, err)
}

func TestBuildIntersect(t *testing.T) {
	t.Run("empty", func(t *testing.T) {
		u := New()
		assert.Nil(t, u.buildIntersect())
	})
	t.Run("single point", func(t *testing.T) {
		u := New(WithIntersectPoint("12345.abcdef"))
		refs := u.buildIntersect()
		require.Len(t, refs, 1)
		assert.Equal(t, uint64(12345), refs[0].Slot)
		assert.Equal(t, []byte{0xab, 0xcd, 0xef}, refs[0].Hash)
	})
	t.Run("multiple points", func(t *testing.T) {
		u := New(WithIntersectPoint("100.aa,200.bb"))
		refs := u.buildIntersect()
		require.Len(t, refs, 2)
		assert.Equal(t, uint64(100), refs[0].Slot)
		assert.Equal(t, uint64(200), refs[1].Slot)
	})
	t.Run("invalid point skipped", func(t *testing.T) {
		u := New(WithIntersectPoint("bad,200.bb"))
		refs := u.buildIntersect()
		require.Len(t, refs, 1)
		assert.Equal(t, uint64(200), refs[0].Slot)
	})
}
