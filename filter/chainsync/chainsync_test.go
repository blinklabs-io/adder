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

package chainsync

import "testing"

// MockLogger is a mock implementation of the plugin.Logger interface
type MockLogger struct{}

func (l *MockLogger) Info(msg string, args ...interface{})  {}
func (l *MockLogger) Error(msg string, args ...interface{}) {}
func (l *MockLogger) Debug(msg string, args ...interface{}) {}
func (l *MockLogger) Warn(msg string, args ...interface{})  {}
func (l *MockLogger) Trace(msg string, args ...interface{}) {}

func TestNewChainSync(t *testing.T) {
	c := New()
	if c == nil {
		t.Fatalf("expected non-nil ChainSync instance")
	}
}

func TestChainSync_Start(t *testing.T) {
	c := New()
	err := c.Start()
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	// Additional checks can be added here
}

func TestChainSync_Stop(t *testing.T) {
	c := New()
	err := c.Stop()
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	// Check if channels are closed
	select {
	case <-c.inputChan:
	default:
		t.Fatalf("expected inputChan to be closed")
	}
	select {
	case <-c.outputChan:
	default:
		t.Fatalf("expected outputChan to be closed")
	}
	select {
	case <-c.errorChan:
	default:
		t.Fatalf("expected errorChan to be closed")
	}
}

func TestChainSync_ErrorChan(t *testing.T) {
	c := New()
	if c.ErrorChan() == nil {
		t.Fatalf("expected non-nil errorChan")
	}
}

func TestChainSync_InputChan(t *testing.T) {
	c := New()
	if c.InputChan() == nil {
		t.Fatalf("expected non-nil inputChan")
	}
}

func TestChainSync_OutputChan(t *testing.T) {
	c := New()
	if c.OutputChan() == nil {
		t.Fatalf("expected non-nil outputChan")
	}
}
