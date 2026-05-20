// suggest.go generates character name and class suggestions for unregistered
// players who join the channel, using themed wordlists that match the
// cosmic horror / sci-fi setting of Void Drift.
package main

import mathrand "math/rand"

var suggestGivenNames = []string{
	"Vael", "Keth", "Sora", "Orin", "Drex", "Mira", "Cael", "Rhen",
	"Tova", "Lyx", "Neth", "Sael", "Vorin", "Kira", "Reth", "Omyr",
	"Lyra", "Caen", "Asha", "Threk", "Hesh", "Vel", "Dael", "Kael",
	"Aen", "Syx", "Orm", "Vreth", "Solen", "Taen", "Zara", "Prae",
	"Drae", "Mael", "Kern", "Sive", "Orath", "Lyss", "Vaen", "Rhyn",
}

var suggestEpithets = []string{
	"Ashborne", "Voidborn", "Driftmark", "Nullscar", "Palehand",
	"Echoless", "Signalless", "Collapseborn", "Veilmarked", "Driftbound",
	"Phasescar", "Entropyborn", "Echomarked", "Voidwalker", "Nullborn",
	"Ashwalker", "Veilborn", "Driftmarked", "Ashscar", "Signalborn",
	"Paleeye", "Nullwalker", "Voidmark", "Driftscar", "Echoborn",
}

var suggestClasses = []string{
	"NullWalker", "DriftSeeker", "SignalGhost", "VoidTouched",
	"PhaseDrifter", "EchoRemnant", "EntropySinger", "PaleArchitect",
	"VeilRunner", "CollapseSurvivor", "SignalHunter", "VoidDrifter",
	"NullSeeker", "DriftPhantom", "PhaseGhost", "EchoWalker",
	"ArchitectsShade", "DriftHermit", "NullAcolyte", "SignalWraith",
}

// generateSuggestion returns a random (name, class) pair drawn from the themed
// wordlists. Both are single IRC tokens (no spaces) suitable for use directly
// in !register: name is GivenNameEpithet (CamelCase, no separator), class is CamelCase.
func generateSuggestion() (name, class string) {
	given := suggestGivenNames[mathrand.Intn(len(suggestGivenNames))]
	epithet := suggestEpithets[mathrand.Intn(len(suggestEpithets))]
	name = given + epithet
	class = suggestClasses[mathrand.Intn(len(suggestClasses))]
	return
}
