# Telegram output controls

The Telegram output sends selected Adder events to one configured chat. It also
answers commands in that chat. Configure a bot token and chat ID obtained through
Telegram, then choose a local settings file to allow notification preferences to
survive restarts:

```yaml
output: telegram
plugins:
  output:
    telegram:
      bot-token: "<Telegram bot token>"
      chat-id: "<Telegram chat ID>"
      settings-file: "/var/lib/adder/telegram-settings.yaml"
      operator-user-ids: "123456789,987654321" # group chats only
```

Use a separate settings file for each Adder instance and configured chat. The
process must be able to write its parent directory. Adder creates the directory
with private permissions and saves the YAML file atomically. The file records
the chat ID and the four preferences; a file belonging to another chat or a
malformed file prevents startup. A hand-written settings file must include
`chat_id` matching the configured chat ID. If `settings-file` is omitted, events
remain enabled and the bot can show settings, but in-chat changes are
unavailable.

| Command | Behavior |
| --- | --- |
| `/start` | Introduce the bot. |
| `/help` | List commands. |
| `/status` | Report whether the Adder pipeline is running. |
| `/settings` | Show the current notification preferences. |
| `/notify blocks on\|off` | Enable or disable block messages. |
| `/notify transactions on\|off` | Enable or disable transaction messages. |
| `/notify rollbacks on\|off` | Enable or disable rollback messages. |
| `/notify governance on\|off` | Enable or disable governance messages. |

All four notification types are enabled initially. Preferences affect Telegram
delivery only; Adder continues processing events. Replies and event messages go
only to the configured chat. In a private chat, only that chat's user can change
preferences. In a group or supergroup, only users listed in `operator-user-ids`
can change them; other group members can view status and settings. Without that
list, group preferences cannot be changed in chat. Channels can receive event
messages but cannot use these message commands.

The bot registers its command menu for the configured chat using Telegram's
[`setMyCommands` chat scope](https://core.telegram.org/bots/api#setmycommands).
It receives message updates through
[`getUpdates`](https://core.telegram.org/bots/api#getupdates) and replies with
[`sendMessage`](https://core.telegram.org/bots/api#sendmessage). In groups,
Telegram's [privacy mode](https://core.telegram.org/bots/features#privacy-mode)
may require addressing a command to the bot as `/status@YourBotName`. The bot
ignores commands addressed to another bot. A Telegram webhook configured for
the same bot prevents long polling; remove it before running this output.

For an end-to-end check without affecting a production bot, Telegram recommends
[using a separate test bot](https://core.telegram.org/bots/features#testing-your-bot).
Set that bot's token and a test chat ID, issue `/status` and `/settings`, change
one preference, and confirm the corresponding event message is suppressed.
