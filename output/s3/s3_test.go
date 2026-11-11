// Copyright 2026 Blink Labs Software
// SPDX-License-Identifier: Apache-2.0

package s3

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	awss3 "github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/blinklabs-io/adder/event"
	"github.com/blinklabs-io/adder/plugin"
	"github.com/stretchr/testify/require"
)

type fakePutter struct {
	inputs []*awss3.PutObjectInput
	data   [][]byte
	err    error
}

type callbackPutter func(context.Context, *awss3.PutObjectInput) error

func (f callbackPutter) PutObject(ctx context.Context, in *awss3.PutObjectInput, _ ...func(*awss3.Options)) (*awss3.PutObjectOutput, error) {
	if err := f(ctx, in); err != nil {
		return nil, err
	}
	return &awss3.PutObjectOutput{}, nil
}

func (f *fakePutter) PutObject(_ context.Context, in *awss3.PutObjectInput, _ ...func(*awss3.Options)) (*awss3.PutObjectOutput, error) {
	f.inputs = append(f.inputs, in)
	b, err := io.ReadAll(in.Body)
	if err != nil {
		return nil, err
	}
	f.data = append(f.data, b)
	if f.err != nil {
		return nil, f.err
	}
	return &awss3.PutObjectOutput{}, nil
}

func TestS3KeyAndPayload(t *testing.T) {
	f := &fakePutter{}
	o := New(WithRegion("us-east-1"), WithBucket("archive"), WithPrefix("events"))
	o.client = f
	evt := event.New(event.TypeRollback, time.Date(2026, 1, 2, 3, 4, 5, 6, time.FixedZone("west", -5*3600)), map[string]any{"chain": "main"}, map[string]any{"slot": 42})
	require.NoError(t, o.archive(context.Background(), evt))
	require.NoError(t, o.archive(context.Background(), evt))
	require.Len(t, f.inputs, 2)
	require.Equal(t, aws.ToString(f.inputs[0].Key), aws.ToString(f.inputs[1].Key))
	changed := evt
	changed.Payload = map[string]any{"slot": 43}
	require.NoError(t, o.archive(context.Background(), changed))
	require.NotEqual(t, aws.ToString(f.inputs[0].Key), aws.ToString(f.inputs[2].Key))
	require.Equal(t, f.data[0], f.data[1])
	require.True(t, strings.HasPrefix(aws.ToString(f.inputs[0].Key), "events/2026/01/02/20260102T080405.000000006Z-"))
	require.True(t, strings.HasSuffix(aws.ToString(f.inputs[0].Key), ".json"))
	require.Equal(t, "archive", aws.ToString(f.inputs[0].Bucket))
	require.Equal(t, "application/json", aws.ToString(f.inputs[0].ContentType))
	require.Equal(t, "SHA256", string(f.inputs[0].ChecksumAlgorithm))
	var decoded map[string]any
	require.NoError(t, json.Unmarshal(f.data[0], &decoded))
	require.Equal(t, event.TypeRollback, decoded["type"])
	require.Equal(t, float64(42), decoded["payload"].(map[string]any)["slot"])
	f.err = errors.New("denied")
	require.ErrorContains(t, o.archive(context.Background(), evt), "denied")
}

func TestS3WorkerFilteringAndErrors(t *testing.T) {
	calls := make(chan struct{}, 2)
	o := New(WithRegion("us-east-1"), WithBucket("archive"), WithEventTypes(map[string]bool{event.TypeRollback: true}))
	o.client = callbackPutter(func(_ context.Context, _ *awss3.PutObjectInput) error {
		calls <- struct{}{}
		return errors.New("upload rejected")
	})
	require.NoError(t, o.Start())
	o.InputChan() <- event.New(event.TypeBlock, time.Now(), nil, nil)
	o.InputChan() <- event.New(event.TypeRollback, time.Now(), nil, map[string]any{"slot": 1})
	select {
	case err := <-o.ErrorChan():
		require.ErrorContains(t, err, "upload rejected")
	case <-time.After(2 * time.Second):
		t.Fatal("missing upload error")
	}
	o.InputChan() <- event.New(event.TypeRollback, time.Now(), nil, make(chan int))
	select {
	case err := <-o.ErrorChan():
		require.ErrorContains(t, err, "encode S3 event")
	case <-time.After(2 * time.Second):
		t.Fatal("missing marshal error")
	}
	select {
	case <-calls:
	default:
		t.Fatal("expected upload")
	}
	select {
	case <-calls:
		t.Fatal("unexpected upload")
	default:
	}
	require.NoError(t, o.Stop())
}

func TestS3StopCancelsBlockedUpload(t *testing.T) {
	entered := make(chan struct{})
	exited := make(chan struct{})
	o := New(WithRegion("us-east-1"), WithBucket("archive"))
	o.client = callbackPutter(func(ctx context.Context, _ *awss3.PutObjectInput) error {
		close(entered)
		<-ctx.Done()
		close(exited)
		return ctx.Err()
	})
	require.NoError(t, o.Start())
	o.InputChan() <- event.New(event.TypeBlock, time.Now(), nil, nil)
	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		t.Fatal("upload did not begin")
	}
	stopped := make(chan error, 1)
	go func() { stopped <- o.Stop() }()
	select {
	case err := <-stopped:
		require.NoError(t, err)
	case <-time.After(2 * time.Second):
		t.Fatal("Stop did not join upload")
	}
	select {
	case <-exited:
	default:
		t.Fatal("upload did not observe cancellation")
	}
}

func TestS3Configuration(t *testing.T) {
	for _, v := range []map[string]any{
		nil, {"region": "us-east-1"}, {"region": "us-east-1", "bucket": "bad/name"},
		{"region": "us-east-1", "bucket": "archive", "prefix": "../bad"},
		{"region": "us-east-1", "bucket": "archive", "event-types": "unknown"},
		{"region": "us-east-1", "bucket": "archive", "event-types": ""},
	} {
		_, err := plugin.GetPlugin(plugin.PluginTypeOutput, "s3", v)
		require.Error(t, err)
	}
	p, err := plugin.GetPlugin(plugin.PluginTypeOutput, "s3", map[string]any{"region": "us-east-1", "bucket": "archive", "event-types": "rollbacks"})
	require.NoError(t, err)
	require.True(t, p.(*Output).selected[event.TypeRollback])
	require.False(t, p.(*Output).selected[event.TypeBlock])
}

func TestS3SDKPutObjectRequest(t *testing.T) {
	var method, path, contentType, auth, checksum string
	var body []byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		method, path = r.Method, r.URL.Path
		contentType, auth = r.Header.Get("Content-Type"), r.Header.Get("Authorization")
		checksum = r.Header.Get("X-Amz-Checksum-Sha256")
		var err error
		body, err = io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read request: %v", err)
		}
		w.Header().Set("ETag", `"test-etag"`)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	client := awss3.NewFromConfig(aws.Config{
		Region:      "us-east-1",
		Credentials: credentials.NewStaticCredentialsProvider("test-key", "test-secret", ""),
		HTTPClient:  server.Client(),
	}, func(o *awss3.Options) { o.BaseEndpoint = aws.String(server.URL); o.UsePathStyle = true })
	o := New(WithRegion("us-east-1"), WithBucket("archive"), WithPrefix("events"))
	o.client = client
	evt := event.New(event.TypeBlock, time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC), nil, map[string]any{"height": 3})
	require.NoError(t, o.archive(context.Background(), evt))
	require.Equal(t, http.MethodPut, method)
	require.True(t, strings.HasPrefix(path, "/archive/events/2026/01/02/"), path)
	require.Equal(t, "application/json", contentType)
	require.Contains(t, auth, "AWS4-HMAC-SHA256")
	require.NotEmpty(t, checksum)
	var decoded event.Event
	require.NoError(t, json.Unmarshal(body, &decoded))
	require.Equal(t, event.TypeBlock, decoded.Type)
}
