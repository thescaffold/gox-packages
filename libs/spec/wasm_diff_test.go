package spec

import (
	"encoding/json"
	"fmt"
	"math/rand"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// TestWasmMatchesNative builds the browser build and runs the whole corpus
// through it under Node: every answer must equal the native one byte for byte.
// It skips where Node is not installed, and under -short.
func TestWasmMatchesNative(t *testing.T) {
	if testing.Short() {
		t.Skip("short")
	}
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node not installed")
	}
	goroot, err := exec.Command("go", "env", "GOROOT").Output()
	if err != nil {
		t.Skip("go env: ", err)
	}
	dir := t.TempDir()
	wasm := filepath.Join(dir, "spec.wasm")
	build := exec.Command("go", "build", "-o", wasm, "./wasm")
	build.Env = append(os.Environ(), "GOOS=js", "GOARCH=wasm")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}
	execJs := filepath.Join(string(goroot[:len(goroot)-1]), "lib", "wasm", "wasm_exec.js")
	if _, err := os.Stat(execJs); err != nil {
		t.Skip("wasm_exec.js not found")
	}

	files := corpus(t)
	// Seeded mutations of the corpus: deleted, duplicated and swapped chunks,
	// stray bytes, so the two builds are compared on broken input too.
	rng := rand.New(rand.NewSource(7))
	var seeds []string
	for _, n := range sortedKeys(files) {
		seeds = append(seeds, files[n])
	}
	for i := 0; i < 600; i++ {
		s := []rune(seeds[rng.Intn(len(seeds))])
		for k := rng.Intn(4) + 1; k > 0 && len(s) > 2; k-- {
			a, b := rng.Intn(len(s)), rng.Intn(len(s))
			if a > b {
				a, b = b, a
			}
			switch rng.Intn(4) {
			case 0:
				s = append(s[:a], s[b:]...)
			case 1:
				s = append(s[:b], append(append([]rune{}, s[a:b]...), s[b:]...)...)
			case 2:
				s[a] = []rune("\t\r\v*#`[]{}:-> \u00e9\u2028\x00")[rng.Intn(16)]
			case 3:
				s = append(s[:a], append([]rune("\n\n- "), s[a:]...)...)
			}
		}
		files[fmt.Sprintf("mutation-%03d", i)] = string(s)
	}
	// One large document, for timing: the grocery example repeated.
	files["big"] = strings.Repeat(files[sortedKeys(files)[0]]+"\n\n", 1500)
	names := sortedKeys(files)
	inputs := filepath.Join(dir, "inputs.json")
	enc, _ := json.Marshal(files)
	if err := os.WriteFile(inputs, enc, 0o600); err != nil {
		t.Fatal(err)
	}
	outputs := filepath.Join(dir, "outputs.json")
	cmd := exec.Command(node, "wasm/run.js", execJs, wasm, inputs, outputs)
	cmd.Stderr = os.Stderr // the timings
	if err := cmd.Run(); err != nil {
		t.Fatalf("node: %v", err)
	}
	raw, err := os.ReadFile(outputs)
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]string
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	for _, n := range names {
		if want := string(AnalyzeJSON(files[n])); got[n] != want {
			t.Errorf("%s: browser build differs from native\n native: %.300s\nbrowser: %.300s", n, want, got[n])
		}
	}
	t.Logf("%d documents identical", len(names))
}

func sortedKeys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
