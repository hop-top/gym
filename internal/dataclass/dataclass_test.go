package dataclass_test

import (
	"testing"

	"hop.top/gym/internal/dataclass"
)

// Case 18: ladder boundaries. equal -> allow, one above -> over, and
// special_category is over every ceiling below it.
func TestOver_LadderBoundaries(t *testing.T) {
	for i, class := range dataclass.Ladder {
		for j, ceiling := range dataclass.Ladder {
			want := i > j
			if got := dataclass.Over(class, ceiling); got != want {
				t.Errorf("Over(%s, %s) = %v; want %v", class, ceiling, got, want)
			}
		}
	}
	for _, ceiling := range dataclass.Ladder[:len(dataclass.Ladder)-1] {
		if !dataclass.Over("special_category", ceiling) {
			t.Errorf("special_category not over %s", ceiling)
		}
	}
}

func TestLadderOrder(t *testing.T) {
	want := []string{"public", "internal", "confidential", "personal", "special_category"}
	for i, c := range want {
		if dataclass.Ladder[i] != c {
			t.Fatalf("Ladder[%d] = %s; want %s", i, dataclass.Ladder[i], c)
		}
	}
	if dataclass.Default != "internal" {
		t.Fatalf("Default = %s", dataclass.Default)
	}
	if dataclass.Valid("secret") {
		t.Fatal("unknown class valid")
	}
}
