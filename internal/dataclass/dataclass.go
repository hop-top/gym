// Package dataclass defines the ordered data-classification ladder a
// skill manifest declares and an operator ceiling bounds.
package dataclass

import "strings"

// Ladder lists the classes from lowest to highest.
var Ladder = []string{"public", "internal", "confidential", "personal", "special_category"}

// Default is the ceiling applied when none is configured.
const Default = "internal"

// Rank returns the position of class in Ladder (0 = lowest).
func Rank(class string) (int, bool) {
	for i, c := range Ladder {
		if c == class {
			return i, true
		}
	}
	return 0, false
}

// Valid reports whether class is on the ladder.
func Valid(class string) bool {
	_, ok := Rank(class)
	return ok
}

// Over reports whether class ranks strictly above ceiling. Unknown
// values never count as over: callers validate before comparing.
func Over(class, ceiling string) bool {
	c, ok1 := Rank(class)
	m, ok2 := Rank(ceiling)
	return ok1 && ok2 && c > m
}

// Names renders the ladder for error messages.
func Names() string { return strings.Join(Ladder, ", ") }
