// Package text ports ntx-packages/libs/core/src/services/text.service.ts.
// TextService generates random username-style strings and applies common
// text transforms (titleCase, slugify, prettify).
package text

import (
	cryptorand "crypto/rand"
	"encoding/binary"
	"strings"
	"unicode"
)

// nouns / adjectives are abbreviated subsets of the TS service. The TS service
// has ~200 of each; here we ship a representative slice. Callers needing the
// full TS dictionary can replace these slices.
var nouns = []string{
	"animal", "apple", "atom", "aurora", "autumn", "bacon", "ball", "banana",
	"beach", "bear", "bee", "bird", "boat", "book", "bowl", "branch", "breeze",
	"brook", "candy", "canyon", "cat", "cave", "cliff", "cloud", "cookie",
	"creek", "dawn", "dew", "dog", "duck", "dusk", "earth", "fall", "field",
	"firefly", "fish", "flower", "fog", "forest", "frog", "galaxy", "garden",
	"glass", "grove", "hill", "horse", "house", "ice", "island", "lagoon",
	"lake", "land", "leaf", "light", "lion", "marsh", "meadow", "mist", "moon",
	"moss", "mountain", "ocean", "peak", "pine", "pond", "rabbit", "rain",
	"reef", "river", "rock", "sage", "sand", "shark", "sky", "snowflake",
	"sparrow", "spring", "star", "stone", "storm", "stream", "summer",
	"summit", "sun", "sunrise", "sunset", "surf", "swamp", "thorns", "tiger",
	"tree", "tundra", "valley", "volcano", "water", "waterfall", "willow",
	"window", "winter", "zebra",
}

var adjectives = []string{
	"able", "active", "adept", "adored", "agile", "alert", "alive", "ample",
	"ancient", "amazing", "bold", "brave", "bright", "bristly", "calm",
	"capable", "careful", "caring", "cheerful", "chilly", "classic", "clean",
	"clear", "clever", "cold", "cool", "cosmic", "curious", "daring", "dazzling",
	"deep", "devoted", "distant", "dreamy", "earnest", "eager", "easy",
	"electric", "elegant", "endless", "enchanting", "epic", "eternal", "expert",
	"famed", "fancy", "fast", "fearless", "feline", "fierce", "fine", "firm",
	"fit", "flowing", "fluffy", "fortunate", "free", "fresh", "friendly",
	"funny", "gentle", "gifted", "gleaming", "glorious", "golden", "graceful",
	"grand", "great", "happy", "hardy", "harmonious", "honest", "humble",
	"inspired", "jolly", "joyful", "keen", "kind", "lavish", "lively", "loyal",
	"lucky", "majestic", "mighty", "mild", "modest", "noble", "open", "patient",
	"peaceful", "playful", "polite", "proud", "pure", "quick", "quiet", "rare",
	"royal", "rugged", "safe", "serene", "sharp", "silky", "silver", "sincere",
	"smart", "smooth", "soft", "solid", "sparkling", "splendid", "spry",
	"steady", "stellar", "strong", "subtle", "swift", "tame", "thrifty", "timely",
	"tireless", "tranquil", "true", "trusted", "vast", "vibrant", "vivid",
	"warm", "wise", "witty", "young", "zealous",
}

// Service generates random text and applies common transforms.
type Service struct{}

// New constructs a TextService. Stateless.
func New() *Service { return &Service{} }

// Username returns "{adjective}-{noun}-{n}" where n is a random 3-digit number.
// Mirrors TS TextService.username() naming convention.
func (s *Service) Username() string {
	adj := adjectives[randInt(len(adjectives))]
	noun := nouns[randInt(len(nouns))]
	num := 100 + randInt(900) // 100..999
	return adj + "-" + noun + "-" + itoa(num)
}

// TitleCase upper-cases the first letter of each word.
func (s *Service) TitleCase(in string) string {
	parts := strings.Fields(in)
	for i, p := range parts {
		if len(p) == 0 {
			continue
		}
		runes := []rune(p)
		runes[0] = unicode.ToUpper(runes[0])
		parts[i] = string(runes)
	}
	return strings.Join(parts, " ")
}

// Slugify converts text to a URL-safe slug.
func (s *Service) Slugify(in string) string {
	in = strings.ToLower(in)
	var b strings.Builder
	prevDash := false
	for _, r := range in {
		switch {
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			b.WriteRune(r)
			prevDash = false
		default:
			if !prevDash {
				b.WriteByte('-')
				prevDash = true
			}
		}
	}
	return strings.Trim(b.String(), "-")
}

// Prettify replaces underscores/dashes with spaces and title-cases.
func (s *Service) Prettify(in string) string {
	in = strings.ReplaceAll(in, "_", " ")
	in = strings.ReplaceAll(in, "-", " ")
	return s.TitleCase(in)
}

// Truncate cuts text to maxLen and appends "..." if truncated.
func (s *Service) Truncate(in string, maxLen int) string {
	if maxLen <= 0 || len(in) <= maxLen {
		return in
	}
	if maxLen <= 3 {
		return in[:maxLen]
	}
	return in[:maxLen-3] + "..."
}

func randInt(max int) int {
	if max <= 0 {
		return 0
	}
	var b [8]byte
	if _, err := cryptorand.Read(b[:]); err != nil {
		return 0
	}
	return int(binary.BigEndian.Uint64(b[:]) % uint64(max))
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [12]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
