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

//go:build darwin

package setup

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDarwinServicePlistDirAndPath(t *testing.T) {
	dir := servicePlistDir()
	assert.True(t, strings.HasSuffix(dir, "Library/LaunchAgents"))

	path := serviceUnitPath()
	assert.True(t, strings.HasSuffix(path, "Library/LaunchAgents/io.blinklabs.adder.plist"))
}

func TestDarwinRenderUnitAutoStart(t *testing.T) {
	cfgAutoStart := ServiceConfig{
		BinaryPath: "/Applications/AdderTray.app/Contents/MacOS/adder",
		ConfigPath: "/Users/test/Library/Application Support/Adder/config.yaml",
		LogDir:     "/Users/test/Library/Logs/Adder",
		AutoStart:  true,
	}

	data, err := renderUnit(cfgAutoStart)
	require.NoError(t, err)
	content := string(data)

	assert.Contains(t, content, "<key>RunAtLoad</key>\n    <true/>")
	assert.Contains(t, content, "<key>KeepAlive</key>\n    <true/>")
	assert.Contains(t, content, "<string>/Applications/AdderTray.app/Contents/MacOS/adder</string>")
	assert.Contains(t, content, "<string>--config</string>")

	cfgNoAutoStart := ServiceConfig{
		BinaryPath: "/Applications/AdderTray.app/Contents/MacOS/adder",
		ConfigPath: "/Users/test/Library/Application Support/Adder/config.yaml",
		LogDir:     "/Users/test/Library/Logs/Adder",
		AutoStart:  false,
	}

	dataNoAuto, err := renderUnit(cfgNoAutoStart)
	require.NoError(t, err)
	contentNoAuto := string(dataNoAuto)

	assert.Contains(t, contentNoAuto, "<key>RunAtLoad</key>\n    <false/>")
	assert.Contains(t, contentNoAuto, "<key>KeepAlive</key>\n    <false/>")
}

func TestDarwinRenderUnitXmlEscape(t *testing.T) {
	cfg := ServiceConfig{
		BinaryPath: "/path/with <special> & \"quotes\"/adder",
		AutoStart:  true,
	}

	data, err := renderUnit(cfg)
	require.NoError(t, err)
	content := string(data)

	assert.Contains(t, content, "/path/with &lt;special&gt; &amp; &#34;quotes&#34;/adder")
}

func TestFindAppBundlePath(t *testing.T) {
	// If /Applications/AdderTray.app exists, it returns it
	path := findAppBundlePath()
	if path != "" {
		assert.True(t, strings.HasSuffix(path, ".app"))
	}
}

func TestUpdateDarwinLoginItemNoPanic(t *testing.T) {
	// Should execute gracefully without panicking
	assert.NotPanics(t, func() {
		_ = updateDarwinLoginItem(false)
	})
	err := updateDarwinLoginItem(false)
	assert.NoError(t, err)
}
