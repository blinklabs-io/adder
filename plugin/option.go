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

package plugin

import (
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"

	"github.com/spf13/pflag"
)

type PluginOptionType int

const (
	PluginOptionTypeString PluginOptionType = 1
	PluginOptionTypeBool   PluginOptionType = 2
	PluginOptionTypeInt    PluginOptionType = 3
	PluginOptionTypeUint   PluginOptionType = 4
)

// PluginOption describes a scalar option. Definitions contain no instance state.
type PluginOption struct {
	DefaultValue any
	Name         string
	CustomEnvVar string
	CustomFlag   string
	Description  string
	Type         PluginOptionType
}

// Options contains validated, immutable scalar values for one factory invocation.
// Accessors require a name and type declared by the factory's PluginEntry.
// A mismatched accessor is a programming error and panics.
type Options struct{ values map[string]any }

// String returns a declared string option.
func (o Options) String(name string) string { return o.values[name].(string) }

// Bool returns a declared boolean option.
func (o Options) Bool(name string) bool { return o.values[name].(bool) }

// Int returns a declared signed 32-bit option as an int.
func (o Options) Int(name string) int { return o.values[name].(int) }

// Uint returns a declared unsigned 32-bit option as a uint.
func (o Options) Uint(name string) uint { return o.values[name].(uint) }

// SplitAndTrim splits comma-separated values, discarding whitespace and empty entries.
func SplitAndTrim(val string) []string {
	var out []string
	for item := range strings.SplitSeq(val, ",") {
		if item = strings.TrimSpace(item); item != "" {
			out = append(out, item)
		}
	}
	return out
}

func (p PluginOption) flagName(kind, name string) string {
	if p.CustomFlag != "" {
		return kind + "-" + p.CustomFlag
	}
	return kind + "-" + name + "-" + p.Name
}

// AddToFlagSet registers independent flag storage for this definition.
func (p PluginOption) AddToFlagSet(fs *pflag.FlagSet, kind, name string) error {
	value, err := p.normalize(p.DefaultValue)
	if err != nil {
		return err
	}
	flag := p.flagName(kind, name)
	if fs.Lookup(flag) != nil {
		return fmt.Errorf("duplicate flag %q", flag)
	}
	switch p.Type {
	case PluginOptionTypeString:
		fs.String(flag, value.(string), p.Description)
	case PluginOptionTypeBool:
		fs.Bool(flag, value.(bool), p.Description)
	case PluginOptionTypeInt:
		fs.Int(flag, value.(int), p.Description)
	case PluginOptionTypeUint:
		fs.Uint(flag, value.(uint), p.Description)
	}
	return nil
}

func (p PluginOption) parse(value string) (any, error) {
	switch p.Type {
	case PluginOptionTypeString:
		return value, nil
	case PluginOptionTypeBool:
		v, err := strconv.ParseBool(value)
		if err == nil {
			return v, nil
		}
	case PluginOptionTypeInt:
		v, err := strconv.ParseInt(value, 10, 32)
		if err == nil {
			return int(v), nil
		}
	case PluginOptionTypeUint:
		v, err := strconv.ParseUint(value, 10, 32)
		if err == nil {
			return uint(v), nil
		}
	}
	return nil, fmt.Errorf(
		"invalid value for option %q (type %d)",
		p.Name,
		p.Type,
	)
}

func (p PluginOption) normalize(value any) (any, error) {
	switch p.Type {
	case PluginOptionTypeString:
		if v, ok := value.(string); ok {
			return v, nil
		}
	case PluginOptionTypeBool:
		if v, ok := value.(bool); ok {
			return v, nil
		}
	case PluginOptionTypeInt:
		if v, ok := value.(int); ok {
			return p.parse(strconv.Itoa(v))
		}
	case PluginOptionTypeUint:
		switch v := value.(type) {
		case int:
			return p.parse(strconv.Itoa(v))
		case uint:
			return p.parse(strconv.FormatUint(uint64(v), 10))
		case uint64:
			return p.parse(strconv.FormatUint(v, 10))
		}
	}
	return nil, fmt.Errorf(
		"invalid value for option %q (type %d)",
		p.Name,
		p.Type,
	)
}

// ValidateHTTPURL checks configured HTTP endpoints without exposing credentials in errors.
func ValidateHTTPURL(value string) error {
	u, err := url.Parse(value)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") ||
		u.Hostname() == "" {
		return errors.New("expected an absolute http or https URL")
	}
	return nil
}
