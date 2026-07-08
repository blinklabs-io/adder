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

//go:build freebsd

package setup

import "errors"

var errFreeBSDServiceUnsupported = errors.New("FreeBSD service management is not implemented")

func renderUnit(cfg ServiceConfig) ([]byte, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return []byte{}, nil
}

func registerService(ServiceConfig) error {
	return errFreeBSDServiceUnsupported
}

func unregisterService() error {
	return errFreeBSDServiceUnsupported
}

func serviceStatusCheck() (ServiceStatus, error) {
	return ServiceNotRegistered, nil
}

func startService() error {
	return errFreeBSDServiceUnsupported
}

func stopService() error {
	return errFreeBSDServiceUnsupported
}
