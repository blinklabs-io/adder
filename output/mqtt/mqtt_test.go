// Copyright 2026 Blink Labs Software
// SPDX-License-Identifier: Apache-2.0

package mqtt

import (
	"bufio"
	"context"
	"encoding/binary"
	"encoding/json"
	"io"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/blinklabs-io/adder/event"
	"github.com/blinklabs-io/adder/plugin"
	"github.com/stretchr/testify/require"
)

type publication struct {
	topic   string
	qos     byte
	retain  bool
	version byte
	payload []byte
}

func packet(r *bufio.Reader) (byte, []byte, error) {
	header, err := r.ReadByte()
	if err != nil {
		return 0, nil, err
	}
	var size int
	for shift := uint(0); shift < 28; shift += 7 {
		b, err := r.ReadByte()
		if err != nil {
			return 0, nil, err
		}
		size |= int(b&0x7f) << shift
		if b&0x80 == 0 {
			break
		}
	}
	data := make([]byte, size)
	_, err = io.ReadFull(r, data)
	return header, data, err
}

func broker(t *testing.T, ack bool) (string, <-chan publication) {
	return brokerWithQoS2Reason(t, ack, 0)
}

func brokerWithQoS2Reason(t *testing.T, ack bool, qos2Reason byte) (string, <-chan publication) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	publications := make(chan publication, 2)
	done := make(chan struct{})
	stop := make(chan struct{})
	t.Cleanup(func() { close(stop); _ = listener.Close(); <-done })
	go func() {
		defer close(done)
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		go func() { <-stop; _ = conn.Close() }()
		r := bufio.NewReader(conn)
		h, connectData, err := packet(r)
		if err != nil || h != 0x10 || len(connectData) < 7 {
			return
		}
		version := connectData[6]
		_, _ = conn.Write([]byte{0x20, 0x03, 0x00, 0x00, 0x00}) // MQTT 5 CONNACK accepted
		for {
			h, data, err := packet(r)
			if err != nil {
				return
			}
			if h == 0x62 && ack && len(data) >= 2 {
				if qos2Reason == 0 {
					_, _ = conn.Write([]byte{0x70, 0x02, data[0], data[1]}) // PUBCOMP success
				} else {
					_, _ = conn.Write([]byte{0x70, 0x04, data[0], data[1], qos2Reason, 0x00})
				}
				continue
			}
			if h>>4 != 3 || len(data) < 2 {
				continue
			}
			topicLen := int(binary.BigEndian.Uint16(data[:2]))
			if len(data) < topicLen+2 {
				return
			}
			pos := topicLen + 2
			qos := (h >> 1) & 3
			var packetID []byte
			if qos > 0 {
				if len(data) < pos+2 {
					return
				}
				packetID = data[pos : pos+2]
				pos += 2
			}
			if len(data) <= pos || data[pos] != 0 {
				return
			} // empty MQTT 5 properties
			pos++
			publications <- publication{topic: string(data[2 : topicLen+2]), qos: qos, retain: h&1 != 0, version: version, payload: data[pos:]}
			if ack && qos == 1 {
				_, _ = conn.Write([]byte{0x40, 0x02, packetID[0], packetID[1]})
			} else if ack && qos == 2 {
				_, _ = conn.Write([]byte{0x50, 0x02, packetID[0], packetID[1]}) // PUBREC
			}
		}
	}()
	return "mqtt://" + listener.Addr().String(), publications
}

func TestQoS2PacketIdentifierNotFoundIsUncertain(t *testing.T) {
	url, _ := brokerWithQoS2Reason(t, true, 0x92)
	o := New(WithBroker(url), WithQoS(2))
	require.NoError(t, o.Start())
	t.Cleanup(func() { require.NoError(t, o.Stop()) })
	evt := event.New(event.TypeRollback, time.Now(), nil, event.RollbackEvent{SlotNumber: 42})
	require.ErrorContains(t, o.publish(context.Background(), evt), "completion uncertain")
}

func TestQoS0AndQoS2CompleteAccordingToProtocol(t *testing.T) {
	for _, qos := range []byte{0, 2} {
		t.Run(string(rune('0'+qos)), func(t *testing.T) {
			url, publications := broker(t, true)
			o := New(WithBroker(url), WithQoS(qos))
			require.NoError(t, o.Start())
			t.Cleanup(func() { require.NoError(t, o.Stop()) })
			evt := event.New(event.TypeRollback, time.Now(), nil, event.RollbackEvent{SlotNumber: 42})
			require.NoError(t, o.publish(context.Background(), evt))
			select {
			case got := <-publications:
				require.Equal(t, qos, got.qos)
			case <-time.After(time.Second):
				t.Fatal("no publication")
			}
		})
	}
}

func TestPublishUsesDocumentedQoS1PacketAndTopic(t *testing.T) {
	url, publications := broker(t, true)
	o := New(WithBroker(url), WithTopicPrefix("cardano/adder"))
	require.NoError(t, o.Start())
	t.Cleanup(func() { require.NoError(t, o.Stop()) })
	o.InputChan() <- event.New(event.TypeTransaction, time.Now(), nil, map[string]any{"hash": "filtered"})
	o.InputChan() <- event.New(event.TypeRollback, time.Now(), nil, event.RollbackEvent{SlotNumber: 42})
	select {
	case got := <-publications:
		require.Equal(t, "cardano/adder/rollback", got.topic)
		require.Equal(t, byte(1), got.qos)
		require.False(t, got.retain)
		require.Equal(t, byte(5), got.version)
		var evt event.Event
		require.NoError(t, json.Unmarshal(got.payload, &evt))
		require.Equal(t, event.TypeRollback, evt.Type)
	case <-time.After(3 * time.Second):
		t.Fatal("no MQTT publication")
	}
}

func TestFactoryRejectsInvalidConfiguration(t *testing.T) {
	for _, tc := range []struct {
		values  map[string]any
		message string
	}{
		{map[string]any{"broker": "https://example.org"}, "broker"},
		{map[string]any{"broker": "mqtt://user:secret@example.org"}, "broker"},
		{map[string]any{"broker": "mqtt://example.org", "username": "user", "password": "secret"}, "tls://"},
		{map[string]any{"broker": "mqtt://example.org:"}, "port"},
		{map[string]any{"topic-prefix": "bad/#"}, "topic-prefix"},
		{map[string]any{"topic-prefix": string([]byte{0xff})}, "topic-prefix"},
		{map[string]any{"topic-prefix": strings.Repeat("a", 65535)}, "65535"},
		{map[string]any{"qos": 3}, "qos"},
		{map[string]any{"event-types": "unknown"}, "event type"},
	} {
		p, err := plugin.GetPlugin(plugin.PluginTypeOutput, "mqtt", tc.values)
		require.Nil(t, p)
		require.ErrorContains(t, err, tc.message)
		require.NotContains(t, err.Error(), "secret")
	}
}

func TestTopicLimitCountsUTF8BytesAndEventSuffix(t *testing.T) {
	selected := map[string]bool{event.TypeRollback: true}
	maxPrefix := 65535 - len("/rollback")
	require.NoError(t, validateTopics(strings.Repeat("a", maxPrefix), selected))
	require.ErrorContains(t, validateTopics(strings.Repeat("a", maxPrefix+1), selected), "65535")
	utf8Prefix := strings.Repeat("é", maxPrefix/2)
	require.NoError(t, validateTopics(utf8Prefix, selected))
	require.ErrorContains(t, validateTopics(utf8Prefix+"é", selected), "65535")
}

func TestMissingPUBACKReportsUncertainDelivery(t *testing.T) {
	url, publications := broker(t, false)
	o := New(WithBroker(url))
	o.publishTimeout = 100 * time.Millisecond
	require.NoError(t, o.Start())
	t.Cleanup(func() { require.NoError(t, o.Stop()) })
	o.InputChan() <- event.New(event.TypeRollback, time.Now(), nil, event.RollbackEvent{SlotNumber: 42})
	select {
	case <-publications:
	case <-time.After(time.Second):
		t.Fatal("publish packet not sent")
	}
	select {
	case err := <-o.ErrorChan():
		require.ErrorContains(t, err, "delivery uncertain")
	case <-time.After(time.Second):
		t.Fatal("missing PUBACK was not reported")
	}
}

func TestFactoryAcceptsTLSAndIPv6(t *testing.T) {
	for _, broker := range []string{"tls://broker.example.org:8883", "mqtt://[::1]:1883"} {
		p, err := plugin.GetPlugin(plugin.PluginTypeOutput, "mqtt", map[string]any{"broker": broker})
		require.NoError(t, err)
		require.NotNil(t, p)
	}
}

func TestBrokerURLAddsDefaultPorts(t *testing.T) {
	for _, tc := range []struct{ raw, host string }{
		{"mqtt://broker.example.org", "broker.example.org:1883"},
		{"tls://[::1]", "[::1]:8883"},
		{"mqtt://broker.example.org:1234", "broker.example.org:1234"},
	} {
		u, err := brokerURL(tc.raw)
		require.NoError(t, err)
		require.Equal(t, tc.host, u.Host)
	}
}

func TestBoundedTransportWriteCannotHangOnStalledReader(t *testing.T) {
	writer, reader := net.Pipe()
	defer reader.Close()
	ctx, cancel := context.WithCancel(context.Background())
	conn := newBoundedConn(ctx, writer, 100*time.Millisecond)
	_, packetLocked := conn.(sync.Locker)
	require.True(t, packetLocked, "Paho requires packet-level write locking")
	defer conn.Close()
	done := make(chan error, 1)
	go func() { _, err := conn.Write([]byte("stalled")); done <- err }()
	select {
	case err := <-done:
		require.Error(t, err)
	case <-time.After(time.Second):
		t.Fatal("stalled writer did not time out")
	}
	second := make(chan error, 1)
	go func() { _, err := conn.Write([]byte("cancelled")); second <- err }()
	cancel()
	select {
	case err := <-second:
		require.Error(t, err)
	case <-time.After(time.Second):
		t.Fatal("cancellation did not close transport")
	}
}
