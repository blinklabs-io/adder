package pipeline

import (
	"testing"
	"time"

	"github.com/blinklabs-io/adder/event"
)

func BenchmarkPipelineEventFlow(b *testing.B) {
	for _, tc := range []struct {
		name    string
		filters int
	}{
		{name: "direct"},
		{name: "one_filter", filters: 1},
		{name: "three_filters", filters: 3},
	} {
		b.Run(tc.name, func(b *testing.B) {
			p := New()
			in := &fakeInput{}
			out := newCollectOutput(1)
			p.AddInput(in)
			for range tc.filters {
				p.AddFilter(newPassFilter())
			}
			p.AddOutput(out)
			if err := p.Start(); err != nil {
				b.Fatal(err)
			}
			defer func() {
				if err := p.Stop(); err != nil {
					b.Error(err)
				}
			}()

			evt := event.Event{
				Type:      event.TypeBlock,
				Timestamp: time.Unix(1, 0),
			}
			b.ReportAllocs()
			b.ResetTimer()
			for range b.N {
				in.emit(evt)
				<-out.received
			}
			b.StopTimer()
		})
	}
}
