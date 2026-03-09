package utxorpc

import (
	"testing"

	"github.com/stretchr/testify/assert"
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
