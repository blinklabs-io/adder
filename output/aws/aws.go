// Copyright 2026 Blink Labs Software
// SPDX-License-Identifier: Apache-2.0

package aws

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/sns"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
	"github.com/blinklabs-io/adder/event"
	"github.com/blinklabs-io/adder/internal/logging"
	"github.com/blinklabs-io/adder/plugin"
)

const requestTimeout = 10 * time.Second

type sqsSender interface {
	SendMessage(context.Context, *sqs.SendMessageInput, ...func(*sqs.Options)) (*sqs.SendMessageOutput, error)
}
type snsSender interface {
	Publish(context.Context, *sns.PublishInput, ...func(*sns.Options)) (*sns.PublishOutput, error)
}

var eventNames = map[string]string{
	"blocks": event.TypeBlock, "transactions": event.TypeTransaction,
	"rollbacks": event.TypeRollback, "governance": event.TypeGovernance,
	"drep-registration": event.TypeDRepRegistration,
	"drep-update":       event.TypeDRepUpdate,
	"drep-retirement":   event.TypeDRepRetirement,
}

// Output sends selected events to one SQS queue or SNS topic.
type Output struct {
	plugin.Base
	kind, region, queueURL, topicARN, groupID string
	selected                                  map[string]bool
	maxMessageBytes                           int
	sqsClient                                 sqsSender
	snsClient                                 snsSender
}

type Option func(*Output)

func WithRegion(v string) Option       { return func(o *Output) { o.region = v } }
func WithQueueURL(v string) Option     { return func(o *Output) { o.queueURL = v } }
func WithTopicARN(v string) Option     { return func(o *Output) { o.topicARN = v } }
func WithGroupID(v string) Option      { return func(o *Output) { o.groupID = v } }
func WithMaxMessageBytes(v int) Option { return func(o *Output) { o.maxMessageBytes = v } }
func WithEventTypes(v map[string]bool) Option {
	return func(o *Output) {
		o.selected = make(map[string]bool, len(v))
		for typ, enabled := range v {
			o.selected[typ] = enabled
		}
	}
}
func NewSQS(opts ...Option) *Output { return newOutput("sqs", opts...) }
func NewSNS(opts ...Option) *Output { return newOutput("sns", opts...) }
func newOutput(kind string, opts ...Option) *Output {
	o := &Output{kind: kind, maxMessageBytes: 256 << 10, selected: map[string]bool{event.TypeBlock: true, event.TypeRollback: true, event.TypeGovernance: true}}
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
	if (o.kind == "sqs" && o.sqsClient == nil) || (o.kind == "sns" && o.snsClient == nil) {
		cfg, err := awsconfig.LoadDefaultConfig(ctx, awsconfig.WithRegion(o.region))
		if err != nil {
			return fmt.Errorf("load AWS configuration: %w", err)
		}
		if o.kind == "sqs" {
			o.sqsClient = sqs.NewFromConfig(cfg)
		} else {
			o.snsClient = sns.NewFromConfig(cfg)
		}
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
				if err := o.publish(ctx, evt); err != nil && ctx.Err() == nil {
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
	switch o.kind {
	case "sqs":
		u, err := url.Parse(o.queueURL)
		if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Path == "" || u.Path == "/" {
			return errors.New("queue-url must be an HTTPS SQS queue URL")
		}
	case "sns":
		parts := strings.Split(o.topicARN, ":")
		if len(parts) != 6 || parts[0] != "arn" || parts[2] != "sns" || parts[3] != o.region || parts[4] == "" || parts[5] == "" {
			return errors.New("topic-arn must be an SNS topic ARN in the configured region")
		}
		if o.maxMessageBytes < 1024 || o.maxMessageBytes > 1<<20 {
			return errors.New("max-message-bytes must be between 1024 and 1048576")
		}
	default:
		return errors.New("unknown AWS output type")
	}
	if o.isFIFO() && o.groupID == "" {
		return errors.New("group-id is required for FIFO destinations")
	}
	if len(o.groupID) > 128 {
		return errors.New("group-id must be at most 128 ASCII characters")
	}
	for i := 0; i < len(o.groupID); i++ {
		if o.groupID[i] < '!' || o.groupID[i] > '~' {
			return errors.New("group-id must contain printable ASCII without spaces")
		}
	}
	return nil
}

func (o *Output) isFIFO() bool {
	if o.kind == "sqs" {
		return strings.HasSuffix(o.queueURL, ".fifo")
	}
	return strings.HasSuffix(o.topicARN, ".fifo")
}

func (o *Output) publish(ctx context.Context, evt event.Event) error {
	data, err := json.Marshal(evt)
	if err != nil {
		return fmt.Errorf("encode AWS event: %w", err)
	}
	requestCtx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()
	if o.kind == "sqs" {
		if len(data) > 1<<20 {
			return errors.New("SQS event exceeds 1 MiB message limit")
		}
		input := &sqs.SendMessageInput{QueueUrl: aws.String(o.queueURL), MessageBody: aws.String(string(data))}
		if o.groupID != "" {
			input.MessageGroupId = aws.String(o.groupID)
		}
		if o.isFIFO() {
			input.MessageDeduplicationId = aws.String(digest(data))
		}
		if _, err := o.sqsClient.SendMessage(requestCtx, input); err != nil {
			return fmt.Errorf("send SQS message: %w", err)
		}
	} else {
		if len(data) > o.maxMessageBytes {
			return errors.New("SNS event exceeds configured message size limit")
		}
		input := &sns.PublishInput{TopicArn: aws.String(o.topicARN), Message: aws.String(string(data))}
		if o.groupID != "" {
			input.MessageGroupId = aws.String(o.groupID)
		}
		if o.isFIFO() {
			input.MessageDeduplicationId = aws.String(digest(data))
		}
		if _, err := o.snsClient.Publish(requestCtx, input); err != nil {
			return fmt.Errorf("publish SNS message: %w", err)
		}
	}
	return nil
}

func digest(data []byte) string { sum := sha256.Sum256(data); return hex.EncodeToString(sum[:]) }
func (o *Output) Stop() error   { return o.Shutdown(plugin.ShutdownHooks{}) }
func (o *Output) reportError(err error) {
	logger := o.Logger()
	if logger == nil {
		logger = logging.GetLoggerForComponent("output.aws")
	}
	logger.Error(err.Error())
	o.TrySendError(err)
}

var _ plugin.ManagedPlugin = (*Output)(nil)
