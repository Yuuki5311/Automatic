package captcha

import (
	"math/rand"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestParseTraceFileAndBuildPlan(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sample.txt")
	content := "" +
		"1-100-200-50-鼠标-----\n" +
		"2-100-200-100-鼠标-左键按下---\n" +
		"3-150-205-20-鼠标-----\n" +
		"4-200-210-20-鼠标-----\n" +
		"5-300-215-30-鼠标-左键弹起---\n"
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	trace, err := parseTraceFile(path)
	if err != nil {
		t.Fatalf("parseTraceFile: %v", err)
	}
	if trace.downIndex != 1 || trace.upIndex != 4 {
		t.Fatalf("down=%d up=%d", trace.downIndex, trace.upIndex)
	}
	rng := rand.New(rand.NewSource(1))
	plan, err := trace.buildDragPlan(10, 20, 200, rng)
	if err != nil {
		t.Fatalf("buildDragPlan: %v", err)
	}
	if len(plan.Steps) < 3 {
		t.Fatalf("steps=%d", len(plan.Steps))
	}
	var sawDown, sawUp bool
	for _, s := range plan.Steps {
		if s.Down {
			sawDown = true
			if s.X != 10 || s.Y != 20 {
				t.Fatalf("down point = (%v,%v)", s.X, s.Y)
			}
		}
		if s.Up {
			sawUp = true
		}
	}
	if !sawDown || !sawUp {
		t.Fatalf("down=%v up=%v", sawDown, sawUp)
	}
	if plan.TargetDist < 200 || plan.TargetDist > 208 {
		t.Fatalf("TargetDist=%v", plan.TargetDist)
	}
}

func TestLoadTracesFromRepo(t *testing.T) {
	root := findModuleRoot()
	if root == "" {
		t.Skip("module root not found")
	}
	dir := filepath.Join(root, "browser-cat-slider", "runtime", "data", "cat-slider-traces")
	traces, err := loadTracesFromDirs([]string{dir})
	if err != nil {
		t.Fatalf("loadTracesFromDirs: %v", err)
	}
	if len(traces) < 10 {
		t.Fatalf("expected many traces, got %d", len(traces))
	}
	picked, err := pickTrace(traces, rand.New(rand.NewSource(time.Now().UnixNano())))
	if err != nil {
		t.Fatal(err)
	}
	if picked.Source == "" || len(picked.Points) < 2 {
		t.Fatalf("bad picked trace: %+v", picked)
	}
}

func TestDecodeTraceTextUTF8(t *testing.T) {
	s, err := decodeTraceText([]byte("1-1-1-1-鼠标-左键按下---\n"))
	if err != nil {
		t.Fatal(err)
	}
	if s == "" {
		t.Fatal("empty")
	}
}
