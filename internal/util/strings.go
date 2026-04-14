// Package util provides shared string and formatting helpers used across DPM packages.
package util

import "strings"

// ContainsCI reports whether substr is within s (case-insensitive).
func ContainsCI(s, substr string) bool {
	return strings.Contains(strings.ToLower(s), strings.ToLower(substr))
}

// ContainsTagCI reports whether any tag in tags contains substr (case-insensitive).
func ContainsTagCI(tags []string, substr string) bool {
	for _, tag := range tags {
		if ContainsCI(tag, substr) {
			return true
		}
	}
	return false
}

// Trigrams returns the set of character trigrams for s (lowercased, boundary-padded).
// Boundary markers improve prefix/suffix match quality for short strings.
// Example: "nmap" → {"$nm", "nma", "map", "ap$"}
func Trigrams(s string) map[string]struct{} {
	s = strings.ToLower(s)
	padded := "$" + s + "$"
	trigrams := make(map[string]struct{}, len(padded))
	for i := 0; i+3 <= len(padded); i++ {
		trigrams[padded[i:i+3]] = struct{}{}
	}
	return trigrams
}

// TrigramSimilarity returns the Jaccard similarity (0.0–1.0) between
// the trigram sets of two strings. Used for fuzzy matching scoring.
func TrigramSimilarity(a, b string) float64 {
	ta := Trigrams(a)
	tb := Trigrams(b)
	if len(ta) == 0 && len(tb) == 0 {
		return 1.0
	}
	if len(ta) == 0 || len(tb) == 0 {
		return 0.0
	}
	intersection := 0
	for t := range ta {
		if _, ok := tb[t]; ok {
			intersection++
		}
	}
	union := len(ta) + len(tb) - intersection
	return float64(intersection) / float64(union)
}
