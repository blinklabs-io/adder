// Copyright 2026 Blink Labs Software
// SPDX-License-Identifier: Apache-2.0

package managed

import (
	"fmt"
	"net/http"
)

func (r *Router) metrics(w http.ResponseWriter, _ *http.Request) {
	c := r.counters.value()
	w.Header().Set("Content-Type", "text/plain; version=0.0.4")
	for _, entry := range []struct {
		name  string
		value uint64
	}{
		{"input_dropped", c.InputDropped},
		{"matched", c.Matched},
		{"destination_dropped", c.DestinationDropped},
		{"submitted_to_plugin", c.SubmittedToPlugin},
		{"plugin_errors", c.PluginErrors},
		{"unknown_outcomes", c.UnknownOutcomes},
		{"canceled", c.Canceled},
		{"diagnostic_dropped", c.DiagnosticDropped},
	} {
		_, _ = fmt.Fprintf(w, "# TYPE adder_managed_%s_total counter\nadder_managed_%s_total %d\n", entry.name, entry.name, entry.value)
	}
	_, _ = fmt.Fprintf(w, "# TYPE adder_managed_plugin_instances gauge\nadder_managed_plugin_instances %d\n", r.instances.Load())
}
