package service

import "testing"

func TestParseUpstreamModels(t *testing.T) {
	raw := []byte(`{"code":0,"data":{"models":[{"id":"glm-5.3","name":"GLM-5.3"},{"id":"hy4-preview-f","name":"Hy4 preview"},{"id":"glm-5.3","name":"dup"}]}}`)
	got := parseUpstreamModels(raw)
	if len(got) != 2 {
		t.Fatalf("len=%d want 2", len(got))
	}
	if got[0].ID != "glm-5.3" || got[1].ID != "hy4-preview-f" {
		t.Fatalf("unexpected ids: %#v %#v", got[0], got[1])
	}
}
