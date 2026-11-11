# MQTT output

The `mqtt` output publishes selected Adder events as JSON to MQTT topics. It
uses the [Eclipse Paho Go client v0.23.0](https://pkg.go.dev/github.com/eclipse/paho.golang@v0.23.0)
with MQTT 5. The [OASIS MQTT 5.0 specification](https://docs.oasis-open.org/mqtt/mqtt/v5.0/os/mqtt-v5.0-os.html)
defines the publish packets and acknowledgement rules used by this output.

```yaml
output: mqtt
plugins:
  output:
    mqtt:
      broker: mqtt://127.0.0.1:1883
      client-id: adder-output
      topic-prefix: adder/events
      qos: 1
      event-types: blocks,rollbacks,governance
```

The default topics are `adder/events/block`, `adder/events/rollback`, and
`adder/events/governance`. `transactions`, `drep-registration`, `drep-update`,
and `drep-retirement` can also be selected. Each payload is the complete Adder
event JSON. The output does not set MQTT's RETAIN flag.

`broker` accepts `mqtt://` and `tls://` URLs. `username` and `password` are
optional; credentials require `tls://`. Give each running Adder
instance a distinct `client-id`, because an MQTT broker disconnects an existing
session when another client connects with the same identifier.

`qos` may be 0, 1, or 2. At QoS 1, a completed publish means the broker sent
PUBACK; at QoS 2, it means the broker completed the QoS 2 exchange. Neither
acknowledges delivery to subscribers. At QoS 0, completion does not include a
broker acknowledgement. Disconnected events and failed publishes are reported
through the output error channel and are not replayed from durable storage.
The output reconnects before the next event when its connection has dropped.
If an acknowledgement times out, delivery is uncertain and the output reports
the failure. The output does not use Paho's optional publish queue.
For QoS 2, a PUBCOMP `0x92` response is also reported as an uncertain outcome:
the protocol permits it during recovery but otherwise indicates a session
mismatch.

The topic prefix must contain nonempty topic levels and cannot contain MQTT
wildcards `+` or `#`. Event type is appended as the last topic level.
