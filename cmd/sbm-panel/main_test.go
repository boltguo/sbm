package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/boltguo/sbm/internal/model"
	"github.com/boltguo/sbm/internal/traffic"
)

func TestLoadAppRulesReadsConfigDirectory(t *testing.T) {
	defer func() {
		if err := traffic.LoadAppRulesFile("../../internal/traffic/app_rules.json"); err != nil {
			t.Fatal(err)
		}
	}()
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.json")
	rules := filepath.Join(dir, "app-rules.json")

	if err := os.WriteFile(rules, []byte(`{"rules":[{"id":"myapp","name":"MyApp","category":"dev","domains":["myapp.example"]}]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	loadAppRules("", configPath)
	if !model.IsKnownAppRule("myapp") || model.IsKnownAppRule("openai") {
		t.Fatal("config apply and serve must load app-rules.json beside the config")
	}

	// A broken file keeps the rules already active instead of failing.
	if err := os.WriteFile(rules, []byte(`{`), 0o600); err != nil {
		t.Fatal(err)
	}
	loadAppRules("", configPath)
	if !model.IsKnownAppRule("myapp") {
		t.Fatal("a broken rule file must not replace the active rules")
	}
}
