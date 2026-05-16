// Package text ports ntx-packages/libs/core/src/services/text.service.ts.
// TextService produces random adjective/noun/nonce names. The noun and
// adjective dictionaries below are copied verbatim from the TS service.
package text

import (
	cryptorand "crypto/rand"
	"strconv"

	coreutils "github.com/thescaffold/gox-packages/libs/core/utils"
)

var nouns = []string{
	"abyss", "animal", "apple", "atoll", "aurora", "autumn", "bacon", "badlands", "ball",
	"banana", "bath", "beach", "bear", "bed", "bee", "bike", "bird", "boat", "book", "bowl",
	"branch", "bread", "breeze", "briars", "brook", "brush", "bunny", "candy", "canopy",
	"canyon", "car", "cat", "cave", "cavern", "cereal", "chair", "chasm", "chip", "cliff",
	"coal", "coast", "cookie", "cove", "cow", "crater", "creek", "darkness", "dawn", "desert",
	"dew", "dog", "door", "dove", "drylands", "duck", "dusk", "earth", "fall", "farm", "fern",
	"field", "firefly", "fish", "fjord", "flood", "flower", "flowers", "fog", "foliage",
	"forest", "freeze", "frog", "fu", "galaxy", "garden", "geyser", "gift", "glass", "grove",
	"guide", "guru", "hat", "hug", "hero", "hill", "horse", "house", "hurricane", "ice",
	"iceberg", "island", "juice", "lagoon", "lake", "land", "lawn", "leaf", "leaves", "light",
	"lion", "marsh", "meadow", "milk", "mist", "moon", "moss", "mountain", "mouse", "nature",
	"oasis", "ocean", "pants", "peak", "pebble", "pine", "pilot", "plane", "planet", "plant",
	"plateau", "pond", "prize", "rabbit", "rain", "range", "reef", "reserve", "resonance",
	"river", "rock", "sage", "salute", "sanctuary", "sand", "sands", "shark", "shelter", "shirt",
	"shoe", "silence", "sky", "smokescreen", "snowflake", "socks", "soil", "soul", "soup",
	"sparrow", "spoon", "spring", "star", "stone", "storm", "stream", "summer", "summit", "sun",
	"sunrise", "sunset", "sunshine", "surf", "swamp", "table", "teacher", "temple", "thorns",
	"tiger", "tigers", "towel", "train", "tree", "truck", "tsunami", "tundra", "valley",
	"volcano", "water", "waterfall", "waves", "wild", "willow", "window", "winds", "winter",
	"zebra",
}

var adjectives = []string{
	"able", "action", "active", "actual", "adept", "adored", "adroit", "affectionate", "agile",
	"airy", "alert", "alive", "alter", "amiable", "ample", "and", "anima", "apt", "ardent",
	"are", "astute", "august", "avid", "awake", "aware", "balmy", "benevolent", "big",
	"billowing", "blessed", "bold", "boss", "brainy", "brave", "brawny", "breezy", "brief",
	"bright", "brisk", "busy", "calm", "can", "canny", "cared", "caring", "casual", "celestial",
	"charming", "chic", "chief", "choice", "chosen", "chummy", "civic", "civil", "classy",
	"clean", "clear", "clever", "close", "cogent", "composed", "cool", "cosmic", "cozy",
	"cuddly", "cute", "dainty", "dandy", "dapper", "daring", "dear", "decent", "deep", "deft",
	"deluxe", "devout", "direct", "divine", "doted", "doting", "dreamy", "driven", "dry",
	"earthy", "easy", "elated", "energized", "enigmatic", "equal", "exact", "exotic", "expert",
	"exuberant", "fair", "famed", "famous", "fancy", "fast", "fiery", "fine", "fit", "flashy",
	"fleek", "fleet", "flowing", "fluent", "fluffy", "fluttering", "flying", "fond", "frank",
	"free", "fresh", "full", "fun", "funny", "fuscia", "genial", "gentle", "giddy", "gifted",
	"giving", "glad", "gnarly", "gold", "golden", "good", "goodly", "graceful", "grand", "great",
	"green", "groovy", "guided", "gutsy", "haloed", "happy", "hardy", "harmonious", "hearty",
	"heroic", "high", "hip", "hollow", "holy", "honest", "huge", "humane", "humble", "hunky",
	"icy", "ideal", "immune", "indigo", "inquisitive", "jazzed", "jazzy", "jolly", "jovial",
	"joyful", "joyous", "jubilant", "juicy", "just", "keen", "khaki", "kind", "kingly", "large",
	"lavish", "lawful", "left", "legal", "legit", "light", "like", "liked", "likely", "limber",
	"limitless", "lively", "loved", "lovely", "loyal", "lucid", "lucky", "lush", "main", "major",
	"master", "mature", "max", "maxed", "mellow", "merciful", "merry", "mighty", "mint",
	"mirthful", "modern", "modest", "money", "moonlit", "moral", "moving", "mucho", "mutual",
	"mysterious", "native", "natural", "near", "neat", "needed", "new", "nice", "nifty",
	"nimble", "noble", "normal", "noted", "novel", "okay", "open", "outrageous", "overt",
	"pacific", "parched", "peachy", "peppy", "pithy", "placid", "pleasant", "plucky", "plum",
	"poetic", "poised", "polite", "posh", "potent", "pretty", "prime", "primo", "prized", "pro",
	"prompt", "proper", "proud", "pumped", "punchy", "pure", "purring", "quaint", "quick",
	"quiet", "rad", "radioactive", "rapid", "rare", "ready", "real", "regal", "resilient",
	"rich", "right", "robust", "rooted", "rosy", "rugged", "safe", "sassy", "saucy", "savvy",
	"scenic", "secret", "seemly", "serene", "sharp", "showy", "shrewd", "simple", "sleek",
	"slick", "smart", "smiley", "smooth", "snappy", "snazzy", "snowy", "snugly", "social",
	"sole", "solitary", "sound", "spacial", "spicy", "spiffy", "spry", "stable", "star", "stark",
	"steady", "stoic", "strong", "stunning", "sturdy", "suave", "subtle", "sunny", "sunset",
	"super", "superb", "sure", "swank", "sweet", "swell", "swift", "talented", "teal", "the",
	"thriving", "tidy", "timely", "top", "tops", "tough", "touted", "tranquil", "trim",
	"tropical", "true", "trusty", "undisturbed", "unique", "united", "unsightly", "unwavering",
	"upbeat", "uplifting", "urbane", "usable", "useful", "utmost", "valid", "vast", "vestal",
	"viable", "vital", "vivid", "vocal", "vogue", "voiceless", "volant", "wandering", "wanted",
	"warm", "wealthy", "whispering", "whole", "winged", "wired", "wise", "witty", "wooden",
	"worthy", "zealous",
}

var workspaceNouns = []string{
	"center", "core", "nexus", "focus", "epicenter", "heart", "pivot", "mainstay", "area",
	"region", "territory", "sector", "district", "locale", "domain", "realm", "space",
	"proximity", "nucleus", "junction", "headquarters", "crossroads", "seat", "workspace",
	"base", "lab", "hub", "zone", "sphere", "oasis", "lair", "forge", "sanctum", "enclave",
	"haven", "arena", "playground", "workshop", "studio", "sanctuary",
}

var workspaceAdjectives = []string{
	"center", "core", "nexus", "central point", "focal point", "focus", "epicenter", "heart",
	"pivot", "mainstay", "area", "region", "prime", "nexus", "core", "optima", "stellar",
	"prodigy", "fusion", "vertex", "alpha", "catalyst", "wonder", "innovation", "marvel",
	"genius", "dream", "spectra", "epic", "imagination", "infinity", "phenomenon",
}

var workspacePhrases = []string{
	"epic arena for extraordinary feats", "marvelous realm of creativity",
	"innovation oasis for astonishing endeavors",
	"enchanting domain of remarkable accomplishments",
	"stellar playground for unbelievable wonders",
	"spectacular workshop for extraordinary creations",
	"magnificent studio of incredible achievements", "phenomenal hub for astonishing ventures",
	"extraordinary sanctuary for unparalleled innovation",
	"astonishing haven for extraordinary pursuits",
}

// Service mirrors TS TextService. It is stateless.
type Service struct {
	// Workspace, App and Stores mirror the nested TS sub-objects.
	Workspace *WorkspaceText
	App       *AppText
	Stores    *StoresText
}

// New constructs a TextService with its nested namespaces wired.
func New() *Service {
	s := &Service{}
	s.Workspace = &WorkspaceText{parent: s}
	s.App = &AppText{parent: s}
	s.Stores = &StoresText{parent: s}
	return s
}

// Get returns a random element of items. Mirrors TS get():
// items[Math.floor(Math.random() * items.length)].
func (s *Service) Get(items []string) string {
	if len(items) == 0 {
		return ""
	}
	return items[randInt(len(items))]
}

// Noun returns a random noun.
func (s *Service) Noun() string { return s.Get(nouns) }

// Adjective returns a random adjective.
func (s *Service) Adjective() string { return s.Get(adjectives) }

// Nonce returns a single random byte value (0-255), mirroring TS nonce()
// which reads one byte via getRandomValues and returns it as a number.
func (s *Service) Nonce() int {
	var b [1]byte
	if _, err := cryptorand.Read(b[:]); err != nil {
		return 0
	}
	return int(b[0])
}

// Name returns "{adjective}{sep}{noun}{sep}{nonce}". The separator
// defaults to "-" when empty, matching TS name(separator = '-').
func (s *Service) Name(separator string) string {
	if separator == "" {
		separator = "-"
	}
	return s.Adjective() + separator + s.Noun() + separator + strconv.Itoa(s.Nonce())
}

// WorkspaceText mirrors TS TextService.workspace.
type WorkspaceText struct{ parent *Service }

// Noun returns a random workspace noun.
func (w *WorkspaceText) Noun() string { return w.parent.Get(workspaceNouns) }

// Adjective returns a random workspace adjective.
func (w *WorkspaceText) Adjective() string { return w.parent.Get(workspaceAdjectives) }

// Phrase returns a random workspace phrase.
func (w *WorkspaceText) Phrase() string { return w.parent.Get(workspacePhrases) }

// Name returns titleCase("{adjective} {noun} {nonce}") with spaces removed,
// mirroring TS workspace.name().
func (w *WorkspaceText) Name() string {
	raw := w.Adjective() + " " + w.Noun() + " " + strconv.Itoa(w.parent.Nonce())
	titled := coreutils.TitleCase(raw)
	out := make([]rune, 0, len(titled))
	for _, r := range titled {
		if r != ' ' {
			out = append(out, r)
		}
	}
	return string(out)
}

// Desc returns the workspace phrase with its first character upper-cased,
// mirroring TS workspace.desc().
func (w *WorkspaceText) Desc() string {
	return coreutils.UCFirst(w.Phrase())
}

// AppText mirrors TS TextService.app.
type AppText struct{ parent *Service }

// Name returns "{adjective}{sep}{noun}{sep}{nonce}" using the parent's
// dictionaries, mirroring TS app.name(separator = '-').
func (a *AppText) Name(separator string) string {
	if separator == "" {
		separator = "-"
	}
	return a.parent.Adjective() + separator + a.parent.Noun() + separator +
		strconv.Itoa(a.parent.Nonce())
}

// StoresText mirrors TS TextService.stores.
type StoresText struct{ parent *Service }

// Name returns "{adjective}{sep}{noun}{sep}{nonce}" using the parent's
// dictionaries, mirroring TS stores.name(separator = '-').
func (st *StoresText) Name(separator string) string {
	if separator == "" {
		separator = "-"
	}
	return st.parent.Adjective() + separator + st.parent.Noun() + separator +
		strconv.Itoa(st.parent.Nonce())
}

// randInt returns a uniformly random int in [0, max).
func randInt(max int) int {
	if max <= 0 {
		return 0
	}
	var b [8]byte
	if _, err := cryptorand.Read(b[:]); err != nil {
		return 0
	}
	var n uint64
	for _, x := range b {
		n = n<<8 | uint64(x)
	}
	return int(n % uint64(max))
}
