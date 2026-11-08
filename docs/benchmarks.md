# Benchmarks

Run the event flow, Cardano filter, and JSON serialization benchmarks from the
repository root:

```sh
go test ./pipeline ./filter/cardano ./event -run '^$' -bench 'Benchmark(PipelineEventFlow|CardanoFilter|EventJSON)' -benchmem -benchtime=1s -count=5
```

`BenchmarkPipelineEventFlow` measures one event at a time through the running
pipeline with zero, one, or three pass-through filters and one output. The
producer waits for each event to reach the output. Plugin startup and shutdown
are outside the timed section. This models a single producer without downstream
backpressure, rather than peak throughput with concurrent inputs.

`BenchmarkCardanoFilter` measures configured address and asset filters on a
transaction with one output and one resolved input, and a pool filter on a block.
Each filter has matching and nonmatching cases. Configuration and fixture
construction are outside the timed section; the benchmark invokes the same
filtering function used by the plugin's event loop.

`BenchmarkEventJSON` measures marshaling and unmarshaling a transaction event
with a transaction context, block hash, fee, withdrawal, and CBOR bytes. It does
not include a full decoded ledger transaction. Unmarshaling into `Event` leaves
the `any` fields as generic JSON values, as it does in a normal round trip.

Compare runs on the same machine with the same Go version and workload. The
reported allocations are per event or filter call; timings alone do not show
where an operator workload spends its time.

## Streaming and backpressure

The ChainSync input has a 2,048 event output buffer; ordinary plugin channels
default to 10 slots. Pipeline forwarding channels are unbuffered. Compare
synthetic input buffers of 10, 256, and 2,048 slots with the same pipeline and
output:

```sh
for order in 0 1 2 2 1 0; do
  GOMAXPROCS=2 ADDER_BENCH_ORDER="$order" GOWORK=off go test ./pipeline -run '^$' -bench '^BenchmarkPipelineStreaming$' -benchmem -benchtime=200ms -count=1 -v
done
```

Each operation sends and drains 4,096 block event envelopes. `fast_output`
drains continuously. `periodic_output_pause` sleeps for 100 microseconds after
every 64 events to model intermittent sink backpressure. The benchmark reports
events/s as well as time and allocations per 4,096-event operation. Startup is
outside the timed section. No Cardano node, decoding, filters, serialization,
network output, or payload allocations are included. The pause is a synthetic
schedule, not a measured sink latency. The six separate runs balance the three
buffer orders; `-v` prints each order with the raw timings. Compare the same
named case across runs; faster producer completion alone does not mean faster
end-to-end delivery.
Because channel creation is outside the timer, B/op does not include the
buffer's storage.

One local run on Apple M5 with Go 1.27.3, `GOMAXPROCS=2`, and other work on the
machine gave these medians from six repetitions:

| Input buffer | Fast output events/s | Periodic pause events/s | Event slot storage |
| ---: | ---: | ---: | ---: |
| 10 | 1,050,530 | 352,140 | 960 B |
| 256 | 1,080,099 | 354,043 | 24 KiB |
| 2,048 | 1,048,588 | 353,900 | 192 KiB |

The slot storage column is capacity multiplied by the 96-byte `event.Event`
value on this arm64 build. It excludes channel bookkeeping and memory retained
by event payloads. Actual occupied and retained memory depends on queue depth
and payload size. The small throughput differences here do not establish a
benefit from the larger buffer.

Larger buffers can absorb a burst but retain more queued events and their
referenced payloads while a sink falls behind. A buffer does not increase a
persistently slow sink's capacity. The buffer cases isolate this tradeoff;
they do not establish an optimal ChainSync setting for a particular node,
network, event mix, or output. Keep the runtime defaults until measurements
from the intended workload justify a change.

## CPU, memory, and execution trace profiles

Capture profiles from a bounded local benchmark. Use a private temporary
directory because profiles can contain application data and paths:

```sh
profile_dir=$(mktemp -d)
GOWORK=off go test ./pipeline -run '^$' -bench '^BenchmarkPipelineStreaming/buffer_2048/fast_output$' -benchtime=1s -count=1 -cpuprofile="$profile_dir/cpu.out"
go tool pprof -top "$profile_dir/cpu.out"
GOWORK=off go test ./pipeline -run '^$' -bench '^BenchmarkPipelineStreaming/buffer_2048/fast_output$' -benchtime=200ms -count=1 -memprofile="$profile_dir/mem.out"
go tool pprof -alloc_space -top "$profile_dir/mem.out"
GOWORK=off go test ./pipeline -run '^$' -bench '^BenchmarkPipelineStreaming/buffer_2048/fast_output$' -benchtime=200ms -count=1 -trace="$profile_dir/trace.out"
go tool trace -pprof=sched "$profile_dir/trace.out" > "$profile_dir/sched.pprof"
go tool pprof -top "$profile_dir/sched.pprof"
```

The CPU profile samples active CPU work. `alloc_space` reports cumulative
allocations, not retained heap; use `-inuse_space` to inspect retained heap at
the capture point. The memory profile also includes process startup and
benchmark setup. The scheduler profile highlights runnable delays. Profiles
from a short run may have too few samples; increase `-benchtime` only when the
machine has capacity. Capture each profile separately so one profiler does not
distort another. This synthetic channel workload can be dominated by Go runtime
scheduling and does not identify a bottleneck in a real Adder deployment. For
comparisons, keep the Go version, machine, workload, and
benchmark duration fixed, collect multiple unprofiled runs, and compare both
events/s and B/op. Profile instrumentation changes timings, so do not use
profiled runs as the throughput baseline.

Adder's debug listener is disabled by default and does not register HTTP
pprof handlers. These commands use Go's test profiler and open no network
listener. Do not expose a profiling listener on a public interface.
