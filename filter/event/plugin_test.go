// Copyright 2026 Blink Labs Software
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//	http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package event

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestConfiguredPluginEventTypes(t *testing.T) {
	testCases := []struct {
		name     string
		input    string
		expected []string
	}{
		{
			name:     "no whitespace",
			input:    "block,transaction",
			expected: []string{"block", "transaction"},
		},
		{
			name:     "YAML folded scalar (spaces after commas)",
			input:    "block, transaction, rollback",
			expected: []string{"block", "transaction", "rollback"},
		},
		{
			name:     "YAML literal scalar (newlines and tabs)",
			input:    "block,\n\ttransaction\n",
			expected: []string{"block", "transaction"},
		},
		{
			// A folded scalar only replaces newlines with spaces for equally
			// indented lines; a more-indented line keeps its newline and
			// leading indent
			name:     "YAML folded scalar (more-indented line)",
			input:    "block,\n  transaction,\nrollback",
			expected: []string{"block", "transaction", "rollback"},
		},
		{
			name:     "single type with leading space",
			input:    " block",
			expected: []string{"block"},
		},
		{
			name:     "empty entries dropped",
			input:    "block,,  ,transaction,",
			expected: []string{"block", "transaction"},
		},
		{
			name:     "whitespace only leaves filter unset",
			input:    "  ,\n ",
			expected: nil,
		},
		{
			name:     "empty leaves filter unset",
			input:    "",
			expected: nil,
		},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			values := map[string]any{"type": testCase.input}
			e, ok := mustConfiguredPlugin(t, values).(*Event)
			assert.True(t, ok, "plugin should be an *Event")
			assert.Equal(t, testCase.expected, e.filterTypes)
		})
	}
}
