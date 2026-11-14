// Copyright 2026 Blink Labs Software
// SPDX-License-Identifier: Apache-2.0

package managed

import "errors"

type credentialRevision struct {
	version uint64
	digest  [32]byte
}

type customerRevision struct {
	revision     uint64
	digest       [32]byte
	destinations map[string]map[string]credentialRevision
}

type revisionState struct {
	history       map[string]customerRevision
	historyWeight int64
	allCutover    uint64
}

// Keep only the latest revision per identity, including removed identities, so
// deleting and re-adding a route cannot reset its revision or credential guards.
// Identity counts are bounded for the whole run, not just the active roster.
func advanceHistory(previous map[string]customerRevision, prepared map[string]*preparedCustomer, options Options) (map[string]customerRevision, int64, error) {
	next := make(map[string]customerRevision, len(previous)+len(prepared))
	for id, old := range previous {
		next[id] = old
	}
	for id, p := range prepared {
		old, exists := previous[id]
		if exists && p.config.Revision < old.revision {
			return nil, 0, errors.New("stale customer revision")
		}
		if exists && p.config.Revision == old.revision && p.digest != old.digest {
			return nil, 0, errors.New("customer content changed without advancing revision")
		}
		updated := customerRevision{revision: p.config.Revision, digest: p.digest, destinations: map[string]map[string]credentialRevision{}}
		for destID, credentials := range old.destinations {
			updated.destinations[destID] = make(map[string]credentialRevision, len(credentials))
			for key, value := range credentials {
				updated.destinations[destID][key] = value
			}
		}
		for destID, spec := range p.specs {
			bindings := updated.destinations[destID]
			if bindings == nil {
				bindings = map[string]credentialRevision{}
				updated.destinations[destID] = bindings
			}
			for key, ref := range spec.config.Credentials {
				latest, seen := bindings[key]
				digest := p.credentials[ref]
				if seen && ref.Version < latest.version {
					return nil, 0, errors.New("stale credential version")
				}
				if seen && ref.Version == latest.version && digest != latest.digest {
					return nil, 0, errors.New("credential version content changed")
				}
				bindings[key] = credentialRevision{version: ref.Version, digest: digest}
			}
		}
		if len(updated.destinations) > options.MaxDestinations {
			return nil, 0, errors.New("run destination identity budget exceeded")
		}
		next[id] = updated
	}
	if len(next) > options.MaxCustomers {
		return nil, 0, errors.New("run customer identity budget exceeded")
	}
	weight := int64(len(next)) * 512
	for _, customer := range next {
		weight += int64(len(customer.destinations)) * 512
		for _, bindings := range customer.destinations {
			weight += int64(len(bindings)) * 512
		}
	}
	return next, weight, nil
}
