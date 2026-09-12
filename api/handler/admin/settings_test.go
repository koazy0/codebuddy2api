package admin

import (
	"strings"
	"testing"

	"codebuddy-gateway/config"
)

func TestValidateCronSpec(t *testing.T) {
	if err := validateCronSpec("0 3 * * *"); err != nil {
		t.Fatalf("valid cron rejected: %v", err)
	}
	if err := validateCronSpec("*/15 * * * *"); err != nil {
		t.Fatalf("interval cron rejected: %v", err)
	}
	if err := validateCronSpec("not-a-cron"); err == nil {
		t.Fatal("invalid cron accepted")
	}
}

func TestPatchYAMLSectionKeepsOtherKeys(t *testing.T) {
	raw := []byte("system:\n    db: sqlite\n    listenAddr: \"0.0.0.0:8088\"\n\nrefresh:\n    enabled: true\n    cron: \"0 3 * * *\"\n    threshold-days: 30\n    timeout-seconds: 15\n\nwatchdog:\n    enabled: true\n")
	cfg := config.Refresh{Enabled: false, Cron: "0 4 * * *", ThresholdDays: 7, TimeoutSeconds: 20}
	out := patchYAMLSection(raw, "refresh", fmtRefreshBody(cfg))
	text := string(out)
	if !strings.Contains(text, "listenAddr: \"0.0.0.0:8088\"") {
		t.Fatalf("lost listenAddr: %s", text)
	}
	if !strings.Contains(text, "cron: \"0 4 * * *\"") {
		t.Fatalf("cron not updated: %s", text)
	}
	if !strings.Contains(text, "enabled: false") {
		t.Fatalf("enabled not updated: %s", text)
	}
	if !strings.Contains(text, "watchdog:") {
		t.Fatalf("lost watchdog: %s", text)
	}
}
