// Copyright 2026 Blink Labs Software
// SPDX-License-Identifier: Apache-2.0

package s3

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	awss3 "github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/blinklabs-io/adder/event"
	"github.com/blinklabs-io/adder/internal/logging"
	"github.com/blinklabs-io/adder/plugin"
)

const requestTimeout = 30 * time.Second

type putter interface {
	PutObject(context.Context, *awss3.PutObjectInput, ...func(*awss3.Options)) (*awss3.PutObjectOutput, error)
}

var eventNames = map[string]string{
	"blocks": event.TypeBlock, "transactions": event.TypeTransaction,
	"rollbacks": event.TypeRollback, "governance": event.TypeGovernance,
	"drep-registration": event.TypeDRepRegistration,
	"drep-update":       event.TypeDRepUpdate,
	"drep-retirement":   event.TypeDRepRetirement,
}

// Output archives selected events as individual JSON objects in S3.
type Output struct {
	plugin.Base
	region, bucket, prefix string
	selected               map[string]bool
	client                 putter
}

type Option func(*Output)

func WithRegion(v string) Option { return func(o *Output) { o.region = v } }
func WithBucket(v string) Option { return func(o *Output) { o.bucket = v } }
func WithPrefix(v string) Option { return func(o *Output) { o.prefix = v } }
func WithEventTypes(v map[string]bool) Option {
	return func(o *Output) {
		o.selected = make(map[string]bool, len(v))
		for typ, enabled := range v {
			o.selected[typ] = enabled
		}
	}
}

func New(opts ...Option) *Output {
	selected := make(map[string]bool, len(eventNames))
	for _, typ := range eventNames {
		selected[typ] = true
	}
	o := &Output{prefix: "adder/events", selected: selected}
	for _, opt := range opts {
		opt(o)
	}
	return o
}

func (o *Output) Role() plugin.PluginType { return plugin.PluginTypeOutput }
func (o *Output) Start() error            { return o.StartContext(context.Background()) }
func (o *Output) StartContext(ctx context.Context) error {
	return o.StartRun(ctx, plugin.BaseConfig{HasInput: true}, o.start, plugin.ShutdownHooks{})
}

func (o *Output) start(ctx context.Context) error {
	if err := o.validate(); err != nil {
		return err
	}
	if o.client == nil {
		cfg, err := awsconfig.LoadDefaultConfig(ctx, awsconfig.WithRegion(o.region))
		if err != nil {
			return fmt.Errorf("load AWS configuration: %w", err)
		}
		o.client = awss3.NewFromConfig(cfg)
	}
	done, in := o.Done(), o.Input()
	o.Go(func() {
		for {
			select {
			case <-ctx.Done():
				return
			case <-done:
				return
			case evt, ok := <-in:
				if !ok {
					return
				}
				if !o.selected[evt.Type] {
					continue
				}
				if err := o.archive(ctx, evt); err != nil && ctx.Err() == nil {
					o.reportError(err)
				}
			}
		}
	})
	return nil
}

func (o *Output) validate() error {
	if o.region == "" {
		return errors.New("region is required")
	}
	if o.bucket == "" || strings.ContainsAny(o.bucket, "/ \t\r\n") {
		return errors.New("bucket is required without path characters")
	}
	if strings.HasPrefix(o.prefix, "/") || strings.HasSuffix(o.prefix, "/") || strings.Contains(o.prefix, "//") || strings.Contains(o.prefix, "..") || strings.ContainsAny(o.prefix, "\x00\r\n") {
		return errors.New("prefix must be a relative S3 key prefix without empty levels")
	}
	return nil
}

func objectKey(prefix string, evt event.Event, data []byte) string {
	sum := sha256.Sum256(data)
	date := evt.Timestamp.UTC().Format("2006/01/02")
	name := evt.Timestamp.UTC().Format("20060102T150405.000000000Z") + "-" + hex.EncodeToString(sum[:]) + ".json"
	if prefix == "" {
		return date + "/" + name
	}
	return prefix + "/" + date + "/" + name
}

func (o *Output) archive(ctx context.Context, evt event.Event) error {
	data, err := json.Marshal(evt)
	if err != nil {
		return fmt.Errorf("encode S3 event: %w", err)
	}
	requestCtx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()
	_, err = o.client.PutObject(requestCtx, &awss3.PutObjectInput{
		Bucket: aws.String(o.bucket), Key: aws.String(objectKey(o.prefix, evt, data)),
		Body: bytes.NewReader(data), ContentType: aws.String("application/json"),
		ChecksumAlgorithm: types.ChecksumAlgorithmSha256,
	})
	if err != nil {
		return fmt.Errorf("archive S3 event: %w", err)
	}
	return nil
}

func (o *Output) Stop() error { return o.Shutdown(plugin.ShutdownHooks{}) }
func (o *Output) reportError(err error) {
	logger := o.Logger()
	if logger == nil {
		logger = logging.GetLoggerForComponent("output.s3")
	}
	logger.Error(err.Error())
	o.TrySendError(err)
}

var _ plugin.ManagedPlugin = (*Output)(nil)
