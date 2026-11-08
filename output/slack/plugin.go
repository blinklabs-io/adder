// Copyright 2026 Blink Labs Software
// SPDX-License-Identifier: Apache-2.0

package slack

import (
	"errors"
	"fmt"
	"net/url"

	"github.com/blinklabs-io/adder/internal/logging"
	"github.com/blinklabs-io/adder/plugin"
)

func init() {
	plugin.Register(plugin.PluginEntry{
		Type:               plugin.PluginTypeOutput,
		Name:               "slack",
		Description:        "send selected events to Slack incoming webhooks",
		NewFromOptionsFunc: newFromOptions,
		Options: []plugin.PluginOption{
			{
				Name: "webhook-urls", Type: plugin.PluginOptionTypeString,
				Description: "comma-separated Slack incoming webhook URLs", DefaultValue: "",
			},
			{
				Name: "event-types", Type: plugin.PluginOptionTypeString,
				Description: "comma-separated event types for Slack delivery", DefaultValue: "blocks,transactions,rollbacks,governance",
			},
		},
	})
}

func newFromOptions(values plugin.Options) (plugin.ManagedPlugin, error) {
	urls := plugin.SplitAndTrim(values.String("webhook-urls"))
	if len(urls) == 0 {
		return nil, errors.New("webhook-urls must contain a Slack incoming webhook URL")
	}
	for i, destination := range urls {
		if err := validateWebhookURL(destination); err != nil {
			return nil, fmt.Errorf("webhook-urls entry %d: %w", i+1, err)
		}
	}
	types, err := selectedTypes(plugin.SplitAndTrim(values.String("event-types")))
	if err != nil {
		return nil, err
	}
	return New(WithWebhookURLs(urls...), WithEventTypes(types), WithLogger(
		logging.GetLogger().With("plugin", "output.slack"),
	)), nil
}

func validateWebhookURL(destination string) error {
	if err := plugin.ValidateHTTPURL(destination); err != nil {
		return err
	}
	parsed, err := url.Parse(destination)
	if err != nil || parsed.Scheme != "https" || parsed.User != nil {
		return errors.New("must be an HTTPS URL")
	}
	return nil
}
