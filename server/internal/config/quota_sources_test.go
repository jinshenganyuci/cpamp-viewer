package config

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestLoadMultipleQuotaSources(t *testing.T) {
	for _, mode := range []string{"inline", "file"} {
		t.Run(mode, func(t *testing.T) {
			prepareConfigTest(t)
			t.Setenv("CPAMP_BASE_URL", "https://primary.example.test")
			keyFile := writeConfigFixture(t, " private-secondary-key\n")
			inputs := []quotaSourceInput{
				{Type: "cpa", Name: " CPA 二号 ", BaseURL: "https://cpa.example.test/", AdminKeyFile: keyFile},
				{Type: "sub2api", BaseURL: "https://sub-one.example.test", AdminAPIKey: "private-sub-one"},
				{Type: "sub2api", Name: "Sub 二号", BaseURL: "https://sub-two.example.test", AdminJWT: "private-sub-two"},
			}
			encoded, err := json.Marshal(inputs)
			if err != nil {
				t.Fatal(err)
			}
			if mode == "inline" {
				t.Setenv("VIEWER_QUOTA_SOURCES", string(encoded))
			} else {
				t.Setenv("VIEWER_QUOTA_SOURCES_FILE", writeConfigFixture(t, string(encoded)))
			}
			cfg, err := Load()
			if err != nil {
				t.Fatal(err)
			}
			if len(cfg.QuotaSources) != 3 || cfg.QuotaSources[0].Type != "cpamp" || cfg.QuotaSources[0].Name != "CPA 二号" || cfg.QuotaSources[0].BaseURL != "https://cpa.example.test" || cfg.QuotaSources[0].AdminKey != "private-secondary-key" {
				t.Fatal("CPA source was not loaded independently")
			}
			if cfg.CPAMPAdminKey != "test-admin-key" || cfg.QuotaSources[1].AdminAPIKey != "private-sub-one" || cfg.QuotaSources[2].AdminJWT != "private-sub-two" || cfg.QuotaSources[1].Name == "" {
				t.Fatal("source credentials or default names were mixed")
			}
		})
	}
}

func TestQuotaSourcesRejectInvalidConfigurationWithoutLeakingSecrets(t *testing.T) {
	for _, payload := range []string{
		`null`, `{}`, `[null]`, `[] []`, `[{"type":"private-secret"}]`,
		`[{"type":"cpa","base_url":"https://user:private-secret@example.test","admin_key":"private-secret"}]`,
		`[{"type":"cpa","base_url":"https://example.test/?private-secret","admin_key":"private-secret"}]`,
		`[{"type":"cpa","base_url":"https://example.test"}]`,
		`[{"type":"cpa","base_url":"https://example.test","admin_key":"private-secret","admin_key_file":"/private-secret"}]`,
		`[{"type":"cpa","base_url":"https://example.test","admin_key_file":"/private-secret"}]`,
		`[{"type":"cpa","base_url":"https://example.test","admin_api_key":"private-secret"}]`,
		`[{"type":"sub2api","base_url":"https://example.test","admin_key":"private-secret"}]`,
		`[{"type":"sub2api","base_url":"https://example.test"}]`,
		`[{"type":"sub2api","base_url":"https://example.test","admin_api_key":"private-secret","admin_jwt":"private-secret"}]`,
		`[{"type":"cpa","base_url":"https://example.test","admin_key":"private-secret\nvalue"}]`,
		`[{"type":"cpa","name":"private-secret\nvalue","base_url":"https://example.test","admin_key":"private-secret"}]`,
		`[{"type":"cpa","base_url":"https://example.test","admin_key":"private-secret","typo":"private-secret"}]`,
	} {
		t.Run(payload, func(t *testing.T) {
			prepareConfigTest(t)
			t.Setenv("VIEWER_QUOTA_SOURCES", payload)
			_, err := Load()
			if err == nil || strings.Contains(err.Error(), "private-secret") {
				t.Fatalf("invalid config was accepted or leaked its contents: %v", err)
			}
		})
	}
}

func TestQuotaSourcesRejectDuplicateEndpoints(t *testing.T) {
	for _, payload := range []string{
		`[{"type":"cpa","base_url":"https://PRIMARY.example.test:443/","admin_key":"key"}]`,
		`[{"type":"sub2api","base_url":"https://legacy-sub.example.test/","admin_api_key":"key"}]`,
		`[{"type":"cpa","base_url":"https://extra.example.test","admin_key":"one"},{"type":"cpamp","base_url":"https://extra.example.test/","admin_key":"two"}]`,
	} {
		prepareConfigTest(t)
		t.Setenv("CPAMP_BASE_URL", "https://primary.example.test")
		t.Setenv("SUB2API_BASE_URL", "https://legacy-sub.example.test")
		t.Setenv("SUB2API_ADMIN_API_KEY", "legacy-key")
		t.Setenv("VIEWER_QUOTA_SOURCES", payload)
		if _, err := Load(); err == nil || !strings.Contains(err.Error(), "duplicates") {
			t.Fatalf("duplicate source was not rejected: %v", err)
		}
	}
}

func TestQuotaSourcesRejectAmbiguousOrOversizedInput(t *testing.T) {
	prepareConfigTest(t)
	t.Setenv("VIEWER_QUOTA_SOURCES", "[]")
	t.Setenv("VIEWER_QUOTA_SOURCES_FILE", writeConfigFixture(t, "[]"))
	if _, err := Load(); err == nil {
		t.Fatal("accepted inline and file settings together")
	}
	t.Setenv("VIEWER_QUOTA_SOURCES_FILE", "")
	t.Setenv("VIEWER_QUOTA_SOURCES", "["+strings.Repeat("{} ,", maxQuotaSources)+"{}]")
	if _, err := Load(); err == nil || !strings.Contains(err.Error(), "limit") {
		t.Fatalf("accepted too many sources: %v", err)
	}
	for _, mode := range []string{"inline", "file"} {
		t.Setenv("VIEWER_QUOTA_SOURCES", "")
		t.Setenv("VIEWER_QUOTA_SOURCES_FILE", "")
		oversized := strings.Repeat(" ", 64<<10) + "[]"
		if mode == "inline" {
			oversized = `[{"name":"` + strings.Repeat("x", 64<<10) + `"}]`
			t.Setenv("VIEWER_QUOTA_SOURCES", oversized)
		} else {
			t.Setenv("VIEWER_QUOTA_SOURCES_FILE", writeConfigFixture(t, oversized))
		}
		if _, err := Load(); err == nil {
			t.Fatal("accepted oversized source configuration")
		}
	}
}
