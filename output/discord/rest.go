// Copyright 2026 Blink Labs Software
// SPDX-License-Identifier: Apache-2.0

package discord

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

func (d *DiscordOutput) request(ctx context.Context, method, path string, body []byte, authenticated bool) ([]byte, error) {
	localCooldown := time.Time{}
	for attempt := 0; attempt < 4; attempt++ {
		d.mu.Lock()
		deadline := localCooldown
		if authenticated {
			deadline = d.cooldown[path]
			if d.globalCooldown.After(deadline) {
				deadline = d.globalCooldown
			}
		}
		d.mu.Unlock()
		if delay := time.Until(deadline); delay > 0 {
			if delay > 10*time.Second {
				return nil, errors.New("discord route is cooling down")
			}
			if err := sleep(ctx, delay); err != nil {
				return nil, err
			}
		}
		req, err := http.NewRequestWithContext(ctx, method, strings.TrimRight(d.apiBaseURL, "/")+path, bytes.NewReader(body))
		if err != nil {
			return nil, errors.New("invalid Discord API endpoint")
		}
		if body != nil {
			req.Header.Set("Content-Type", "application/json")
		}
		if authenticated {
			req.Header.Set("Authorization", "Bot "+d.token)
		}
		resp, err := d.client.Do(req)
		if err != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			var urlErr *url.Error
			if errors.As(err, &urlErr) {
				err = urlErr.Err
			}
			if method != http.MethodGet || attempt == 3 {
				return nil, fmt.Errorf("discord request failed: %w", err)
			}
			if err := sleep(ctx, time.Duration(1<<attempt)*time.Second); err != nil {
				return nil, err
			}
			continue
		}
		data, readErr := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		resp.Body.Close()
		if readErr != nil {
			return nil, readErr
		}
		if resp.StatusCode == http.StatusTooManyRequests {
			delay, ok := retryDelay(resp.Header.Get("Retry-After"), data)
			if !ok {
				return nil, errors.New("discord rate limit lacks a valid retry delay")
			}
			until := time.Now().Add(delay)
			var limit struct {
				Global bool `json:"global"`
			}
			_ = json.Unmarshal(data, &limit)
			if authenticated {
				d.mu.Lock()
				if limit.Global {
					d.globalCooldown = until
				} else {
					d.cooldown[path] = until
				}
				d.mu.Unlock()
			} else {
				localCooldown = until
			}
			if attempt == 3 {
				return nil, errors.New("discord rate limit retry budget exhausted")
			}
			continue
		}
		if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			if authenticated && resp.Header.Get("X-RateLimit-Remaining") == "0" {
				if seconds, err := strconv.ParseFloat(resp.Header.Get("X-RateLimit-Reset-After"), 64); err == nil && seconds >= 0 && seconds <= 3600 {
					d.mu.Lock()
					d.cooldown[path] = time.Now().Add(time.Duration(seconds * float64(time.Second)))
					d.mu.Unlock()
				}
			}
			return data, nil
		}
		if resp.StatusCode >= 500 && method == http.MethodGet && attempt < 3 {
			if err := sleep(ctx, time.Duration(1<<attempt)*time.Second); err != nil {
				return nil, err
			}
			continue
		}
		return nil, fmt.Errorf("discord API returned HTTP %d", resp.StatusCode)
	}
	return nil, errors.New("discord retry budget exhausted")
}

func retryDelay(header string, data []byte) (time.Duration, bool) {
	seconds, err := strconv.ParseFloat(header, 64)
	if err != nil {
		var value struct {
			RetryAfter *float64 `json:"retry_after"`
		}
		if json.Unmarshal(data, &value) != nil || value.RetryAfter == nil {
			return 0, false
		}
		seconds = *value.RetryAfter
	}
	if !(seconds >= 0 && seconds <= 3600) {
		return 0, false
	}
	return time.Duration(seconds * float64(time.Second)), true
}

func sleep(ctx context.Context, duration time.Duration) error {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
