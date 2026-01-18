// Copyright 2025 Blink Labs Software
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

package fcm

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewMessage(t *testing.T) {
	t.Run("empty token returns error", func(t *testing.T) {
		msg, err := NewMessage("")
		assert.Nil(t, msg)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "token is mandatory")
	})

	t.Run("valid token returns message", func(t *testing.T) {
		msg, err := NewMessage("valid-token-123")
		require.NoError(t, err)
		require.NotNil(t, msg)
		assert.Equal(t, "valid-token-123", msg.Token)
	})

	t.Run("with notification option", func(t *testing.T) {
		msg, err := NewMessage(
			"valid-token-123",
			WithNotification("Test Title", "Test Body"),
		)
		require.NoError(t, err)
		require.NotNil(t, msg)
		require.NotNil(t, msg.Notification)
		assert.Equal(t, "Test Title", msg.Notification.Title)
		assert.Equal(t, "Test Body", msg.Notification.Body)
	})

	t.Run("with data option", func(t *testing.T) {
		data := map[string]any{
			"key1": "value1",
			"key2": "value2",
		}
		msg, err := NewMessage(
			"valid-token-123",
			WithData(data),
		)
		require.NoError(t, err)
		require.NotNil(t, msg)
		assert.Equal(t, data, msg.Data)
	})
}
