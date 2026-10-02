package spec

import (
	"encoding/json"
	"math/rand"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func corpus(t *testing.T) map[string]string {
	t.Helper()
	out := map[string]string{}
	for _, dir := range []string{"valid", "messy", "invalid", "fuzz"} {
		files, _ := filepath.Glob(filepath.Join("testdata", dir, "*"))
		for _, f := range files {
			if st, err := os.Stat(f); err != nil || st.IsDir() {
				continue
			}
			b, err := os.ReadFile(f)
			if err != nil {
				t.Fatal(err)
			}
			out[f] = string(b)
		}
	}
	if len(out) == 0 {
		t.Fatal("empty corpus")
	}
	return out
}

func TestAnalyzeIsStableAndCanonical(t *testing.T) {
	for name, text := range corpus(t) {
		a := Analyze(text)
		// The canonical text analyzes to itself.
		b := Analyze(a.Canonical)
		if b.Canonical != a.Canonical {
			t.Errorf("%s: canonical text is not a fixed point", name)
		}
		var back Analysis
		if err := json.Unmarshal(AnalyzeJSON(text), &back); err != nil {
			t.Errorf("%s: %v", name, err)
		}
		if back.Canonical != a.Canonical {
			t.Errorf("%s: JSON round trip changed the text", name)
		}
	}
}

// freshNaive is the original way of finding an unused id: count up from -2.
func freshNaive(used map[string]bool, base string) string {
	id := base
	for n := 2; used[id]; n++ {
		id = base + "-" + itoa(n)
	}
	used[id] = true
	return id
}

func TestFreshMatchesTheNaiveSearch(t *testing.T) {
	rng := rand.New(rand.NewSource(3))
	for round := 0; round < 200; round++ {
		a, b := &idSet{used: map[string]bool{}, next: map[string]int{}}, map[string]bool{}
		bases := []string{"a", "b", "a-2", "a-3", "c-1"}
		for i := 0; i < 300; i++ {
			base := bases[rng.Intn(len(bases))]
			if rng.Intn(10) == 0 { // an id that was there already (a typed one)
				id := base + "-" + itoa(rng.Intn(9)+2)
				a.used[id], b[id] = true, true
			}
			if got, want := a.fresh(base), freshNaive(b, base); got != want {
				t.Fatalf("round %d step %d: fresh(%q) = %q, naive gives %q", round, i, base, got, want)
			}
		}
	}
}

func TestManyCopiesOfOneTitleStayFast(t *testing.T) {
	var sb strings.Builder
	sb.WriteString("# T\n\n## Features\n\n")
	for i := 0; i < 4000; i++ {
		sb.WriteString("- **Same name** — and see [[Same name]]\n")
	}
	start := time.Now()
	a := Analyze(sb.String())
	if d := time.Since(start); d > 5*time.Second {
		t.Fatalf("4000 items with one title took %v", d)
	}
	seen := map[string]bool{}
	for _, o := range a.Outline {
		if seen[o.ID] {
			t.Fatalf("id %q handed out twice", o.ID)
		}
		seen[o.ID] = true
	}
	if len(seen) != 4000 {
		t.Fatalf("%d ids for 4000 items", len(seen))
	}
}
