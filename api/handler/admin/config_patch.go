package admin

import (
	"fmt"
	"os"
	"regexp"
	"strings"

	"codebuddy-gateway/global"
)

func configFilePath() string {
	if global.CORE_VP != nil {
		if used := strings.TrimSpace(global.CORE_VP.ConfigFileUsed()); used != "" {
			return used
		}
	}
	return "config.yaml"
}

func patchYAMLSection(raw []byte, name, body string) []byte {
	re := regexp.MustCompile("(?m)^" + regexp.QuoteMeta(name) + ":\\n(?:[ \\t].*\\n)*")
	block := name + ":\n" + body
	if !strings.HasSuffix(block, "\n") {
		block += "\n"
	}
	text := string(raw)
	if !strings.HasSuffix(text, "\n") {
		text += "\n"
	}
	if loc := re.FindStringIndex(text); loc != nil {
		return []byte(text[:loc[0]] + block + text[loc[1]:])
	}
	return []byte(text + "\n" + block)
}

func writeYAMLSection(name, body string) error {
	path := configFilePath()
	raw, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read config: %w", err)
	}
	patched := patchYAMLSection(raw, name, body)
	if err := os.WriteFile(path, patched, 0644); err != nil {
		return fmt.Errorf("write config: %w", err)
	}
	return nil
}
