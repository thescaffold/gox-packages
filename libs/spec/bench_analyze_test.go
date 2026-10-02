package spec

import (
	"os"
	"strings"
	"testing"
)

func benchAnalyze(b *testing.B, reps int) {
	src, _ := os.ReadFile("testdata/valid/grocery.ospec")
	text := strings.Repeat(string(src)+"\n\n", reps)
	b.SetBytes(int64(len(text)))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		Analyze(text)
	}
}

func BenchmarkAnalyzeGrocery60x(b *testing.B)  { benchAnalyze(b, 60) }
func BenchmarkAnalyzeGrocery350x(b *testing.B) { benchAnalyze(b, 350) }
func BenchmarkAnalyzeGrocery1500x(b *testing.B) { benchAnalyze(b, 1500) }
