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
