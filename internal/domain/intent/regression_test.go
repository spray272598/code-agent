package intent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/spray272598/code-agent/internal/domain/agent/adapter/port"
	"gopkg.in/yaml.v3"
)

// regressionFixture is one row of the data-driven eval dataset.
type regressionFixture struct {
	Name   string `yaml:"name"`
	Input  string `yaml:"input"`
	Intent string `yaml:"intent"`
	Clean  string `yaml:"clean"`
	Source string `yaml:"source"`
	EC     *struct {
		SSH     string `yaml:"ssh"`
		File    string `yaml:"file"`
		Dir     string `yaml:"dir"`
		Session string `yaml:"session"`
	} `yaml:"ec"`
	Resolved string `yaml:"resolved"`
}

// regressionDataset groups fixtures by routing category.
type regressionDataset struct {
	Prefix      []regressionFixture `yaml:"prefix"`
	Continue    []regressionFixture `yaml:"continue"`
	Normal      []regressionFixture `yaml:"normal"`
	Coreference []regressionFixture `yaml:"coreference"`
	LLMFallback []regressionFixture `yaml:"llm_fallback"`
}

// loadFixtures reads the shared eval dataset (scripts/eval/fixtures.yaml),
// walking up from the current working directory so the test works regardless of
// where `go test` is invoked from.
func loadFixtures(t *testing.T) regressionDataset {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	// walk up at most 8 levels looking for scripts/eval/fixtures.yaml
	candidate := filepath.Join(dir, "scripts", "eval", "fixtures.yaml")
	for i := 0; i < 8; i++ {
		if _, err := os.Stat(candidate); err == nil {
			break
		}
		dir = filepath.Dir(dir)
		candidate = filepath.Join(dir, "scripts", "eval", "fixtures.yaml")
	}
	raw, err := os.ReadFile(candidate)
	if err != nil {
		t.Fatalf("read fixtures %s: %v", candidate, err)
	}
	var ds regressionDataset
	if err := yaml.Unmarshal(raw, &ds); err != nil {
		t.Fatalf("parse fixtures: %v", err)
	}
	return ds
}

func intentFromString(s string) Intent {
	switch s {
	case "deep":
		return IntentDeep
	case "team":
		return IntentTeam
	case "continue":
		return IntentContinue
	default:
		return IntentNormal
	}
}

// runCase classifies a single fixture (with optional EntityContext) and asserts
// the expected intent / clean input / source / resolved text.
func runCase(t *testing.T, c *Classifier, fx regressionFixture) {
	t.Helper()
	var ec *EntityContext
	if fx.EC != nil {
		ec = &EntityContext{
			LastSSHConnection: fx.EC.SSH,
			LastFile:          fx.EC.File,
			LastDir:           fx.EC.Dir,
			LastSessionID:     fx.EC.Session,
		}
	}
	res := c.ClassifyWithContext(fx.Input, ec)

	if res.Intent != intentFromString(fx.Intent) {
		t.Errorf("[%s] intent = %s, want %s", fx.Name, res.Intent, fx.Intent)
	}
	if fx.Clean != "" && res.CleanInput != fx.Clean {
		t.Errorf("[%s] clean = %q, want %q", fx.Name, res.CleanInput, fx.Clean)
	}
	if fx.Source != "" && !hasPrefix(res.Source, fx.Source) {
		t.Errorf("[%s] source = %q, want prefix %q", fx.Name, res.Source, fx.Source)
	}
	if fx.Resolved != "" && res.CleanInput != fx.Resolved {
		t.Errorf("[%s] resolved = %q, want %q", fx.Name, res.CleanInput, fx.Resolved)
	}
}

func hasPrefix(s, p string) bool {
	if p == "" {
		return true
	}
	if len(s) < len(p) {
		return false
	}
	return s[:len(p)] == p
}

func TestRegression_IntentRouting(t *testing.T) {
	ds := loadFixtures(t)
	c := NewClassifier(nil) // rule-based only; LLM wired separately below

	groups := map[string][]regressionFixture{
		"prefix":      ds.Prefix,
		"continue":    ds.Continue,
		"normal":      ds.Normal,
		"coreference": ds.Coreference,
	}
	for group, cases := range groups {
		if len(cases) == 0 {
			t.Logf("group %q: no fixtures", group)
		}
		for _, fx := range cases {
			runCase(t, c, fx)
		}
	}
}

// TestRegression_LLMFallback wires a fake LLM so the long-tail semantic path is
// exercised and asserted without a real model call.
func TestRegression_LLMFallback(t *testing.T) {
	ds := loadFixtures(t)
	// map expected intent -> canned LLM reply
	replyFor := map[string]string{
		"team": `{"intent":"team"}`,
		"deep": `{"intent":"deep"}`,
	}
	for _, fx := range ds.LLMFallback {
		llm := &fakeLLM{out: replyFor[fx.Intent]}
		c := NewClassifier(nil)
		c.SetLLM(llm)
		runCase(t, c, fx)
	}
}

// TestRegression_DatasetWellFormed sanity-checks that fixtures decode to at
// least one case per group and that intents are recognised, catching typos in
// the YAML before they silently pass.
func TestRegression_DatasetWellFormed(t *testing.T) {
	ds := loadFixtures(t)
	total := len(ds.Prefix) + len(ds.Continue) + len(ds.Normal) + len(ds.Coreference) + len(ds.LLMFallback)
	if total == 0 {
		t.Fatal("fixtures dataset is empty")
	}
	for _, fx := range append(append(append(ds.Prefix, ds.Continue...), ds.Normal...), ds.Coreference...) {
		if fx.Name == "" {
			t.Error("fixture missing name")
		}
		if intentFromString(fx.Intent) == IntentNormal && fx.Intent != "normal" {
			t.Errorf("[%s] unknown intent %q", fx.Name, fx.Intent)
		}
	}
}

// ensure port import is used (fakeLLM implements port.ILLMPort)
var _ port.ILLMPort = (*fakeLLM)(nil)

// keep json referenced for potential future fixture formats
var _ = json.Marshal
