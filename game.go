package main

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"math"
	mathrand "math/rand"
	"os"
	"sort"
	"strings"
	"sync"
	"time"
)

var itemSlots = [10]string{
	"ring", "amulet", "charm", "weapon", "helm",
	"tunic", "gloves", "leggings", "shield", "boots",
}

var calamityMsgs = []string{
	"%s is forsaken by their deity! TTL increased by %d%%.",
	"A wandering curse latches onto %s. TTL increased by %d%%.",
	"%s trips over a root and loses precious time. TTL increased by %d%%.",
	"Fate frowns upon %s. TTL increased by %d%%.",
	"A black cat crosses %s's path. TTL increased by %d%%.",
	"%s is visited by ill omens. TTL increased by %d%%.",
}

var godsendMsgs = []string{
	"The gods smile upon %s! TTL reduced by %d%%.",
	"A ray of divine light blesses %s! TTL reduced by %d%%.",
	"%s finds a shortcut on the road to glory! TTL reduced by %d%%.",
	"Fortune favours %s today! TTL reduced by %d%%.",
	"A celestial wind carries %s forward! TTL reduced by %d%%.",
	"%s receives a blessing from the heavens! TTL reduced by %d%%.",
}

var itemCalamityMsgs = []string{
	"%s's %s was damaged in an ambush! Item level reduced by %d%%.",
	"A thief nicks %s's %s in the marketplace! Item level reduced by %d%%.",
	"%s drops their %s down a well. Item level reduced by %d%%.",
	"Rust claims %s's %s. Item level reduced by %d%%.",
}

var itemGodsendMsgs = []string{
	"%s polishes their %s to a shine! Item level increased by %d%%.",
	"A wandering smith improves %s's %s! Item level increased by %d%%.",
	"Divine favour enchants %s's %s! Item level increased by %d%%.",
	"%s finds a rare component and upgrades their %s! Item level increased by %d%%.",
}

var handOfGodMsgs = [2][]string{
	{ // hurt
		"The hand of %s's god reaches down and sets them back %d%%!",
		"%s has displeased their deity — struck back %d%%!",
		"A divine rebuke sends %s stumbling backward %d%%!",
	},
	{ // help
		"The hand of %s's god reaches down and pushes them forward %d%%!",
		"%s basks in divine favour and surges ahead %d%%!",
		"A celestial nudge propels %s forward %d%%!",
	},
}

const (
	AlignEvil    int8 = -1
	AlignNeutral int8 = 0
	AlignGood    int8 = 1
)

var alignNames = map[int8]string{
	AlignEvil:    "evil",
	AlignNeutral: "neutral",
	AlignGood:    "good",
}

var goodEventMsgs = []string{
	"The light of %s's god shines upon %s and %s! Both surge ahead %d%%.",
	"%s and %s are united by divine favour! Both gain %d%%.",
	"The gods bless the fellowship of %s and %s! Both advance %d%%.",
}

var evilStealMsgs = []string{
	"%s lurks in the shadows and makes off with %s's %s (level %d)!",
	"%s bribes a corrupt merchant to acquire %s's %s (level %d)!",
	"Under cover of darkness, %s pilfers %s's %s (level %d)!",
}

var forsakenMsgs = []string{
	"%s is forsaken by their dark patron! TTL increased by %d%%.",
	"The shadows abandon %s. TTL increased by %d%%.",
	"%s's evil deeds catch up with them. TTL increased by %d%%.",
}

var questDescs = []string{
	"slay the dragon terrorising the village of Mal'Gorn",
	"recover the stolen Orb of Aldur from the goblin warrens",
	"escort the merchant caravan through the Darkwood",
	"retrieve the ancient tome from the sunken library",
	"defeat the lich haunting the catacombs beneath Castle Greystone",
	"find the missing children taken by the forest sprites",
	"seal the dimensional rift opening near the city of Varek",
	"break the curse on the village of Mirewood",
	"purge the corrupted well poisoning the town of Ashfen",
	"hunt down the bandit king who plagues the northern roads",
	"recover the holy relic stolen from the Temple of Aeon",
	"investigate the strange lights appearing over the Grimfen swamp",
}

const questMinLevel = 15
const questMinPlayers = 4
const gridSize = 500

type Quest struct {
	Questers []*Player
	EndsAt   time.Time
	Desc     string
	// Grid-based quest fields.
	IsGrid  bool
	QX, QY  int
	Reached map[string]bool // lowercase nicks that have reached the target
}

type Player struct {
	Nick      string
	Class     string
	Class2    string // second class chosen at level 12+, empty if not dual-classed
	PassSalt  string
	PassHash  string
	Alignment int8
	Level     int
	TTL       int64   // seconds until next level
	Items     [10]int    // item level per slot
	ItemNames [10]string // unique name for each slot, empty for normal items
	Online    bool
	Addr      string // nick!user@host when online
	X, Y      int    // position on the 500×500 grid (randomised on each login)
}

func (p *Player) itemSum() int {
	s := 0
	for _, v := range p.Items {
		s += v
	}
	return s
}

type Game struct {
	players    map[string]*Player // keyed by lowercase nick
	guilds     map[string]*Guild  // keyed by lowercase guild name
	mu         sync.Mutex
	dataFile   string
	guildsFile string
	say        func(string) // sends a message to the game channel
	setTopic   func(string) // sets the channel topic (wired after construction)
	lastEvent  string       // short description of the most recent notable event
	stopTick   chan struct{}
	quest      *Quest
	DevMode    bool         // speeds up TTL by 5× for development
}

func newGame(dataFile, guildsFile string, say func(string)) *Game {
	g := &Game{
		players:    make(map[string]*Player),
		guilds:     make(map[string]*Guild),
		dataFile:   dataFile,
		guildsFile: guildsFile,
		say:        say,
	}
	g.load()
	g.loadGuilds()
	return g
}

func (g *Game) start() {
	if g.stopTick != nil {
		close(g.stopTick)
	}
	g.stopTick = make(chan struct{})
	go g.tick(g.stopTick)
	g.updateTopic()
}

// OnJoin auto-logins a registered player when they join the channel.
func (g *Game) OnJoin(src string) {
	nick := extractNick(src)
	g.mu.Lock()
	p := g.players[strings.ToLower(nick)]
	if p != nil {
		p.Online = true
		p.Addr = src
		p.X = mathrand.Intn(gridSize)
		p.Y = mathrand.Intn(gridSize)
	}
	g.mu.Unlock()
	if p != nil {
		g.save()
		g.say(fmt.Sprintf("%s, the level %d %s, has joined IdleRPG at (%d,%d)! Next level in %s.",
			p.Nick, p.Level, p.Class, p.X, p.Y, fmtDuration(p.TTL)))
		g.noteEvent(fmt.Sprintf("%s (lvl %d) joined", p.Nick, p.Level))
	}
}

// OnPart marks player offline and applies penalty.
func (g *Game) OnPart(src string) {
	g.mu.Lock()
	p := g.findByAddr(src)
	if p != nil {
		g.applyPenalty(p, 200)
		p.Online = false
	}
	g.mu.Unlock()
	if p != nil {
		g.save()
		g.updateTopic()
	}
}

// OnQuit applies quit penalty (player stays registered but goes offline).
func (g *Game) OnQuit(src string) {
	nick := extractNick(src)
	g.mu.Lock()
	p := g.findByAddr(src)
	if p == nil {
		p = g.players[strings.ToLower(nick)]
		if p != nil && !p.Online {
			p = nil
		}
	}
	if p != nil {
		g.applyPenalty(p, 20)
		p.Online = false
	}
	g.mu.Unlock()
	if p != nil {
		g.save()
		g.updateTopic()
	}
}

// OnNick applies nick-change penalty and re-keys the player map and guild records.
func (g *Game) OnNick(src, newNick string) {
	oldNick := extractNick(src)
	oldKey := strings.ToLower(oldNick)
	newKey := strings.ToLower(newNick)
	g.mu.Lock()
	p := g.players[oldKey]
	if p != nil && p.Online {
		g.applyPenalty(p, 30)
		delete(g.players, oldKey)
		p.Nick = newNick
		p.Addr = strings.Replace(p.Addr, oldNick, newNick, 1)
		g.players[newKey] = p
		// Update guild membership and leadership.
		if guild := g.playerGuild(oldKey); guild != nil {
			for i, m := range guild.Members {
				if m == oldKey {
					guild.Members[i] = newKey
					break
				}
			}
			if guild.Leader == oldKey {
				guild.Leader = newKey
			}
		}
	} else {
		p = nil
	}
	g.mu.Unlock()
	if p != nil {
		g.save()
		g.saveGuilds()
	}
}

// OnKick applies a kick penalty and marks the player offline.
func (g *Game) OnKick(kickedNick string) {
	g.mu.Lock()
	p := g.players[strings.ToLower(kickedNick)]
	if p != nil && p.Online {
		g.applyPenalty(p, 50)
		p.Online = false
	} else {
		p = nil
	}
	g.mu.Unlock()
	if p != nil {
		g.save()
	}
}

// OnPrivmsg applies a talk penalty for registered online players.
func (g *Game) OnPrivmsg(src, text string) {
	g.mu.Lock()
	p := g.findByAddr(src)
	if p != nil {
		g.applyPenalty(p, int64(len(text)))
	}
	g.mu.Unlock()
	if p != nil {
		g.save()
	}
}

// CmdRegister creates a new character.
func (g *Game) CmdRegister(src, nick, class, pass string) string {
	key := strings.ToLower(nick)
	g.mu.Lock()
	_, exists := g.players[key]
	g.mu.Unlock()
	if exists {
		return fmt.Sprintf("Nick %s is already registered.", nick)
	}
	salt := newSalt()
	p := &Player{
		Nick:     nick,
		Class:    class,
		PassSalt: salt,
		PassHash: hashPass(salt, pass),
		Level:    0,
		TTL:      g.ttlForLevel(0),
	}
	g.mu.Lock()
	g.players[key] = p
	g.mu.Unlock()
	g.save()
	return fmt.Sprintf("%s, the %s, has registered for IdleRPG! Next level in %s.", nick, class, fmtDuration(p.TTL))
}

// CmdLogin logs in a player by matching their current IRC nick.
func (g *Game) CmdLogin(src, pass string) string {
	nick := extractNick(src)
	key := strings.ToLower(nick)
	g.mu.Lock()
	p, ok := g.players[key]
	g.mu.Unlock()
	if !ok {
		return "No character registered with that nick. Use !register <nick> <class> <pass> first."
	}
	if p.PassHash != hashPass(p.PassSalt, pass) {
		return "Wrong password."
	}
	g.mu.Lock()
	p.Online = true
	p.Addr = src
	g.mu.Unlock()
	g.save()
	return fmt.Sprintf("%s, the level %d %s, has logged in! Next level in %s.", nick, p.Level, p.Class, fmtDuration(p.TTL))
}

// CmdLogout logs out the calling player.
func (g *Game) CmdLogout(src string) string {
	nick := extractNick(src)
	g.mu.Lock()
	p := g.findByAddr(src)
	if p != nil {
		p.Online = false
	}
	g.mu.Unlock()
	if p == nil {
		return "You are not logged in."
	}
	g.save()
	return fmt.Sprintf("%s has logged out of IdleRPG.", nick)
}

// CmdAlign sets a player's alignment, applying a penalty if changing.
func (g *Game) CmdAlign(src, align string) string {
	var newAlign int8
	switch strings.ToLower(align) {
	case "good":
		newAlign = AlignGood
	case "evil":
		newAlign = AlignEvil
	case "neutral":
		newAlign = AlignNeutral
	default:
		return "Usage: !align <good|neutral|evil>"
	}
	g.mu.Lock()
	p := g.findByAddr(src)
	if p == nil {
		g.mu.Unlock()
		return "You are not logged in."
	}
	changed := p.Alignment != newAlign
	p.Alignment = newAlign
	if changed {
		g.applyPenalty(p, 75)
	}
	g.mu.Unlock()
	g.save()
	if changed {
		return fmt.Sprintf("%s is now %s. Changing alignment costs time — TTL adjusted.", p.Nick, alignNames[newAlign])
	}
	return fmt.Sprintf("%s is already %s.", p.Nick, alignNames[newAlign])
}

// CmdDualClass lets a player at level 12+ choose a permanent second class.
func (g *Game) CmdDualClass(src, class string) string {
	class = strings.TrimSpace(class)
	if class == "" {
		return "Usage: !dualclass <class>"
	}
	g.mu.Lock()
	p := g.findByAddr(src)
	if p == nil {
		g.mu.Unlock()
		return "You are not logged in."
	}
	if p.Level < 12 {
		g.mu.Unlock()
		return fmt.Sprintf("You must be at least level 12 to dual-class (you are level %d).", p.Level)
	}
	if p.Class2 != "" {
		g.mu.Unlock()
		return fmt.Sprintf("You are already dual-classed as %s/%s.", p.Class, p.Class2)
	}
	p.Class2 = class
	slot1 := classFocusSlot(p.Class)
	slot2 := classFocusSlot(p.Class2)
	nick := p.Nick
	g.mu.Unlock()
	g.save()
	if slot1 == slot2 {
		return fmt.Sprintf("%s is now a %s/%s! Both classes share the %s focus — that slot counts triple in battle.",
			nick, p.Class, class, itemSlots[slot1])
	}
	return fmt.Sprintf("%s is now a %s/%s! Primary focus: %s. Secondary focus: %s. Both count double in battle.",
		nick, p.Class, class, itemSlots[slot1], itemSlots[slot2])
}

// CmdStatus returns a player's status string.
func (g *Game) CmdStatus(src, targetNick string) string {
	if targetNick == "" {
		targetNick = extractNick(src)
	}
	g.mu.Lock()
	p, ok := g.players[strings.ToLower(targetNick)]
	g.mu.Unlock()
	if !ok {
		return fmt.Sprintf("No character found for %s.", targetNick)
	}
	status := "offline"
	if p.Online {
		status = "online"
	}
	questInfo := ""
	g.mu.Lock()
	if g.quest != nil {
		for _, qp := range g.quest.Questers {
			if qp == p {
				questInfo = fmt.Sprintf(" [on quest, ends in %s]", fmtDuration(int64(time.Until(g.quest.EndsAt).Seconds())))
				break
			}
		}
	}
	g.mu.Unlock()
	classDisplay := p.Class
	focusDisplay := itemSlots[classFocusSlot(p.Class)]
	if p.Class2 != "" {
		classDisplay = p.Class + "/" + p.Class2
		slot2 := itemSlots[classFocusSlot(p.Class2)]
		if slot2 == focusDisplay {
			focusDisplay += "×3"
		} else {
			focusDisplay += "+" + slot2
		}
	}
	return fmt.Sprintf("%s, the %s level %d %s [%s]%s — TTL: %s — Items: %d (focus: %s)",
		p.Nick, alignNames[p.Alignment], p.Level, classDisplay, status, questInfo,
		fmtDuration(p.TTL), p.itemSum(), focusDisplay)
}

// CmdPos returns the grid position of a player (self if no target given).
func (g *Game) CmdPos(src, targetNick string) string {
	if targetNick == "" {
		targetNick = extractNick(src)
	}
	g.mu.Lock()
	p, ok := g.players[strings.ToLower(targetNick)]
	if !ok {
		g.mu.Unlock()
		return fmt.Sprintf("No character found for %s.", targetNick)
	}
	if !p.Online {
		g.mu.Unlock()
		return fmt.Sprintf("%s is offline and has no position.", p.Nick)
	}
	x, y, nick := p.X, p.Y, p.Nick

	// Find other players sharing the same tile.
	var neighbours []string
	for _, op := range g.players {
		if op != p && op.Online && op.X == x && op.Y == y {
			neighbours = append(neighbours, op.Nick)
		}
	}

	// Check if on a quest destination.
	questNote := ""
	if g.quest != nil && g.quest.IsGrid && g.quest.QX == x && g.quest.QY == y {
		questNote = " [quest destination!]"
	}
	g.mu.Unlock()

	info := fmt.Sprintf("%s is at (%d,%d)%s on a %d×%d grid.", nick, x, y, questNote, gridSize, gridSize)
	if len(neighbours) > 0 {
		info += fmt.Sprintf(" Also here: %s.", strings.Join(neighbours, ", "))
	}
	return info
}

// CmdTop returns the top 5 players by level.
func (g *Game) CmdTop() string {
	g.mu.Lock()
	players := make([]*Player, 0, len(g.players))
	for _, p := range g.players {
		players = append(players, p)
	}
	g.mu.Unlock()

	sort.Slice(players, func(i, j int) bool {
		if players[i].Level != players[j].Level {
			return players[i].Level > players[j].Level
		}
		return players[i].TTL < players[j].TTL
	})

	n := 5
	if len(players) < n {
		n = len(players)
	}
	if n == 0 {
		return "No players yet."
	}
	parts := make([]string, n)
	for i := 0; i < n; i++ {
		p := players[i]
		parts[i] = fmt.Sprintf("%d. %s (lvl %d, items %d)", i+1, p.Nick, p.Level, p.itemSum())
	}
	return "Top players: " + strings.Join(parts, " | ")
}

// CmdQuest returns the status of the active quest, if any.
func (g *Game) CmdQuest() string {
	g.mu.Lock()
	q := g.quest
	g.mu.Unlock()

	if q == nil {
		return "No quest is currently active."
	}

	names := make([]string, len(q.Questers))
	for i, p := range q.Questers {
		names[i] = p.Nick
	}
	questers := strings.Join(names, ", ")

	if q.IsGrid {
		remaining := time.Until(q.EndsAt)
		reached := len(q.Reached)
		total := len(q.Questers)
		return fmt.Sprintf("Grid quest: %s must reach (%d,%d) to %s — %d/%d there, %s remaining.",
			questers, q.QX, q.QY, q.Desc, reached, total, fmtDuration(int64(remaining.Seconds())))
	}
	return fmt.Sprintf("Quest: %s are on a mission to %s — %s remaining.",
		questers, q.Desc, fmtDuration(int64(time.Until(q.EndsAt).Seconds())))
}

// CmdOnline lists all currently online players.
func (g *Game) CmdOnline() string {
	g.mu.Lock()
	var parts []string
	for _, p := range g.players {
		if p.Online {
			parts = append(parts, fmt.Sprintf("%s (lvl %d)", p.Nick, p.Level))
		}
	}
	g.mu.Unlock()

	if len(parts) == 0 {
		return "No players currently online."
	}
	sort.Strings(parts)
	return fmt.Sprintf("Online (%d): %s", len(parts), strings.Join(parts, ", "))
}

func (g *Game) tick(stop <-chan struct{}) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-stop:
			return
		case <-ticker.C:
		}

		g.mu.Lock()
		var levelUps []*Player
		var msgs []string

		online := g.onlinePlayers()

		for _, p := range online {
			p.TTL--
			if p.TTL <= 0 {
				levelUps = append(levelUps, p)
			} else {
				if mathrand.Intn(86400) == 0 {
					msgs = append(msgs, g.randomEvent(p))
				}
				// Bot battle: ~once per day per player.
				if mathrand.Intn(86400) == 0 {
					msgs = append(msgs, g.botBattle(p))
				}
				switch p.Alignment {
				case AlignGood:
					// ~once per 12 days per good player
					if mathrand.Intn(86400*12) == 0 {
						if m := g.goodAlignmentEvent(p, online); m != "" {
							msgs = append(msgs, m)
						}
					}
				case AlignEvil:
					// ~once per 8 days per evil player
					if mathrand.Intn(86400*8) == 0 {
						msgs = append(msgs, g.evilAlignmentEvent(p, online))
					}
				}
			}
		}

		// Move every online player one step in a random direction (toroidal wrap).
		posMap := make(map[[2]int][]*Player, len(online))
		for _, p := range online {
			p.X = (p.X + mathrand.Intn(3) - 1 + gridSize) % gridSize
			p.Y = (p.Y + mathrand.Intn(3) - 1 + gridSize) % gridSize
			key := [2]int{p.X, p.Y}
			posMap[key] = append(posMap[key], p)
		}

		// Location-based encounters: 1/len(online) chance per shared tile.
		var encounterPairs [][2]*Player
		if len(online) > 0 {
			for _, group := range posMap {
				if len(group) >= 2 && mathrand.Intn(len(online)) == 0 {
					mathrand.Shuffle(len(group), func(i, j int) { group[i], group[j] = group[j], group[i] })
					encounterPairs = append(encounterPairs, [2]*Player{group[0], group[1]})
					// One encounter per tick to avoid flooding.
					break
				}
			}
		}
		if len(encounterPairs) > 0 {
			ep := encounterPairs[0]
			msgs = append(msgs, fmt.Sprintf("%s and %s stumble into each other at (%d,%d)!",
				ep[0].Nick, ep[1].Nick, ep[0].X, ep[0].Y))
		}

		// Grid quest progress: check if questers have reached the target.
		if g.quest != nil && g.quest.IsGrid {
			for _, qp := range g.quest.Questers {
				nick := strings.ToLower(qp.Nick)
				if !g.quest.Reached[nick] && qp.X == g.quest.QX && qp.Y == g.quest.QY {
					g.quest.Reached[nick] = true
					msgs = append(msgs, fmt.Sprintf("%s has reached the quest destination (%d,%d)!",
						qp.Nick, g.quest.QX, g.quest.QY))
				}
			}
			allReached := len(g.quest.Reached) == len(g.quest.Questers)
			if allReached {
				msgs = append(msgs, g.resolveQuest(online)...)
				g.quest = nil
			}
		}

		// Hand of God: ~once per 20 days across the whole server.
		if len(online) > 0 && mathrand.Intn(86400*20) == 0 {
			msgs = append(msgs, g.handOfGod(online[mathrand.Intn(len(online))]))
		}

		// Team battle: ~4 times per day when at least 6 players are online.
		if len(online) >= 6 && mathrand.Intn(86400/4) == 0 {
			msgs = append(msgs, g.teamBattle(online)...)
		}

		// Guild battle: ~once per day when 2+ guilds have 2+ online members.
		if mathrand.Intn(86400) == 0 {
			msgs = append(msgs, g.guildBattle()...)
		}

		// Quest: ~once per day when conditions are met and no quest is active.
		if g.quest == nil && mathrand.Intn(86400) == 0 {
			msgs = append(msgs, g.tryStartQuest(online)...)
		}

		// Quest resolution.
		if g.quest != nil && time.Now().After(g.quest.EndsAt) {
			msgs = append(msgs, g.resolveQuest(online)...)
			g.quest = nil
		}

		topicWorthy := len(levelUps) > 0 || len(encounterPairs) > 0

		// Capture notable tick events for the topic before unlocking.
		var tickEvent string
		for _, m := range msgs {
			if strings.Contains(m, "Quest") || strings.Contains(m, "quest") ||
				strings.Contains(m, "Guild battle") || strings.Contains(m, "Team battle") ||
				strings.Contains(m, "hand of") || strings.Contains(m, "Hand of") ||
				strings.Contains(m, "god") || strings.Contains(m, "LEGENDARY") {
				tickEvent = m
				topicWorthy = true
				break
			}
		}
		if tickEvent != "" {
			// Trim to a topic-friendly length.
			if len(tickEvent) > 80 {
				tickEvent = tickEvent[:77] + "..."
			}
			g.lastEvent = tickEvent
		}

		g.mu.Unlock()

		for _, msg := range msgs {
			g.say(msg)
		}
		for _, ep := range encounterPairs {
			g.battle(ep[0], ep[1])
		}
		for _, p := range levelUps {
			g.doLevelUp(p)
		}
		if len(levelUps) > 0 {
			g.save()
		}
		if topicWorthy {
			g.updateTopic()
		}
	}
}

// onlinePlayers returns a slice of all online players. Must be called with mu held.
func (g *Game) onlinePlayers() []*Player {
	out := make([]*Player, 0, len(g.players))
	for _, p := range g.players {
		if p.Online {
			out = append(out, p)
		}
	}
	return out
}

func (g *Game) doLevelUp(p *Player) {
	g.mu.Lock()
	p.Level++
	p.TTL = g.ttlForLevel(p.Level)

	slot, itemLevel, itemName, itemRarity := rollItemDrop(p)
	improved := itemLevel > p.Items[slot]
	if improved {
		p.Items[slot] = itemLevel
		p.ItemNames[slot] = itemName
	}
	slotName := itemSlots[slot]
	nick := p.Nick
	level := p.Level
	ttl := p.TTL
	isum := p.itemSum()

	online := g.onlinePlayers()
	var opponents []*Player
	for _, op := range online {
		if strings.ToLower(op.Nick) != strings.ToLower(nick) {
			opponents = append(opponents, op)
		}
	}
	g.mu.Unlock()

	itemDesc := slotName
	if itemName != "" {
		itemDesc = fmt.Sprintf("%s (%s)", itemName, slotName)
	}
	equipped := ""
	if improved {
		equipped = " (equipped!)"
	}
	label := ""
	if itemRarity != rarityNormal {
		label = " " + rarityLabel(itemRarity)
	}
	g.say(fmt.Sprintf("%s has attained level %d! Next level in %s. They find a %s of level %d%s%s [item total: %d].",
		nick, level, fmtDuration(ttl), itemDesc, itemLevel, equipped, label, isum))

	switch itemRarity {
	case rarityLegendary:
		g.noteEvent(fmt.Sprintf("✦ %s found %s — LEGENDARY!", nick, itemName))
	case rarityRare:
		g.noteEvent(fmt.Sprintf("★ %s found %s (Rare) at lvl %d", nick, itemName, level))
	case rarityUncommon:
		g.noteEvent(fmt.Sprintf("%s reached lvl %d, found %s", nick, level, itemName))
	default:
		g.noteEvent(fmt.Sprintf("%s reached lvl %d", nick, level))
	}

	if len(opponents) > 0 {
		g.battle(p, opponents[mathrand.Intn(len(opponents))])
	}
}

func (g *Game) battle(a, b *Player) {
	g.mu.Lock()

	// Alignment modifies effective item sum: good +10%, evil -10%.
	alignBonus := func(p *Player, sum int) int {
		switch p.Alignment {
		case AlignGood:
			return sum + sum/10
		case AlignEvil:
			return sum - sum/10
		}
		return sum
	}

	aSum := alignBonus(a, effectiveItemSum(a))
	bSum := alignBonus(b, effectiveItemSum(b))
	if aSum < 1 {
		aSum = 1
	}
	if bSum < 1 {
		bSum = 1
	}

	aRoll := mathrand.Intn(aSum)
	bRoll := mathrand.Intn(bSum)

	winner, loser := a, b
	wRoll, lRoll := aRoll, bRoll
	if bRoll > aRoll {
		winner, loser = b, a
		wRoll, lRoll = bRoll, aRoll
	}

	// Critical hit: Good 1/50, Evil 1/20 — doubles the TTL swing.
	crit := false
	switch winner.Alignment {
	case AlignGood:
		crit = mathrand.Intn(50) == 0
	case AlignEvil:
		crit = mathrand.Intn(20) == 0
	}

	pct := int(math.Max(float64(loser.Level)/4.0, 7))
	if crit {
		pct *= 2
	}
	change := winner.TTL * int64(pct) / 100
	if change < 1 {
		change = 1
	}
	winner.TTL -= change
	if winner.TTL < 0 {
		winner.TTL = 0
	}
	loser.TTL += change

	wName, lName := winner.Nick, loser.Nick
	wSum, lSum := winner.itemSum(), loser.itemSum()

	stealMsg := g.tryStealItem(winner, loser)
	g.mu.Unlock()

	critNote := ""
	if crit {
		critNote = " Critical hit!"
	}
	g.say(fmt.Sprintf("%s [%d/%d] battles %s [%d/%d] and wins!%s TTL adjusted by %d%%.",
		wName, wRoll, wSum, lName, lRoll, lSum, critNote, pct))
	if stealMsg != "" {
		g.say(stealMsg)
	}
}

// botBattle pits a player against the bot. Bot item sum = 1 + highest player sum
// across all registered players. Win: 20% TTL reduction. Loss: 10% TTL increase.
// Must be called with mu held.
func (g *Game) botBattle(p *Player) string {
	botSum := 1
	for _, op := range g.players {
		if s := effectiveItemSum(op); s > botSum-1 {
			botSum = s + 1
		}
	}

	pSum := effectiveItemSum(p)
	if pSum < 1 {
		pSum = 1
	}

	pRoll := mathrand.Intn(pSum)
	botRoll := mathrand.Intn(botSum)

	if pRoll >= botRoll {
		change := p.TTL * 20 / 100
		if change < 1 {
			change = 1
		}
		p.TTL -= change
		if p.TTL < 1 {
			p.TTL = 1
		}
		return fmt.Sprintf("%s [%d/%d] challenges the bot [%d/%d] and wins! TTL reduced by 20%%.",
			p.Nick, pRoll, pSum, botRoll, botSum)
	}

	change := p.TTL * 10 / 100
	if change < 1 {
		change = 1
	}
	p.TTL += change
	return fmt.Sprintf("%s [%d/%d] challenges the bot [%d/%d] and loses! TTL increased by 10%%.",
		p.Nick, pRoll, pSum, botRoll, botSum)
}

// tryStealItem gives the winner a 3% chance to steal one item from the loser.
// Must be called with mu held.
func (g *Game) tryStealItem(winner, loser *Player) string {
	if mathrand.Intn(100) >= 3 {
		return ""
	}
	// Pick a random slot where the loser has something worth taking.
	candidates := make([]int, 0, 10)
	for i, v := range loser.Items {
		if v > 0 {
			candidates = append(candidates, i)
		}
	}
	if len(candidates) == 0 {
		return ""
	}
	slot := candidates[mathrand.Intn(len(candidates))]
	stolen := loser.Items[slot]
	stolenName := loser.ItemNames[slot]
	loser.Items[slot] = 0
	loser.ItemNames[slot] = ""
	itemDesc := itemSlots[slot]
	if stolenName != "" {
		itemDesc = stolenName + " (" + itemSlots[slot] + ")"
	}
	if stolen > winner.Items[slot] {
		winner.Items[slot] = stolen
		winner.ItemNames[slot] = stolenName
		return fmt.Sprintf("%s steals %s's %s (level %d) and equips it!",
			winner.Nick, loser.Nick, itemDesc, stolen)
	}
	return fmt.Sprintf("%s steals %s's %s (level %d) but it's worse than their own — discarded.",
		winner.Nick, loser.Nick, itemDesc, stolen)
}

// randomEvent fires a random individual event for one player. Must be called with mu held.
func (g *Game) randomEvent(p *Player) string {
	pct := mathrand.Intn(8) + 5
	change := p.TTL * int64(pct) / 100
	if change < 1 {
		change = 1
	}

	// Pick event type: 0=ttl calamity, 1=ttl godsend, 2=item calamity, 3=item godsend, 4=find item
	eventType := mathrand.Intn(5)

	switch eventType {
	case 0: // TTL calamity
		p.TTL += change
		tmpl := calamityMsgs[mathrand.Intn(len(calamityMsgs))]
		return fmt.Sprintf(tmpl, p.Nick, pct)

	case 1: // TTL godsend
		p.TTL -= change
		if p.TTL < 1 {
			p.TTL = 1
		}
		tmpl := godsendMsgs[mathrand.Intn(len(godsendMsgs))]
		return fmt.Sprintf(tmpl, p.Nick, pct)

	case 2: // Item calamity — degrade one non-zero item
		slot := g.pickNonZeroSlot(p)
		if slot < 0 {
			// Fall back to TTL calamity if no items.
			p.TTL += change
			return fmt.Sprintf(calamityMsgs[0], p.Nick, pct)
		}
		old := p.Items[slot]
		reduced := int(math.Max(float64(old)*float64(100-pct)/100, 1))
		p.Items[slot] = reduced
		tmpl := itemCalamityMsgs[mathrand.Intn(len(itemCalamityMsgs))]
		return fmt.Sprintf(tmpl, p.Nick, itemSlots[slot], pct)

	case 3: // Item godsend — improve one item
		slot := g.pickNonZeroSlot(p)
		if slot < 0 {
			slot = mathrand.Intn(10)
			p.Items[slot] = 1
		}
		old := p.Items[slot]
		p.Items[slot] = int(math.Max(float64(old)*float64(100+pct)/100, float64(old)+1))
		tmpl := itemGodsendMsgs[mathrand.Intn(len(itemGodsendMsgs))]
		return fmt.Sprintf(tmpl, p.Nick, itemSlots[slot], pct)

	default: // Find a random item on the ground
		slot := mathrand.Intn(10)
		maxItem := int(math.Max(float64(p.Level)*1.5, 1))
		found := mathrand.Intn(maxItem) + 1
		equipped := "but it's worse than their current one"
		if found > p.Items[slot] {
			p.Items[slot] = found
			equipped = "and equips it"
		}
		return fmt.Sprintf("%s stumbles upon a %s of level %d on the road %s! [item total: %d]",
			p.Nick, itemSlots[slot], found, equipped, p.itemSum())
	}
}

// pickNonZeroSlot returns a random item slot index that has a value > 0, or -1 if none.
// Must be called with mu held.
func (g *Game) pickNonZeroSlot(p *Player) int {
	candidates := make([]int, 0, 10)
	for i, v := range p.Items {
		if v > 0 {
			candidates = append(candidates, i)
		}
	}
	if len(candidates) == 0 {
		return -1
	}
	return candidates[mathrand.Intn(len(candidates))]
}

// handOfGod fires a dramatic divine event on a random online player. Must be called with mu held.
func (g *Game) handOfGod(p *Player) string {
	pct := mathrand.Intn(71) + 5 // 5–75%
	change := p.TTL * int64(pct) / 100
	if change < 1 {
		change = 1
	}
	if mathrand.Intn(5) == 0 { // 20% chance to hurt
		p.TTL += change
		tmpl := handOfGodMsgs[0][mathrand.Intn(len(handOfGodMsgs[0]))]
		return fmt.Sprintf(tmpl, p.Nick, pct)
	}
	p.TTL -= change
	if p.TTL < 1 {
		p.TTL = 1
	}
	tmpl := handOfGodMsgs[1][mathrand.Intn(len(handOfGodMsgs[1]))]
	return fmt.Sprintf(tmpl, p.Nick, pct)
}

// teamBattle selects two teams of 3 from online players and runs a group battle.
// Must be called with mu held.
func (g *Game) teamBattle(online []*Player) []string {
	// Shuffle and pick 6.
	shuffled := make([]*Player, len(online))
	copy(shuffled, online)
	mathrand.Shuffle(len(shuffled), func(i, j int) { shuffled[i], shuffled[j] = shuffled[j], shuffled[i] })
	teamA := shuffled[:3]
	teamB := shuffled[3:6]

	sumA, sumB := 0, 0
	for _, p := range teamA {
		sumA += effectiveItemSum(p)
	}
	for _, p := range teamB {
		sumB += effectiveItemSum(p)
	}
	if sumA < 1 {
		sumA = 1
	}
	if sumB < 1 {
		sumB = 1
	}

	rollA := mathrand.Intn(sumA)
	rollB := mathrand.Intn(sumB)

	winners, losers := teamA, teamB
	wRoll, lRoll, wSum, lSum := rollA, rollB, sumA, sumB
	if rollB > rollA {
		winners, losers = teamB, teamA
		wRoll, lRoll, wSum, lSum = rollB, rollA, sumB, sumA
	}

	// Find the lowest TTL on each team for scaling the change.
	minWinnerTTL := winners[0].TTL
	for _, p := range winners[1:] {
		if p.TTL < minWinnerTTL {
			minWinnerTTL = p.TTL
		}
	}
	change := minWinnerTTL * 20 / 100

	for _, p := range winners {
		p.TTL -= change
		if p.TTL < 0 {
			p.TTL = 0
		}
	}
	for _, p := range losers {
		p.TTL += change
	}

	names := func(team []*Player) string {
		ns := make([]string, len(team))
		for i, p := range team {
			ns[i] = p.Nick
		}
		return strings.Join(ns, ", ")
	}

	return []string{
		fmt.Sprintf("Team battle! [%s] (%d) vs [%s] (%d) — rolls %d vs %d.",
			names(winners), wSum, names(losers), lSum, wRoll, lRoll),
		fmt.Sprintf("[%s] win! Each winner's TTL drops by 20%% of their weakest member's TTL.", names(winners)),
	}
}

// tryStartQuest attempts to launch a quest when conditions are met. Must be called with mu held.
func (g *Game) tryStartQuest(online []*Player) []string {
	eligible := make([]*Player, 0)
	for _, p := range online {
		if p.Level >= questMinLevel {
			eligible = append(eligible, p)
		}
	}
	if len(eligible) < questMinPlayers {
		return nil
	}

	mathrand.Shuffle(len(eligible), func(i, j int) { eligible[i], eligible[j] = eligible[j], eligible[i] })
	questers := eligible[:questMinPlayers]

	desc := questDescs[mathrand.Intn(len(questDescs))]
	duration := time.Duration(mathrand.Intn(3)+1) * time.Hour // 1–3 hours

	names := make([]string, questMinPlayers)
	for i, p := range questers {
		names[i] = p.Nick
	}

	// 50% chance of a grid-based quest.
	if mathrand.Intn(2) == 0 {
		qx := mathrand.Intn(gridSize)
		qy := mathrand.Intn(gridSize)
		g.quest = &Quest{
			Questers: questers,
			EndsAt:   time.Now().Add(duration),
			Desc:     desc,
			IsGrid:   true,
			QX:       qx,
			QY:       qy,
			Reached:  make(map[string]bool),
		}
		return []string{
			fmt.Sprintf("Grid quest begun! %s must navigate to (%d,%d) to %s. They have %s.",
				strings.Join(names, ", "), qx, qy, desc, fmtDuration(int64(duration.Seconds()))),
		}
	}

	g.quest = &Quest{
		Questers: questers,
		EndsAt:   time.Now().Add(duration),
		Desc:     desc,
	}
	return []string{
		fmt.Sprintf("Quest begun! %s have been sent to %s. They must complete it within %s.",
			strings.Join(names, ", "), desc, fmtDuration(int64(duration.Seconds()))),
	}
}

// resolveQuest completes or fails the active quest. Must be called with mu held.
func (g *Game) resolveQuest(online []*Player) []string {
	quest := g.quest

	// Check all questers are still online.
	onlineSet := make(map[*Player]bool, len(online))
	for _, p := range online {
		onlineSet[p] = true
	}
	allOnline := true
	for _, qp := range quest.Questers {
		if !onlineSet[qp] {
			allOnline = false
			break
		}
	}

	names := make([]string, len(quest.Questers))
	for i, p := range quest.Questers {
		names[i] = p.Nick
	}

	if allOnline {
		// Success: all questers get 25% TTL reduction.
		for _, qp := range quest.Questers {
			change := qp.TTL * 25 / 100
			qp.TTL -= change
			if qp.TTL < 1 {
				qp.TTL = 1
			}
		}
		if quest.IsGrid {
			return []string{
				fmt.Sprintf("Grid quest complete! %s have all reached (%d,%d) and succeeded in their quest to %s! Each receives a 25%% TTL bonus.",
					strings.Join(names, ", "), quest.QX, quest.QY, quest.Desc),
			}
		}
		return []string{
			fmt.Sprintf("Quest complete! %s have succeeded in their quest to %s! Each receives a 25%% TTL bonus.",
				strings.Join(names, ", "), quest.Desc),
		}
	}

	// Failure: all online players are penalised p15.
	for _, p := range online {
		g.applyPenalty(p, 15)
	}
	if quest.IsGrid {
		reached := make([]string, 0, len(quest.Reached))
		for nick := range quest.Reached {
			reached = append(reached, nick)
		}
		suffix := "none reached the destination"
		if len(reached) > 0 {
			suffix = fmt.Sprintf("only %s reached (%d,%d)", strings.Join(reached, ", "), quest.QX, quest.QY)
		}
		return []string{
			fmt.Sprintf("Grid quest failed! %s did not all reach (%d,%d) to %s (%s). All online players suffer a penalty!",
				strings.Join(names, ", "), quest.QX, quest.QY, quest.Desc, suffix),
		}
	}
	return []string{
		fmt.Sprintf("Quest failed! %s did not complete their quest to %s in time. All online players suffer a penalty!",
			strings.Join(names, ", "), quest.Desc),
	}
}

// goodAlignmentEvent pairs two good players for a mutual TTL bonus. Must be called with mu held.
func (g *Game) goodAlignmentEvent(p *Player, online []*Player) string {
	var partners []*Player
	for _, op := range online {
		if op != p && op.Alignment == AlignGood {
			partners = append(partners, op)
		}
	}
	if len(partners) == 0 {
		return ""
	}
	partner := partners[mathrand.Intn(len(partners))]
	pct := mathrand.Intn(8) + 5
	for _, target := range []*Player{p, partner} {
		change := target.TTL * int64(pct) / 100
		if change < 1 {
			change = 1
		}
		target.TTL -= change
		if target.TTL < 1 {
			target.TTL = 1
		}
	}
	tmpl := goodEventMsgs[mathrand.Intn(len(goodEventMsgs))]
	return fmt.Sprintf(tmpl, p.Nick, partner.Nick, pct)
}

// evilAlignmentEvent either steals an item from a good player or gets forsaken. Must be called with mu held.
func (g *Game) evilAlignmentEvent(p *Player, online []*Player) string {
	var goodTargets []*Player
	for _, op := range online {
		if op != p && op.Alignment == AlignGood {
			goodTargets = append(goodTargets, op)
		}
	}

	// If there's a good player to steal from, 50/50 steal vs. forsaken.
	if len(goodTargets) > 0 && mathrand.Intn(2) == 0 {
		target := goodTargets[mathrand.Intn(len(goodTargets))]
		slot := g.pickNonZeroSlot(target)
		if slot >= 0 {
			stolen := target.Items[slot]
			target.Items[slot] = 0
			if stolen > p.Items[slot] {
				p.Items[slot] = stolen
			}
			tmpl := evilStealMsgs[mathrand.Intn(len(evilStealMsgs))]
			return fmt.Sprintf(tmpl, p.Nick, target.Nick, itemSlots[slot], stolen)
		}
	}

	// Forsaken: dark patron punishes the evil player.
	pct := mathrand.Intn(5) + 1
	change := p.TTL * int64(pct) / 100
	if change < 1 {
		change = 1
	}
	p.TTL += change
	tmpl := forsakenMsgs[mathrand.Intn(len(forsakenMsgs))]
	return fmt.Sprintf(tmpl, p.Nick, pct)
}

// applyPenalty adds base * 1.14^level seconds. Must be called with mu held.
func (g *Game) applyPenalty(p *Player, base int64) {
	p.TTL += int64(float64(base) * math.Pow(1.14, float64(p.Level)))
}

func (g *Game) findByAddr(addr string) *Player {
	lo := strings.ToLower(addr)
	for _, p := range g.players {
		if p.Online && strings.ToLower(p.Addr) == lo {
			return p
		}
	}
	return nil
}

func (g *Game) save() {
	if g.dataFile == "" {
		return
	}
	g.mu.Lock()
	data, err := json.MarshalIndent(g.players, "", "  ")
	g.mu.Unlock()
	if err != nil {
		log.Println("save error:", err)
		return
	}
	if err := os.WriteFile(g.dataFile, data, 0644); err != nil {
		log.Println("write error:", err)
	}
}

func (g *Game) load() {
	if g.dataFile == "" {
		return
	}
	data, err := os.ReadFile(g.dataFile)
	if err != nil {
		if !os.IsNotExist(err) {
			log.Println("load error:", err)
		}
		return
	}
	if err := json.Unmarshal(data, &g.players); err != nil {
		log.Println("parse error:", err)
		return
	}
	for _, p := range g.players {
		p.Online = false
		p.Addr = ""
	}
	log.Printf("loaded %d players", len(g.players))
}

// ttlForLevel returns seconds to next level. After level 60 it adds one day per level
// to prevent the curve from becoming impossibly steep.
func (g *Game) ttlForLevel(level int) int64 {
	var t int64
	if level <= 60 {
		t = int64(600 * math.Pow(1.16, float64(level)))
	} else {
		base := int64(600 * math.Pow(1.16, 60))
		t = base + int64(86400*(level-60))
	}
	if g.DevMode {
		t /= 5
	}
	return t
}

func newSalt() string {
	b := make([]byte, 16)
	rand.Read(b)
	return hex.EncodeToString(b)
}

func hashPass(salt, pass string) string {
	h := sha256.Sum256([]byte(salt + pass))
	return fmt.Sprintf("%x", h)
}

func extractNick(src string) string {
	if idx := strings.Index(src, "!"); idx > 0 {
		return src[:idx]
	}
	return src
}

var idleFlavors = []string{
	"The realm awaits brave heroes.",
	"Silence fills the land — idle and grow strong.",
	"Fortune favours the patient.",
	"The gods grow restless for new champions.",
	"Adventure calls... but patience pays.",
	"Even legends began by doing nothing.",
}

// updateTopic rebuilds and sets the channel topic from current game state.
// Safe to call from any goroutine; must NOT be called while holding mu.
func (g *Game) updateTopic() {
	if g.setTopic == nil {
		return
	}

	g.mu.Lock()
	online := 0
	var top *Player
	for _, p := range g.players {
		if p.Online {
			online++
		}
		if top == nil || p.Level > top.Level || (p.Level == top.Level && p.TTL < top.TTL) {
			top = p
		}
	}
	total := len(g.players)

	var questPart string
	if g.quest != nil {
		remaining := fmtDuration(int64(time.Until(g.quest.EndsAt).Seconds()))
		if g.quest.IsGrid {
			questPart = fmt.Sprintf("Grid quest: (%d,%d) — %s [%s left]",
				g.quest.QX, g.quest.QY, g.quest.Desc, remaining)
		} else {
			questPart = fmt.Sprintf("Quest: %s [%s left]", g.quest.Desc, remaining)
		}
	}
	lastEvent := g.lastEvent
	g.mu.Unlock()

	parts := []string{"⚔ IdleRPG"}

	if online == 0 && total == 0 {
		parts = append(parts, idleFlavors[mathrand.Intn(len(idleFlavors))])
	} else {
		parts = append(parts, fmt.Sprintf("%d/%d idling", online, total))
		if top != nil {
			parts = append(parts, fmt.Sprintf("Top: %s lvl %d %s", top.Nick, top.Level, top.Class))
		}
		if questPart != "" {
			parts = append(parts, questPart)
		}
		if lastEvent != "" {
			parts = append(parts, lastEvent)
		} else if online == 0 {
			parts = append(parts, idleFlavors[mathrand.Intn(len(idleFlavors))])
		}
	}

	g.setTopic(strings.Join(parts, " | "))
}

// noteEvent records a short event description and refreshes the topic.
// Must NOT be called while holding mu.
func (g *Game) noteEvent(msg string) {
	g.mu.Lock()
	g.lastEvent = msg
	g.mu.Unlock()
	g.updateTopic()
}

// classFocusSlot returns the item slot index (0-9) that is the focus of a given
// class. Derived via FNV-1a hash so every free-form class name maps to a unique,
// deterministic slot without requiring a fixed class list.
func classFocusSlot(class string) int {
	h := uint32(2166136261)
	for i := 0; i < len(class); i++ {
		c := class[i]
		if c >= 'A' && c <= 'Z' {
			c += 32 // lowercase
		}
		h ^= uint32(c)
		h *= 16777619
	}
	return int(h%10)
}

// effectiveItemSum returns the battle-relevant item sum for a player: the raw sum
// with each class's focus slot counted an extra time. Dual-classed players add
// two focus-slot bonuses (which stack if both classes share the same slot).
func effectiveItemSum(p *Player) int {
	sum := p.itemSum() + p.Items[classFocusSlot(p.Class)]
	if p.Class2 != "" {
		sum += p.Items[classFocusSlot(p.Class2)]
	}
	return sum
}

func fmtDuration(secs int64) string {
	if secs <= 0 {
		return "0s"
	}
	h := secs / 3600
	m := (secs % 3600) / 60
	s := secs % 60
	if h > 0 {
		return fmt.Sprintf("%dh%02dm%02ds", h, m, s)
	}
	if m > 0 {
		return fmt.Sprintf("%dm%02ds", m, s)
	}
	return fmt.Sprintf("%ds", s)
}
