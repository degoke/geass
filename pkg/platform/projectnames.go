package platform

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"math/big"
	"strings"
)

var projectAdjectives = []string{
	"aged", "amber", "ancient", "autumn", "azure", "billowing", "bitter", "blue",
	"bold", "brave", "brisk", "broad", "bright", "burning", "calm", "candid",
	"clear", "clever", "coastal", "cool", "crimson", "crystal", "curious", "dancing",
	"dawn", "distant", "divine", "dreamy", "dusky", "eager", "early", "earthy",
	"ember", "emerald", "endless", "even", "fading", "faint", "falling", "feather",
	"fierce", "floral", "floating", "fragrant", "frosty", "gentle", "glacial", "gleaming",
	"golden", "grand", "grassy", "green", "hidden", "hollow", "humble", "hushed",
	"icy", "ivory", "jolly", "kind", "kindred", "late", "light", "little",
	"lively", "lonely", "luminous", "lunar", "marbled", "measured", "mellow", "mighty",
	"mild", "misty", "modest", "morning", "mossy", "mystic", "nimble", "noble",
	"northern", "open", "orange", "painted", "pale", "patient", "peaceful", "polished",
	"proud", "purple", "quiet", "radiant", "rapid", "restless", "roaming", "rosy",
	"royal", "rustic", "sacred", "sandy", "scarlet", "serene", "shaded", "shadow",
	"shimmering", "shiny", "silent", "silver", "sleepy", "small", "smoky", "snowy",
	"soaring", "soft", "solemn", "solitary", "sparkling", "spacious", "spring", "steady",
	"still", "stellar", "stormy", "summer", "sunny", "swift", "tender", "timber",
	"tranquil", "twilight", "vast", "velvet", "verdant", "vivid", "warm", "wandering",
	"weathered", "whispering", "wild", "winding", "winter", "wise", "wooded", "woven",
	"young",
}

var projectNouns = []string{
	"anchor", "aurora", "bark", "basin", "bay", "beach", "bloom", "branch",
	"breeze", "brook", "canopy", "canyon", "castle", "cedar", "cliff", "cloud",
	"coast", "comet", "coral", "cove", "creek", "crest", "crystal", "current",
	"dawn", "delta", "desert", "dew", "dream", "dune", "echo", "elm",
	"ember", "fern", "field", "fire", "fjord", "flame", "flower", "forest",
	"frost", "garden", "gate", "glacier", "glade", "glen", "glow", "grass",
	"grove", "harbor", "harvest", "haven", "haze", "heath", "heather", "hill",
	"hollow", "horizon", "island", "lagoon", "lake", "lantern", "lark", "leaf",
	"light", "lily", "meadow", "mesa", "mirror", "mist", "moon", "morning",
	"moss", "mountain", "nebula", "night", "oasis", "ocean", "orchard", "orchid",
	"path", "peak", "pebble", "petal", "pine", "plain", "pond", "prairie",
	"quartz", "rain", "ravine", "reef", "ridge", "ripple", "river", "rock",
	"sage", "sand", "shadow", "shell", "shore", "sky", "snow", "sound",
	"spark", "spire", "spring", "spruce", "star", "stone", "storm", "stream",
	"summit", "sun", "sunrise", "sunset", "thicket", "thunder", "tide", "timber",
	"trail", "tree", "valley", "vapor", "vista", "voyage", "water", "waterfall",
	"wave", "willow", "wind", "wood", "woodland", "warmth",
}

// GenerateProjectID returns a stable, DNS-safe project identifier.
func GenerateProjectID() (string, error) {
	b := make([]byte, 4)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("unable to generate project id: %w", err)
	}
	return "proj-" + hex.EncodeToString(b), nil
}

// GenerateDisplayName returns a lowercase slug-style project name (e.g. morning-beach).
func GenerateDisplayName() (string, error) {
	adj, err := randomWord(projectAdjectives)
	if err != nil {
		return "", err
	}
	noun, err := randomWord(projectNouns)
	if err != nil {
		return "", err
	}
	return adj + "-" + noun, nil
}

// NormalizeProjectName converts input into a lowercase slug with dashes between words.
func NormalizeProjectName(name string) string {
	name = strings.TrimSpace(strings.ToLower(name))
	if name == "" {
		return ""
	}
	var b strings.Builder
	lastDash := false
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			lastDash = false
		case r == ' ', r == '_', r == '-':
			if !lastDash && b.Len() > 0 {
				b.WriteByte('-')
				lastDash = true
			}
		}
	}
	return strings.Trim(b.String(), "-")
}

func randomWord(words []string) (string, error) {
	if len(words) == 0 {
		return "", fmt.Errorf("word list is empty")
	}
	n, err := rand.Int(rand.Reader, big.NewInt(int64(len(words))))
	if err != nil {
		return "", err
	}
	return words[n.Int64()], nil
}
