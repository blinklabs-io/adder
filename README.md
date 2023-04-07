# snek

![Snek Logo](./imgs/snek_logo_pink.png)

Snek is a tool for tailing the Cardano blockchain and emitting events for each
block and transaction that it sees.

## Example usage

```bash
export CARDANO_NETWORK=preview \
  CARDANO_NODE_SOCKET_TCP_HOST=preview-node.world.dev.cardano.org \
  CARDANO_NODE_SOCKET_TCP_PORT=30002 \
  CARDANO_NODE_USE_NTN=true
./snek 
```
