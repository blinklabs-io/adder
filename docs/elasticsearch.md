# Elasticsearch output

The `elasticsearch` output indexes selected Adder events into a versioned
index. It creates the index on startup with a strict mapping for
`schema_version`, `event_type`, and `event_time`. The full event is stored in
`event` as source JSON with indexing disabled, avoiding accidental mappings
for arbitrary payload fields.

```yaml
output: elasticsearch
plugins:
  output:
    elasticsearch:
      url: https://elastic.example.org:9200
      index: adder-events-v1
      api-key: BASE64_ENCODED_API_KEY
      event-types: blocks,transactions,rollbacks,governance
```

The document ID is SHA-256 of the complete event JSON. A repeat of the same
JSON overwrites the same document. If the timestamp or payload differs, it is
a different document. Rollbacks are separate `input.rollback` documents;
previous indexed events are not deleted or rewritten. Search clients must
apply rollback events when deriving active chain state. Elasticsearch search
results do not provide insertion order, so this output alone is not suitable
for ordered chain replay.

There is no batch buffer or flush timer: the output waits for each document
response before taking the next event. The shared input channel buffers ten
events. An index request has a ten-second deadline and retries transport
errors and HTTP 429, 502, 503, or 504 up to four attempts with short backoff.
The deterministic document ID makes a retry safe from extra copies; a timeout
can still leave the outcome uncertain. Failures are logged and sent to the
error channel when its 16-entry buffer has space. Queued events are discarded
on stop. For an incompatible schema, create a new `-vN` index and update the
configuration; the plugin does not migrate existing indices.

The API key requires HTTPS and privileges to create the index and index
documents. See the current [Create index API](https://www.elastic.co/docs/api/doc/elasticsearch/operation/operation-indices-create),
[Index document API](https://www.elastic.co/docs/api/doc/elasticsearch/operation/operation-index),
and [indexing guidance for HTTP 429](https://www.elastic.co/docs/deploy-manage/production-guidance/optimize-performance/indexing-speed).
