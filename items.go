// This file implements the unique/legendary item system: rarity tiers, drop
// probability, procedural name generation, and the !items command.
package main

import (
	"fmt"
	"math"
	mathrand "math/rand"
	"strings"
)

// Item rarity tier identifiers. rarityNormal is the empty string so that an
// item with no special name can be zero-valued without an extra boolean field.
const (
	rarityNormal    = ""
	rarityUncommon  = "Uncommon"
	rarityRare      = "Rare"
	rarityLegendary = "Legendary"
)

// Minimum player levels required to receive drops of each rarity tier.
// Below the threshold the rarity is simply skipped in the roll sequence.
const (
	uncommonMinLevel  = 25
	rareMinLevel      = 35
	legendaryMinLevel = 50
)

// Drop chance denominators (1-in-N per level-up). Checked in descending rarity
// order so a single level-up can produce at most one non-normal item.
const (
	legendaryChance = 200 // 0.5%
	rareChance      = 50  // 2.0%
	uncommonChance  = 20  // 5.0%
)

// Prefix word lists for each rarity tier, used by generateItemName.
var uncommonPrefixes = []string{
	"Salvaged", "Hardened", "Reclaimed", "Reinforced", "Patched",
	"Overclocked", "Jury-rigged", "Stripped", "Tempered", "Corroded",
}

var rarePrefixes = []string{
	"Void-touched", "Architect", "Signal-bound", "Null-threaded", "Drift-forged",
	"Echo-scarred", "Phase-locked", "Resonant", "Deep-etched", "Collapsed",
}

var legendaryPrefixes = []string{
	"Pale Architect", "Null-sovereign", "Void-eternal", "Signal-core", "Entropy-forged",
	"Last-light", "Pre-collapse", "Unravelled", "Drift-sovereign", "God-machine",
}

// slotNouns maps each item slot name to a list of flavourful nouns used as the
// second word in a generated item name (e.g. "Void-touched Resonator").
var slotNouns = map[string][]string{
	"ring":     {"Beacon", "Signet", "Resonator", "Loop", "Band"},
	"amulet":   {"Core", "Pendant", "Relay", "Tether", "Medallion"},
	"charm":    {"Shard", "Fragment", "Token", "Sliver", "Splinter"},
	"weapon":   {"Blade", "Lance", "Resonator", "Edge", "Fang"},
	"helm":     {"Cortex", "Visor", "Crown", "Caul", "Shroud"},
	"tunic":    {"Carapace", "Weave", "Mantle", "Liner", "Pall"},
	"gloves":   {"Gauntlets", "Grips", "Claws", "Bracers", "Fists"},
	"leggings": {"Greaves", "Plates", "Guards", "Cuisses", "Chausses"},
	"shield":   {"Barrier", "Ward", "Bulwark", "Aegis", "Shell"},
	"boots":    {"Treads", "Walkers", "Steps", "Striders", "Sabatons"},
}

// generateItemName produces a two-word procedural name ("Prefix Noun") for a
// non-normal item. The prefix is drawn from the rarity's word list and the noun
// from the slot's noun list, both chosen uniformly at random.
func generateItemName(rarity, slot string) string {
	var prefixes []string
	switch rarity {
	case rarityUncommon:
		prefixes = uncommonPrefixes
	case rarityRare:
		prefixes = rarePrefixes
	case rarityLegendary:
		prefixes = legendaryPrefixes
	}
	prefix := prefixes[mathrand.Intn(len(prefixes))]
	nouns := slotNouns[slot]
	noun := nouns[mathrand.Intn(len(nouns))]
	return prefix + " " + noun
}

// weightedItemLevel picks an item level in [min, max] using a geometric
// distribution with base 1.4. Level min has weight 1, min+1 has weight 1/1.4,
// min+2 has weight 1/1.4², and so on — making higher levels exponentially
// rarer within the allowed range.
func weightedItemLevel(min, max int) int {
	if min >= max {
		return min
	}
	const base = 1.4
	n := max - min + 1
	weights := make([]float64, n)
	total := 0.0
	for k := range weights {
		w := 1.0 / math.Pow(base, float64(k))
		weights[k] = w
		total += w
	}
	r := mathrand.Float64() * total
	for k, w := range weights {
		r -= w
		if r <= 0 {
			return min + k
		}
	}
	return min
}

// rollItemDrop determines the item granted on a level-up. Rarities are checked
// in descending order (Legendary → Rare → Uncommon → Normal); the first roll
// that succeeds sets the rarity and level range. The player's level must already
// have been incremented before calling this function.
//
// Returns: slot index (0–9), item level, unique name (empty for Normal), rarity.
func rollItemDrop(p *Player) (slot, level int, name, rarity string) {
	slot = mathrand.Intn(10)
	slotName := itemSlots[slot]

	if p.Level >= legendaryMinLevel && mathrand.Intn(legendaryChance) == 0 {
		// Legendary: item level is 3–5× player level, minimum 50–100.
		min := int(math.Max(float64(p.Level)*3, 50))
		max := int(math.Max(float64(p.Level)*5, 100))
		if max <= min {
			max = min + 1
		}
		level = weightedItemLevel(min, max)
		name = generateItemName(rarityLegendary, slotName)
		return slot, level, name, rarityLegendary
	}

	if p.Level >= rareMinLevel && mathrand.Intn(rareChance) == 0 {
		// Rare: item level is 2–3× player level.
		min := p.Level*2 + 1
		max := p.Level * 3
		if max <= min {
			max = min + 1
		}
		level = weightedItemLevel(min, max)
		name = generateItemName(rarityRare, slotName)
		return slot, level, name, rarityRare
	}

	if p.Level >= uncommonMinLevel && mathrand.Intn(uncommonChance) == 0 {
		// Uncommon: item level is 1.5–2× player level.
		min := int(float64(p.Level)*1.5) + 1
		max := p.Level * 2
		if max <= min {
			max = min + 1
		}
		level = weightedItemLevel(min, max)
		name = generateItemName(rarityUncommon, slotName)
		return slot, level, name, rarityUncommon
	}

	// Normal drop: item level is 1 to 1.5× player level, weighted toward lower values.
	maxNormal := int(math.Max(float64(p.Level)*1.5, 1))
	level = weightedItemLevel(1, maxNormal)
	return slot, level, "", rarityNormal
}

// rarityLabel returns the IRC channel announcement label for a non-normal drop.
// Returns "" for normal items so callers can append it unconditionally.
func rarityLabel(rarity string) string {
	switch rarity {
	case rarityUncommon:
		return "[Uncommon]"
	case rarityRare:
		return "[** Rare **]"
	case rarityLegendary:
		return "[*** LEGENDARY ***]"
	}
	return ""
}

// CmdItems returns the full item loadout for the target player, including
// unique names where present. If targetNick is empty, reports on the calling
// player.
func (g *Game) CmdItems(src, targetNick string) string {
	if targetNick == "" {
		targetNick = extractNick(src)
	}
	g.mu.Lock()
	p, ok := g.players[strings.ToLower(targetNick)]
	g.mu.Unlock()
	if !ok {
		return fmt.Sprintf("No character found for %s.", targetNick)
	}

	parts := make([]string, 0, 10)
	for i, slot := range itemSlots {
		lvl := p.Items[i]
		if lvl == 0 {
			continue
		}
		entry := fmt.Sprintf("%s:%d", slot, lvl)
		if p.ItemNames[i] != "" {
			entry += fmt.Sprintf(" (%s)", p.ItemNames[i])
		}
		parts = append(parts, entry)
	}
	if len(parts) == 0 {
		return fmt.Sprintf("%s has no items yet.", p.Nick)
	}
	return fmt.Sprintf("%s's items: %s [total: %d]", p.Nick, strings.Join(parts, " | "), p.itemSum())
}
