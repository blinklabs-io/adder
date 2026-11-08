package pipeline

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/blinklabs-io/adder/event"
	"github.com/blinklabs-io/adder/plugin"
)

type bufferedBenchmarkInput struct {
	fakeInput
	buffer int
}

func (in *bufferedBenchmarkInput) StartContext(ctx context.Context) error {
	return in.StartRun(
		ctx,
		plugin.BaseConfig{HasOutput: true, OutputBuffer: in.buffer},
		func(context.Context) error { return nil },
		plugin.ShutdownHooks{},
	)
}

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

func BenchmarkPipelineStreaming(b *testing.B) {
	const batchSize = 4096
	for _, buffer := range []int{10, 256, 2048} {
		for _, slow := range []bool{false, true} {
			name := fmt.Sprintf("buffer_%d/fast_output", buffer)
			if slow {
				name = fmt.Sprintf("buffer_%d/periodic_output_pause", buffer)
			}
			b.Run(name, func(b *testing.B) {
				p := New()
				in := &bufferedBenchmarkInput{buffer: buffer}
				out := newCollectOutput(0)
				p.AddInput(in)
				p.AddOutput(out)
				if err := p.Start(); err != nil {
					b.Fatal(err)
				}
				defer func() {
					if err := p.Stop(); err != nil {
						b.Error(err)
					}
				}()

				evt := event.Event{Type: event.TypeBlock, Timestamp: time.Unix(1, 0)}
				drained := make(chan struct{})
				go func() {
					defer close(drained)
					for i := range b.N * batchSize {
						<-out.received
						if slow && i%64 == 63 {
							time.Sleep(100 * time.Microsecond)
						}
					}
				}()

				b.ReportAllocs()
				b.ResetTimer()
				for range b.N {
					for range batchSize {
						in.emit(evt)
					}
				}
				<-drained
				b.StopTimer()
				b.ReportMetric(float64(b.N*batchSize)/b.Elapsed().Seconds(), "events/s")
			})
		}
	}
}
