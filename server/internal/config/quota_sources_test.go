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

func TestQuotaSourceAccessGuardSelectionPreservesOverrides(t *testing.T) {
	for _, mode := range []string{"inline", "file"} {
		for _, sourceType := range []string{"cpa", " CPAMP "} {
			for _, tc := range []struct {
				name       string
				fields     string
				wantAll    string
				wantKeys   bool
				wantLength int
			}{
				{name: "inherit"},
				{name: "public all", fields: `,"access_guard_public_all":true`, wantAll: "true"},
				{name: "explicit disabled", fields: `,"access_guard_public_all":false`, wantAll: "false"},
				{name: "explicit empty selection", fields: `,"access_guard_public_keys":[]`, wantKeys: true},
				{name: "selected bindings", fields: `,"access_guard_public_keys":[{"binding_id":" native-key-1 ","name":" 公开显示名 "}]`, wantKeys: true, wantLength: 1},
			} {
				t.Run(mode+"/"+sourceType+"/"+tc.name, func(t *testing.T) {
					prepareConfigTest(t)
					t.Setenv("ACCESS_GUARD_PUBLIC_ALL", "true")
					payload := `[{"type":"` + sourceType + `","base_url":"https://secondary.example.test","admin_key":"private-secondary-key"` + tc.fields + `}]`
					if mode == "inline" {
						t.Setenv("VIEWER_QUOTA_SOURCES", payload)
					} else {
						t.Setenv("VIEWER_QUOTA_SOURCES_FILE", writeConfigFixture(t, payload))
					}
					cfg, err := Load()
					if err != nil {
						t.Fatal(err)
					}
					if !cfg.AccessGuardPublicAll || len(cfg.QuotaSources) != 1 {
						t.Fatal("source selection changed the global selection or source count")
					}
					source := cfg.QuotaSources[0]
					if tc.wantAll == "" {
						if source.AccessGuardPublicAll != nil {
							t.Fatal("missing public-all setting did not remain unspecified")
						}
					} else if source.AccessGuardPublicAll == nil || *source.AccessGuardPublicAll != (tc.wantAll == "true") {
						t.Fatal("explicit public-all setting was lost")
					}
					if (source.AccessGuardPublicKeys != nil) != tc.wantKeys || len(source.AccessGuardPublicKeys) != tc.wantLength {
						t.Fatal("explicit empty selection was confused with an unspecified selection")
					}
					if tc.wantLength == 1 && source.AccessGuardPublicKeys[0] != (AccessGuardPublicKey{BindingID: "native-key-1", Name: "公开显示名"}) {
						t.Fatal("public binding selection was not normalized")
					}
				})
			}
		}
	}
}

func TestQuotaSourceAccessGuardRejectsUnsafeSelection(t *testing.T) {
	for _, tc := range []struct {
		name   string
		fields string
	}{
		{"all null", `"access_guard_public_all":null`},
		{"all string", `"access_guard_public_all":"false"`},
		{"all number", `"access_guard_public_all":0`},
		{"all object", `"access_guard_public_all":{}`},
		{"all array", `"access_guard_public_all":[]`},
		{"both empty", `"access_guard_public_all":false,"access_guard_public_keys":[]`},
		{"both enabled", `"access_guard_public_all":true,"access_guard_public_keys":[]`},
		{"both null", `"access_guard_public_all":null,"access_guard_public_keys":null`},
		{"keys null", `"access_guard_public_keys":null`},
		{"keys object", `"access_guard_public_keys":{}`},
		{"keys wrapped object", `"access_guard_public_keys":{"keys":[]}`},
		{"keys string", `"access_guard_public_keys":"private-selection-secret"`},
		{"keys boolean", `"access_guard_public_keys":false`},
		{"null entry", `"access_guard_public_keys":[null]`},
		{"missing id", `"access_guard_public_keys":[{"name":"private-selection-secret"}]`},
		{"missing name", `"access_guard_public_keys":[{"binding_id":"private-selection-secret"}]`},
		{"empty id", `"access_guard_public_keys":[{"binding_id":" ","name":"private-selection-secret"}]`},
		{"empty name", `"access_guard_public_keys":[{"binding_id":"private-selection-secret","name":" "}]`},
		{"id type", `"access_guard_public_keys":[{"binding_id":5,"name":"private-selection-secret"}]`},
		{"name type", `"access_guard_public_keys":[{"binding_id":"private-selection-secret","name":false}]`},
		{"id control", `"access_guard_public_keys":[{"binding_id":"private-selection-secret\nvalue","name":"label"}]`},
		{"name control", `"access_guard_public_keys":[{"binding_id":"id","name":"private-selection-secret\nvalue"}]`},
		{"long id", `"access_guard_public_keys":[{"binding_id":"` + strings.Repeat("x", 129) + `","name":"private-selection-secret"}]`},
		{"long name", `"access_guard_public_keys":[{"binding_id":"private-selection-secret","name":"` + strings.Repeat("x", 257) + `"}]`},
		{"duplicate ids after trim", `"access_guard_public_keys":[{"binding_id":"id","name":"private-selection-secret"},{"binding_id":" id ","name":"second"}]`},
		{"entry unknown field", `"access_guard_public_keys":[{"binding_id":"id","name":"label","private-selection-secret":"/private-selection-secret/config.json"}]`},
		{"entry duplicate field", `"access_guard_public_keys":[{"binding_id":"private-selection-secret","binding_id":"id","name":"label"}]`},
		{"entry case alias", `"access_guard_public_keys":[{"Binding_ID":"private-selection-secret","name":"label"}]`},
		{"entry limit", `"access_guard_public_keys":[` + strings.Repeat(`{"binding_id":"id","name":"label"},`, 256) + `{"binding_id":"last","name":"private-selection-secret"}]`},
		{"source duplicate all", `"access_guard_public_all":false,"access_guard_public_all":true`},
		{"source duplicate keys", `"access_guard_public_keys":[],"access_guard_public_keys":[]`},
		{"source case alias", `"Access_Guard_Public_All":true`},
		{"source unknown field", `"access_guard_public_all":true,"private-selection-secret":"/private-selection-secret/config.json"`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			prepareConfigTest(t)
			t.Setenv("VIEWER_QUOTA_SOURCES", `[{"type":"cpa","base_url":"https://secondary.example.test","admin_key":"private-selection-secret",`+tc.fields+`}]`)
			_, err := Load()
			if err == nil {
				t.Fatal("accepted invalid public selection")
			}
			if strings.Contains(err.Error(), "private-selection-secret") || strings.Contains(err.Error(), "secondary.example.test") {
				t.Fatal("invalid public selection leaked configuration contents")
			}
		})
	}
}

func TestSub2APIQuotaSourceRejectsAnyAccessGuardSelection(t *testing.T) {
	for _, fields := range []string{
		`"access_guard_public_all":true`,
		`"access_guard_public_all":false`,
		`"access_guard_public_all":null`,
		`"access_guard_public_keys":[]`,
		`"access_guard_public_keys":null`,
		`"access_guard_public_keys":[{"binding_id":"private-selection-secret","name":"label"}]`,
		`"access_guard_public_all":false,"access_guard_public_keys":[]`,
	} {
		prepareConfigTest(t)
		t.Setenv("VIEWER_QUOTA_SOURCES", `[{"type":"sub2api","base_url":"https://sub.example.test","admin_api_key":"private-selection-secret",`+fields+`}]`)
		_, err := Load()
		if err == nil || !strings.Contains(err.Error(), "only supported for CPA sources") {
			t.Fatal("Sub2API public selection was not rejected with a safe source-type error")
		}
		if strings.Contains(err.Error(), "private-selection-secret") {
			t.Fatal("Sub2API public selection error leaked configuration contents")
		}
	}
}
