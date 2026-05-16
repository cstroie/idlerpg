# IdleRPG — Project Guide for Claude

## What This Is

A standalone IRC bot implementing the classic IdleRPG game, written in Go.
Players gain levels by idling in the channel. Activity (talking, nick changes,
parting, quitting, getting kicked) adds penalty time. See README.md for player
commands and game mechanics.

## Build & Run

```bash
go build
./idlerpg -server irc.libera.chat:6667 -nick idlerpgbot -channel "#idlerpg"
```

No test suite yet. Build with `go build ./...` to verify changes compile.

## Code Structure

| File | Purpose |
|------|---------|
| `main.go` | IRC wiring (fluffle/goirc), event dispatch, command routing, reconnect loop |
| `game.go` | All game logic: players, tick loop, events, battles, quests, persistence |
| `go.mod` / `go.sum` | Module: `github.com/cstroie/idlerpg`, requires `fluffle/goirc` |

## Key Design Points

- `Game.players` is a `map[string]*Player` keyed by **lowercase nick**.
- All map/player mutations are protected by `Game.mu` (`sync.Mutex`).
- The tick goroutine runs every second; `start()` closes the previous stop channel
  before spawning a new one (prevents goroutine leaks on reconnect).
- `say()` and other channel announcements must be called **outside** the mutex.
  Collect messages into a `[]string` inside the lock, then send after releasing.
- Players are identified by their full `nick!user@host` address (`Player.Addr`)
  to prevent impersonation via nick squatting.
- Passwords are SHA-256 hashed with a per-player 16-byte random salt (`PassSalt`).
- Data is persisted to JSON after every state change; all players start offline on load.

## Adding New Events

1. Add message template strings as package-level `var` slices near the top of `game.go`.
2. Implement the event as a method that takes `*Player` (or a slice) and returns `string`
   or `[]string`. It must be called **with `mu` held**; messages are returned, not sent.
3. Wire it into `tick()` under the appropriate probability check.
4. Keep rates consistent: individual per-player events use `mathrand.Intn(86400)`,
   server-wide events use larger denominators.

## IRC Event Handlers (main.go)

| IRC event | Game call | Penalty |
|-----------|-----------|---------|
| JOIN | `OnJoin` | none (auto-login) |
| PART | `OnPart` | p200 |
| QUIT | `OnQuit` | p20 |
| NICK | `OnNick` | p30 |
| KICK | `OnKick` | p50 |
| PRIVMSG | `OnPrivmsg` | 1s/char (channel only, non-command) |

## Random Number Usage

`math/rand` is aliased as `mathrand` to avoid collision with `crypto/rand`
(used only for salt generation). Use `mathrand` for all game randomness.

## Research

See `RESEARCH.md` for detailed notes on the original IdleRPG mechanics and
what other implementations have done. See `TODO.md` for planned features.
