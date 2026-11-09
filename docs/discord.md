# Discord bot output

The `discord` output posts selected events to one configured Discord server and
channel. It also registers two server slash commands:

- `/adder-status`: confirms that this Adder output is running.
- `/adder-recent`: shows up to five events delivered since this process started.

The implementation targets Discord's latest publicly listed REST and Gateway
version, **v10**, as shown in the official
[API version table](https://docs.discord.com/developers/reference#api-versioning).
It uses the documented [Gateway connection flow](https://docs.discord.com/developers/events/gateway),
[guild command registration](https://docs.discord.com/developers/interactions/application-commands),
[message creation](https://docs.discord.com/developers/resources/message), and
[interaction callbacks](https://docs.discord.com/developers/interactions/receiving-and-responding).

Both responses are ephemeral. Commands received outside the configured server
are ignored; commands in another channel of the configured server get an
ephemeral channel restriction. The recent list is in memory and clears on
restart. This output has no durable queue.

```yaml
output: discord
plugins:
  output:
    discord:
      application-id: "123456789012345678"
      guild-id: "234567890123456789"
      channel-id: "345678901234567890"
      event-types: "blocks,rollbacks,governance"
```

All three IDs and the bot token are required. Set the token with the
`OUTPUT_DISCORD_BOT_TOKEN` environment variable, preferably from a secret
store, or with `bot-token` in YAML. `event-types` accepts one or more of
`blocks`, `transactions`, `rollbacks`, and `governance`; blocks, rollbacks, and
governance are enabled by default. Transactions can be high volume, so select
them only when the destination can handle the traffic. The bot reuses the
existing Discord webhook event layout for messages. It suppresses mentions in
posted events and command replies. Delivery
errors appear on the output error channel and in logs without the token.

Create a Discord application and bot, then install it in the target server with
`bot` and `applications.commands` scopes. Grant the bot View Channel, Send
Messages, and Embed Links in the target channel. The integration uses slash
command interactions over Gateway and requests zero Gateway intents, so it
does not need privileged Message Content or member intents. It registers its
two commands in the configured server at startup. These steps follow Discord's
[application command documentation](https://docs.discord.com/developers/interactions/application-commands)
and [Gateway documentation](https://docs.discord.com/developers/events/gateway).

The bot sends heartbeats, resumes a Gateway session when possible, and
reconnects with bounded backoff. HTTP requests honor Discord's rate-limit
headers and retry rejected 429 requests up to three times. GET requests also
retry temporary server and transport failures. POST requests are not replayed
after ambiguous failures, which could otherwise duplicate messages.
A `Retry-After` cooldown longer than ten seconds causes the current delivery to
fail immediately. Later events for that route are also rejected while more than
ten seconds remain, then wait out the rest of the cooldown.
A delivery failure is reported; an undelivered event is not retained for a
later restart. See Discord's [rate limit documentation](https://docs.discord.com/developers/topics/rate-limits).

For a live check, use a separate test server and bot. Confirm a selected event
appears in the configured channel, `/adder-status` and `/adder-recent` return
ephemeral replies there, and a command in another channel gets the restriction.
Local tests use a fake Discord API and Gateway to check the documented wire
messages without live credentials.
