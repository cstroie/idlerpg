package main

import (
	"fmt"
	"math"
	mathrand "math/rand"
	"strings"
)

// Item rarity tiers.
const (
	rarityNormal    = ""
	rarityUncommon  = "Uncommon"
	rarityRare      = "Rare"
	rarityLegendary = "Legendary"
)

// Unlock levels for each rarity tier.
const (
	uncommonMinLevel  = 25
	rareMinLevel      = 35
	legendaryMinLevel = 50
)

// Drop chances per level-up (checked in descending rarity order).
const (
	legendaryChance = 200 // 1 in 200  (0.5%)
	rareChance      = 50  // 1 in 50   (2%)
	uncommonChance  = 20  // 1 in 20   (5%)
)

var uncommonPrefixes = []string{
	"Polished", "Superior", "Enchanted", "Gleaming", "Sturdy",
	"Refined", "Tempered", "Honed", "Fortified", "Keen",
}

var rarePrefixes = []string{
	"Ancient", "Ethereal", "Hallowed", "Runic", "Shadowed",
	"Arcane", "Spectral", "Veiled", "Infused", "Soulbound",
}

var legendaryPrefixes = []string{
	"Legendary", "Divine", "Mythical", "Eternal", "Godforged",
	"Celestial", "Abyssal", "Primordial", "Transcendent", "Undying",
}

var slotNouns = map[string][]string{
	"ring":     {"Ring", "Band", "Loop", "Signet", "Coil"},
	"amulet":   {"Amulet", "Pendant", "Talisman", "Medallion", "Locket"},
	"charm":    {"Charm", "Token", "Relic", "Fetish", "Totem"},
	"weapon":   {"Blade", "Staff", "Edge", "Shard", "Fang"},
	"helm":     {"Crown", "Helm", "Circlet", "Visage", "Diadem"},
	"tunic":    {"Robe", "Vestment", "Mantle", "Raiment", "Shroud"},
	"gloves":   {"Gauntlets", "Grips", "Fists", "Bracers", "Claws"},
	"leggings": {"Greaves", "Legguards", "Cuisses", "Tassets", "Chausses"},
	"shield":   {"Aegis", "Bulwark", "Ward", "Bastion", "Rampart"},
	"boots":    {"Treads", "Striders", "Sabatons", "Steps", "Walkers"},
}

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

// weightedItemLevel picks a level in [min, max] with probability proportional
// to 1/(1.4^k) where k = level-min, making higher levels exponentially rarer.
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

// rollItemDrop determines a level-up item drop for the player.
// Returns slot index, item level, unique name (empty for normal), and rarity string.
// Must be called with the player's level already incremented.
func rollItemDrop(p *Player) (slot, level int, name, rarity string) {
	slot = mathrand.Intn(10)
	slotName := itemSlots[slot]

	if p.Level >= legendaryMinLevel && mathrand.Intn(legendaryChance) == 0 {
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
		min := int(float64(p.Level)*1.5) + 1
		max := p.Level * 2
		if max <= min {
			max = min + 1
		}
		level = weightedItemLevel(min, max)
		name = generateItemName(rarityUncommon, slotName)
		return slot, level, name, rarityUncommon
	}

	// Normal drop: level 1 to 1.5× player level, weighted toward lower values.
	maxNormal := int(math.Max(float64(p.Level)*1.5, 1))
	level = weightedItemLevel(1, maxNormal)
	return slot, level, "", rarityNormal
}

// rarityLabel returns an IRC-friendly label for announcing unique drops.
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

// CmdItems shows a player's full item loadout with unique names where applicable.
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
