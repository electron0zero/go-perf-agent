package report

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRender(t *testing.T) {
	dir := t.TempDir()
	runDir := filepath.Join(dir, "runs", "h1")
	if err := os.MkdirAll(runDir, 0o755); err != nil {
		t.Fatal(err)
	}
	verdict := `{"id":"h1","status":"proved","verdict":{"kept":true,"tests_passed":true,` +
		`"delta":"-19.04%","p_value":"0.001","reason":"significant improvement in sec/op",` +
		`"worktree":".go-perf-agent/wt/h1","benchstat":"Foo-8  100n  90n  -19.04%"}}`
	if err := os.WriteFile(filepath.Join(runDir, "verdict.json"), []byte(verdict), 0o644); err != nil {
		t.Fatal(err)
	}

	md, err := Render(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"proved", "h1", "-19.04%", "Proved hypotheses", "Telemetry coverage"} {
		if !strings.Contains(md, want) {
			t.Errorf("report missing %q\n---\n%s", want, md)
		}
	}
	// no profiles/traces collected -> coverage flags the production-telemetry gaps
	if !strings.Contains(md, "production traces") {
		t.Errorf("coverage should flag missing production traces\n%s", md)
	}
}
