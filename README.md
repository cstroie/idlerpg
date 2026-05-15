# IdleRPG IRC Bot

A standalone IRC bot implementing the classic [IdleRPG](https://idlerpg.net/) game, written in Go.

Players register a character and gain levels simply by idling in the channel. Talking, changing nick, parting, or quitting adds penalty time. Characters battle each other on level-up, find items, and experience random fortune and misfortune.

## Usage

```bash
go build
./idlerpg -server irc.libera.chat:6667 -nick idlerpgbot -channel "#idlerpg"
```

All flags:

| Flag | Default | Description |
|------|---------|-------------|
| `-server` | `irc.libera.chat:6667` | IRC server `host:port` |
| `-nick` | `idlerpgbot` | Bot nick |
| `-password` | _(none)_ | Server password |
| `-ssl` | `false` | Use SSL |
| `-channel` | `#idlerpg` | Game channel |
| `-data` | `idlerpg.json` | Player data file (JSON, created automatically) |

## Player commands

| Command | Description |
|---------|-------------|
| `!register <nick> <class> <pass>` | Create a character. Class can be multiple words; password is always the last argument. |
| `!login <pass>` | Log in manually (auto-login happens on channel join). |
| `!logout` | Go offline. |
| `!status [nick]` | Show level, time to next level, and item total for yourself or another player. |
| `!whoami` | Shortcut for your own status. |
| `!top` | Top 5 players by level. |
| `!help` | Show command summary in channel. |

## Game mechanics

### Levelling

Players level up passively. The time required for level N is:

```
600 × 1.16^N  seconds
```

Level 0 → 1 takes 10 minutes; level 20 takes ~2.5 hours; level 40 takes ~26 hours.

### Penalties

Any activity adds time to your next level. Penalty formula: `base × 1.14^level` seconds.

| Event | Base penalty |
|-------|-------------|
| Talking in channel | 1 second per character typed |
| Nick change | 30 s |
| Quit IRC | 20 s |
| Part channel | 200 s |

### Items

Each level-up grants a random item (one of: ring, amulet, charm, weapon, helm, tunic, gloves, leggings, shield, boots) with a level between 1 and `1.5 × player level`. The item is equipped if it beats the current one in that slot.

### Battles

On every level-up the player challenges a random online opponent. Each side rolls `rand(0, sum_of_item_levels)`. The higher roll wins. The winner's TTL shrinks by `max(loser_level / 4, 7)%`; the loser's TTL grows by the same amount.

### Random events

Roughly once per day per online player, a calamity or godsend fires:

- **Calamity** — TTL increases by 5–12%.
- **Godsend** — TTL decreases by 5–12%.

### Persistence

Player data is saved to a JSON file after every state change and loaded on startup. All players start offline after a restart and are re-logged in automatically when they rejoin the channel.

## License

MIT
