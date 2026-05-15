package main

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"math"
	"math/rand"
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

type Player struct {
	Nick     string
	Class    string
	PassHash string
	Level    int
	TTL      int64  // seconds until next level
	Items    [10]int // item level per slot
	Online   bool
	Addr     string // nick!user@host when online
}

func (p *Player) itemSum() int {
	s := 0
	for _, v := range p.Items {
		s += v
	}
	return s
}

type Game struct {
	players  map[string]*Player // keyed by lowercase nick
	mu       sync.Mutex
	dataFile string
	say      func(string) // sends a message to the game channel
}

func newGame(dataFile string, say func(string)) *Game {
	g := &Game{
		players:  make(map[string]*Player),
		dataFile: dataFile,
		say:      say,
	}
	g.load()
	return g
}

func (g *Game) start() {
	go g.tick()
}

// OnJoin auto-logins a registered player when they join the channel.
func (g *Game) OnJoin(src string) {
	nick := extractNick(src)
	g.mu.Lock()
	p := g.players[strings.ToLower(nick)]
	if p != nil {
		p.Online = true
		p.Addr = src
	}
	g.mu.Unlock()
	if p != nil {
		g.save()
		g.say(fmt.Sprintf("%s, the level %d %s, has joined IdleRPG! Next level in %s.",
			p.Nick, p.Level, p.Class, fmtDuration(p.TTL)))
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
	}
}

// OnNick applies nick-change penalty and re-keys the player map.
func (g *Game) OnNick(src, newNick string) {
	oldNick := extractNick(src)
	g.mu.Lock()
	p := g.players[strings.ToLower(oldNick)]
	if p != nil && p.Online {
		g.applyPenalty(p, 30)
		delete(g.players, strings.ToLower(oldNick))
		p.Nick = newNick
		p.Addr = strings.Replace(p.Addr, oldNick, newNick, 1)
		g.players[strings.ToLower(newNick)] = p
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
	p := &Player{
		Nick:     nick,
		Class:    class,
		PassHash: hashPass(pass),
		Level:    0,
		TTL:      ttlForLevel(0),
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
	if p.PassHash != hashPass(pass) {
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
	return fmt.Sprintf("%s, the level %d %s [%s] — TTL: %s — Items: %d",
		p.Nick, p.Level, p.Class, status, fmtDuration(p.TTL), p.itemSum())
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
		parts[i] = fmt.Sprintf("%d. %s (lvl %d)", i+1, p.Nick, p.Level)
	}
	return "Top players: " + strings.Join(parts, " | ")
}

func (g *Game) tick() {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()

	for range ticker.C {
		g.mu.Lock()
		var levelUps []*Player
		for _, p := range g.players {
			if !p.Online {
				continue
			}
			p.TTL--
			if p.TTL <= 0 {
				levelUps = append(levelUps, p)
			} else if rand.Intn(86400) == 0 {
				g.randomEvent(p)
			}
		}
		g.mu.Unlock()

		for _, p := range levelUps {
			g.doLevelUp(p)
		}
		if len(levelUps) > 0 {
			g.save()
		}
	}
}

func (g *Game) doLevelUp(p *Player) {
	g.mu.Lock()
	p.Level++
	p.TTL = ttlForLevel(p.Level)

	slot := rand.Intn(10)
	maxItem := int(math.Max(float64(p.Level)*1.5, 1))
	itemLevel := rand.Intn(maxItem) + 1
	improved := itemLevel > p.Items[slot]
	if improved {
		p.Items[slot] = itemLevel
	}
	slotName := itemSlots[slot]
	nick := p.Nick
	level := p.Level
	ttl := p.TTL
	isum := p.itemSum()

	var opponents []*Player
	for _, op := range g.players {
		if op.Online && strings.ToLower(op.Nick) != strings.ToLower(nick) {
			opponents = append(opponents, op)
		}
	}
	g.mu.Unlock()

	msg := fmt.Sprintf("%s has attained level %d! Next level in %s. They find a %s of level %d",
		nick, level, fmtDuration(ttl), slotName, itemLevel)
	if improved {
		msg += " (equipped!)"
	}
	msg += fmt.Sprintf(" [item total: %d]", isum)
	g.say(msg)

	if len(opponents) > 0 {
		g.battle(p, opponents[rand.Intn(len(opponents))])
	}
}

func (g *Game) battle(a, b *Player) {
	g.mu.Lock()

	aSum := a.itemSum()
	bSum := b.itemSum()
	if aSum < 1 {
		aSum = 1
	}
	if bSum < 1 {
		bSum = 1
	}

	aRoll := rand.Intn(aSum)
	bRoll := rand.Intn(bSum)

	winner, loser := a, b
	wRoll, lRoll := aRoll, bRoll
	if bRoll > aRoll {
		winner, loser = b, a
		wRoll, lRoll = bRoll, aRoll
	}

	pct := int(math.Max(float64(loser.Level)/4.0, 7))
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
	g.mu.Unlock()

	g.say(fmt.Sprintf("%s [%d/%d] battles %s [%d/%d] and wins! TTL adjusted by %d%%.",
		wName, wRoll, aSum, lName, lRoll, bSum, pct))
}

func (g *Game) randomEvent(p *Player) {
	pct := rand.Intn(8) + 5
	change := p.TTL * int64(pct) / 100
	if change < 1 {
		change = 1
	}
	if rand.Intn(2) == 0 {
		p.TTL += change
		go g.say(fmt.Sprintf("%s has been struck by misfortune! TTL increased by %d%%.", p.Nick, pct))
	} else {
		p.TTL -= change
		if p.TTL < 1 {
			p.TTL = 1
		}
		go g.say(fmt.Sprintf("The gods smile upon %s! TTL reduced by %d%%.", p.Nick, pct))
	}
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
		fmt.Println("save error:", err)
		return
	}
	if err := os.WriteFile(g.dataFile, data, 0644); err != nil {
		fmt.Println("write error:", err)
	}
}

func (g *Game) load() {
	if g.dataFile == "" {
		return
	}
	data, err := os.ReadFile(g.dataFile)
	if err != nil {
		if !os.IsNotExist(err) {
			fmt.Println("load error:", err)
		}
		return
	}
	if err := json.Unmarshal(data, &g.players); err != nil {
		fmt.Println("parse error:", err)
		return
	}
	for _, p := range g.players {
		p.Online = false
		p.Addr = ""
	}
	fmt.Printf("loaded %d players\n", len(g.players))
}

func ttlForLevel(level int) int64 {
	return int64(600 * math.Pow(1.16, float64(level)))
}

func hashPass(pass string) string {
	h := sha256.Sum256([]byte(pass))
	return fmt.Sprintf("%x", h)
}

func extractNick(src string) string {
	if idx := strings.Index(src, "!"); idx > 0 {
		return src[:idx]
	}
	return src
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
