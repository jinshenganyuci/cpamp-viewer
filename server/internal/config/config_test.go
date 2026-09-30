package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func prepareConfigTest(t *testing.T) {
	t.Helper()
	t.Setenv("SUB2API_BASE_URL", "")
	t.Setenv("SUB2API_ADMIN_API_KEY", "")
	t.Setenv("SUB2API_ADMIN_API_KEY_FILE", "")
	t.Setenv("SUB2API_ADMIN_KEY", "")
	t.Setenv("SUB2API_ADMIN_KEY_FILE", "")
	t.Setenv("SUB2API_ADMIN_JWT", "")
	t.Setenv("SUB2API_ADMIN_JWT_FILE", "")
	t.Setenv("CPAMP_ADMIN_KEY", "test-admin-key")
	t.Setenv("CPAMP_ADMIN_KEY_FILE", "")
	t.Setenv("VIEWER_PASSWORD", "")
	t.Setenv("VIEWER_PASSWORD_FILE", "")
	t.Setenv("VIEWER_SESSION_SECRET", "01234567890123456789012345678901")
	t.Setenv("VIEWER_SESSION_SECRET_FILE", "")
	t.Setenv("VIEWER_PUBLIC_ACCESS", "true")
	t.Setenv("ACCESS_GUARD_BASE_URL", "")
	t.Setenv("ACCESS_GUARD_MANAGEMENT_KEY", "")
	t.Setenv("ACCESS_GUARD_MANAGEMENT_KEY_FILE", "")
	t.Setenv("ACCESS_GUARD_PUBLIC_ALL", "")
	t.Setenv("ACCESS_GUARD_PUBLIC_KEYS", "")
	t.Setenv("ACCESS_GUARD_PUBLIC_KEYS_FILE", "")
}

func TestLoadSub2APIRequiresDedicatedCredential(t *testing.T) {
	prepareConfigTest(t)
	t.Setenv("SUB2API_BASE_URL", "https://sub2api.example.test/prefix/")
	if _, err := Load(); err == nil || !strings.Contains(err.Error(), "SUB2API_ADMIN_API_KEY") {
		t.Fatalf("expected missing Sub2API credential error, got %v", err)
	}
	t.Setenv("SUB2API_ADMIN_API_KEY", "test-sub2api-admin-key")
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Sub2APIAdminAPIKey != "test-sub2api-admin-key" || cfg.CPAMPAdminKey != "test-admin-key" || cfg.Sub2APIBaseURL != "https://sub2api.example.test/prefix" {
		t.Fatalf("unexpected Sub2API configuration: %#v", cfg)
	}
}

func TestLoadSub2APIRejectsAmbiguousURL(t *testing.T) {
	prepareConfigTest(t)
	t.Setenv("SUB2API_ADMIN_JWT", "test-admin-jwt")
	for _, value := range []string{"https://user:pass@example.test", "https://example.test/path?x=1", "https://example.test/#fragment", "https://example.test/../admin", "https://example.test\\evil.test"} {
		t.Setenv("SUB2API_BASE_URL", value)
		if _, err := Load(); err == nil {
			t.Fatalf("accepted invalid Sub2API URL %q", value)
		}
	}
}

func TestLoadRejectsSub2APISecretWithoutURL(t *testing.T) {
	prepareConfigTest(t)
	t.Setenv("SUB2API_ADMIN_API_KEY_FILE", "/missing/sub2api-key")
	if _, err := Load(); err == nil || !strings.Contains(err.Error(), "SUB2API_BASE_URL") {
		t.Fatalf("expected missing Sub2API URL error, got %v", err)
	}
}

func writeConfigFixture(t *testing.T, data string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "private-config.json")
	if err := os.WriteFile(path, []byte(data), 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

func prepareAccessGuardConfig(t *testing.T, data string) {
	t.Helper()
	prepareConfigTest(t)
	t.Setenv("ACCESS_GUARD_BASE_URL", "https://guard.example.test/cpa/")
	t.Setenv("ACCESS_GUARD_MANAGEMENT_KEY", "private-management-secret")
	t.Setenv("ACCESS_GUARD_PUBLIC_KEYS_FILE", writeConfigFixture(t, data))
}

func TestLoadAccessGuardDisabledByDefault(t *testing.T) {
	prepareConfigTest(t)
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.AccessGuardBaseURL != "" || cfg.AccessGuardManagementKey != "" || cfg.AccessGuardViaCPAMP || cfg.AccessGuardPublicAll || len(cfg.AccessGuardPublicKeys) != 0 {
		t.Fatal("Access Guard should be disabled without configuration")
	}
}

func TestLoadAccessGuardCompleteConfig(t *testing.T) {
	prepareAccessGuardConfig(t, `{"keys":[{"binding_id":" native-key-1 ","name":" 公开显示名 "}]}`)
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.AccessGuardBaseURL != "https://guard.example.test/cpa" || cfg.AccessGuardManagementKey != "private-management-secret" || cfg.AccessGuardViaCPAMP || cfg.AccessGuardPublicAll {
		t.Fatal("Access Guard base URL or management credential was not loaded")
	}
	if len(cfg.AccessGuardPublicKeys) != 1 || cfg.AccessGuardPublicKeys[0].BindingID != "native-key-1" || cfg.AccessGuardPublicKeys[0].Name != "公开显示名" {
		t.Fatalf("unexpected public key projection configuration: %#v", cfg.AccessGuardPublicKeys)
	}
}

func TestLoadAccessGuardEmptyPublicKeys(t *testing.T) {
	prepareAccessGuardConfig(t, `{"keys":[]}`)
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.AccessGuardBaseURL == "" || cfg.AccessGuardViaCPAMP || cfg.AccessGuardPublicAll || cfg.AccessGuardPublicKeys == nil || len(cfg.AccessGuardPublicKeys) != 0 {
		t.Fatal("an empty public array should enable integration with no public entries")
	}
}

func TestLoadAccessGuardPublicAllUsesExistingCPAMPConfiguration(t *testing.T) {
	prepareConfigTest(t)
	t.Setenv("CPAMP_BASE_URL", "https://cpamp.example.test")
	t.Setenv("ACCESS_GUARD_PUBLIC_ALL", "true")
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.AccessGuardViaCPAMP || !cfg.AccessGuardPublicAll || len(cfg.AccessGuardPublicKeys) != 0 {
		t.Fatal("explicit public-all should select the CPAMP proxy without a public-key file")
	}
	if cfg.AccessGuardBaseURL != "" || cfg.AccessGuardManagementKey != "" {
		t.Fatal("CPAMP mode must not copy its credential or URL into the independent CPA connection")
	}
	if cfg.CPAMPBaseURL != "https://cpamp.example.test" || cfg.CPAMPAdminKey != "test-admin-key" {
		t.Fatal("existing CPAMP connection was changed")
	}
}

func TestLoadAccessGuardPublicAllFalseDoesNotEnablePublication(t *testing.T) {
	for _, value := range []string{"", "false", " false "} {
		t.Run(value, func(t *testing.T) {
			prepareConfigTest(t)
			t.Setenv("ACCESS_GUARD_PUBLIC_ALL", value)
			cfg, err := Load()
			if err != nil {
				t.Fatal(err)
			}
			if cfg.AccessGuardPublicAll || cfg.AccessGuardViaCPAMP || cfg.AccessGuardBaseURL != "" || len(cfg.AccessGuardPublicKeys) != 0 {
				t.Fatal("absent or false public-all must not enable publication")
			}
		})
	}
}

func TestLoadAccessGuardPublicAllRejectsAmbiguousBooleans(t *testing.T) {
	for _, value := range []string{"1", "0", "yes", "no", "on", "off", "TRUE", "False", "tru", "null", "private-invalid-value"} {
		t.Run(value, func(t *testing.T) {
			prepareConfigTest(t)
			t.Setenv("ACCESS_GUARD_PUBLIC_ALL", value)
			_, err := Load()
			if err == nil {
				t.Fatal("invalid public-all must fail instead of changing the public selection")
			}
			if err.Error() != "ACCESS_GUARD_PUBLIC_ALL must be true or false" {
				t.Fatal("boolean error must identify the setting without echoing its value")
			}
		})
	}
}

func TestLoadAccessGuardInlineAndFileSelectionsCanUseCPAMP(t *testing.T) {
	for _, source := range []string{"inline", "file"} {
		for _, empty := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s empty=%t", source, empty), func(t *testing.T) {
				prepareConfigTest(t)
				t.Setenv("ACCESS_GUARD_PUBLIC_ALL", "false")
				data := `{"keys":[{"binding_id":" native-key-1 ","name":" 公开名称 "}]}`
				if empty {
					data = `{"keys":[]}`
				}
				if source == "inline" {
					t.Setenv("ACCESS_GUARD_PUBLIC_KEYS", data)
				} else {
					t.Setenv("ACCESS_GUARD_PUBLIC_KEYS_FILE", writeConfigFixture(t, data))
				}
				cfg, err := Load()
				if err != nil {
					t.Fatal(err)
				}
				if !cfg.AccessGuardViaCPAMP || cfg.AccessGuardPublicAll || cfg.AccessGuardBaseURL != "" || cfg.AccessGuardManagementKey != "" {
					t.Fatal("a selection without an independent connection should use CPAMP and retain its scope")
				}
				if cfg.AccessGuardPublicKeys == nil {
					t.Fatal("an explicit empty selection must remain distinguishable from no selection")
				}
				if empty {
					if len(cfg.AccessGuardPublicKeys) != 0 {
						t.Fatal("empty selection must not become public-all")
					}
				} else if len(cfg.AccessGuardPublicKeys) != 1 || cfg.AccessGuardPublicKeys[0].BindingID != "native-key-1" || cfg.AccessGuardPublicKeys[0].Name != "公开名称" {
					t.Fatal("public selection was not loaded and trimmed")
				}
			})
		}
	}
}

func TestLoadAccessGuardIndependentConnectionSupportsExplicitSelections(t *testing.T) {
	for _, source := range []string{"all", "inline"} {
		t.Run(source, func(t *testing.T) {
			prepareConfigTest(t)
			t.Setenv("ACCESS_GUARD_BASE_URL", "https://guard.example.test")
			t.Setenv("ACCESS_GUARD_MANAGEMENT_KEY", "independent-management-secret")
			if source == "all" {
				t.Setenv("ACCESS_GUARD_PUBLIC_ALL", "true")
			} else {
				t.Setenv("ACCESS_GUARD_PUBLIC_KEYS", `{"keys":[]}`)
			}
			cfg, err := Load()
			if err != nil {
				t.Fatal(err)
			}
			if cfg.AccessGuardViaCPAMP || cfg.AccessGuardPublicAll != (source == "all") {
				t.Fatal("explicit independent connection must not silently use CPAMP or change publication scope")
			}
			if cfg.AccessGuardBaseURL != "https://guard.example.test" || cfg.AccessGuardManagementKey != "independent-management-secret" {
				t.Fatal("independent connection settings were not retained")
			}
		})
	}
}

func TestLoadAccessGuardRejectsConflictingPublicSelections(t *testing.T) {
	for _, sources := range [][]string{{"all", "inline"}, {"all", "file"}, {"inline", "file"}, {"all", "inline", "file"}} {
		t.Run(strings.Join(sources, "+"), func(t *testing.T) {
			prepareConfigTest(t)
			for _, source := range sources {
				switch source {
				case "all":
					t.Setenv("ACCESS_GUARD_PUBLIC_ALL", "true")
				case "inline":
					t.Setenv("ACCESS_GUARD_PUBLIC_KEYS", `{"keys":[]}`)
				case "file":
					t.Setenv("ACCESS_GUARD_PUBLIC_KEYS_FILE", writeConfigFixture(t, `{"keys":[]}`))
				}
			}
			if _, err := Load(); err == nil {
				t.Fatal("conflicting public selections must fail without silently widening the scope")
			}
		})
	}
}

func TestLoadAccessGuardPartialConnectionDoesNotFallBackToCPAMP(t *testing.T) {
	for _, field := range []string{"ACCESS_GUARD_BASE_URL", "ACCESS_GUARD_MANAGEMENT_KEY", "ACCESS_GUARD_MANAGEMENT_KEY_FILE"} {
		t.Run(field, func(t *testing.T) {
			prepareConfigTest(t)
			t.Setenv("ACCESS_GUARD_PUBLIC_ALL", "true")
			value := "private-incomplete-setting"
			if field == "ACCESS_GUARD_BASE_URL" {
				value = "https://guard.example.test"
			}
			t.Setenv(field, value)
			if _, err := Load(); err == nil {
				t.Fatal("partial independent connection must fail instead of falling back to CPAMP")
			}
		})
	}
}

func TestLoadAccessGuardConnectionWithoutSelectionDoesNotPublishAll(t *testing.T) {
	prepareConfigTest(t)
	t.Setenv("ACCESS_GUARD_PUBLIC_ALL", "false")
	t.Setenv("ACCESS_GUARD_BASE_URL", "https://guard.example.test")
	t.Setenv("ACCESS_GUARD_MANAGEMENT_KEY", "independent-management-secret")
	if _, err := Load(); err == nil {
		t.Fatal("an independent connection without a public selection must not publish all bindings")
	}
}

func TestLoadAccessGuardRequiresCompleteConfiguration(t *testing.T) {
	for _, missing := range []string{"ACCESS_GUARD_BASE_URL", "ACCESS_GUARD_MANAGEMENT_KEY", "ACCESS_GUARD_PUBLIC_KEYS_FILE"} {
		t.Run(missing, func(t *testing.T) {
			prepareAccessGuardConfig(t, `{"keys":[]}`)
			t.Setenv(missing, "")
			if _, err := Load(); err == nil {
				t.Fatal("expected incomplete Access Guard configuration to fail")
			}
		})
	}
	t.Run("secret file only", func(t *testing.T) {
		prepareConfigTest(t)
		t.Setenv("ACCESS_GUARD_MANAGEMENT_KEY_FILE", "/private/nonexistent-secret")
		if _, err := Load(); err == nil {
			t.Fatal("a secret file setting alone should fail")
		}
	})
}

func TestLoadAccessGuardManagementKeyFile(t *testing.T) {
	prepareAccessGuardConfig(t, `{"keys":[]}`)
	t.Setenv("ACCESS_GUARD_MANAGEMENT_KEY", "")
	t.Setenv("ACCESS_GUARD_MANAGEMENT_KEY_FILE", writeConfigFixture(t, " private-file-secret\n"))
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.AccessGuardManagementKey != "private-file-secret" {
		t.Fatal("management credential was not read and trimmed from its file")
	}
}

func TestLoadAccessGuardRejectsInvalidURL(t *testing.T) {
	for _, value := range []string{
		"ftp://guard.example.test", "https:///missing-host", "https://:80",
		"https://user:secret@guard.example.test", "https://guard.example.test?secret=value",
		"https://guard.example.test?", "https://guard.example.test#fragment", "https://guard.example.test#",
		"https://guard.example.test/a/../b", "https://guard.example.test/./cpa", "https://guard.example.test//cpa",
		"https://guard.example.test/%2e%2e/private", "https://guard.example.test/%252e%252e/private",
		"https://guard.example.test/cpa%2fprivate", "https://guard.example.test/cpa\\private",
		"https://guard.example.test:", "https://guard.example.test:0", "https://guard.example.test:65536",
	} {
		t.Run(value, func(t *testing.T) {
			prepareAccessGuardConfig(t, `{"keys":[]}`)
			t.Setenv("ACCESS_GUARD_BASE_URL", value)
			if _, err := Load(); err == nil {
				t.Fatal("expected URL rejection")
			} else if strings.Contains(err.Error(), value) {
				t.Fatal("URL configuration error leaked the configured value")
			}
		})
	}
}

func TestLoadAccessGuardAcceptsSupportedURLs(t *testing.T) {
	for _, value := range []string{"http://localhost:8317", "https://guard.example.test", "http://[::1]:8317/cpa/"} {
		t.Run(value, func(t *testing.T) {
			prepareAccessGuardConfig(t, `{"keys":[]}`)
			t.Setenv("ACCESS_GUARD_BASE_URL", value)
			cfg, err := Load()
			if err != nil {
				t.Fatal(err)
			}
			if cfg.AccessGuardBaseURL != strings.TrimRight(value, "/") {
				t.Fatal("unexpected normalized base URL")
			}
		})
	}
}

func TestLoadAccessGuardRejectsInvalidPublicKeys(t *testing.T) {
	for name, data := range map[string]string{
		"missing array":           `{}`,
		"null document":           `null`,
		"null array":              `{"keys":null}`,
		"wrong array type":        `{"keys":{}}`,
		"duplicate top field":     `{"keys":[],"keys":[]}`,
		"duplicate entry field":   `{"keys":[{"binding_id":"native-key-1","name":"name","name":"other"}]}`,
		"uppercase top field":     `{"KEYS":[]}`,
		"uppercase entry field":   `{"keys":[{"BINDING_ID":"native-key-1","name":"name"}]}`,
		"unknown top field":       `{"keys":[],"private-unknown-field":"hidden"}`,
		"unknown entry field":     `{"keys":[{"binding_id":"native-key-1","name":"name","secret":"hidden"}]}`,
		"duplicate ID":            `{"keys":[{"binding_id":"native-key-1","name":"one"},{"binding_id":" native-key-1 ","name":"two"}]}`,
		"missing name":            `{"keys":[{"binding_id":"native-key-1"}]}`,
		"empty name":              `{"keys":[{"binding_id":"native-key-1","name":"  "}]}`,
		"empty ID":                `{"keys":[{"binding_id":" ","name":"name"}]}`,
		"null entry":              `{"keys":[null]}`,
		"null name":               `{"keys":[{"binding_id":"native-key-1","name":null}]}`,
		"wrong ID type":           `{"keys":[{"binding_id":1,"name":"name"}]}`,
		"trailing document":       `{"keys":[]} {"private":"secret"}`,
		"trailing malformed text": `{"keys":[]} private-secret`,
		"malformed":               `{"keys":[private-secret]}`,
		"control characters":      `{"keys":[{"binding_id":"native-key-1","name":"name\nprivate"}]}`,
		"long ID":                 fmt.Sprintf(`{"keys":[{"binding_id":%q,"name":"name"}]}`, strings.Repeat("a", 129)),
		"long name":               fmt.Sprintf(`{"keys":[{"binding_id":"native-key-1","name":%q}]}`, strings.Repeat("a", 257)),
		"invalid UTF-8":           "{\"keys\":[{\"binding_id\":\"native-key-1\",\"name\":\"\xff\"}]}",
	} {
		t.Run(name, func(t *testing.T) {
			for _, source := range []string{"file", "inline"} {
				t.Run(source, func(t *testing.T) {
					if source == "file" {
						prepareAccessGuardConfig(t, data)
					} else {
						prepareConfigTest(t)
						t.Setenv("ACCESS_GUARD_PUBLIC_KEYS", data)
					}
					if _, err := Load(); err == nil {
						t.Fatal("expected public configuration rejection")
					} else if strings.Contains(err.Error(), "private-secret") || strings.Contains(err.Error(), "hidden") || strings.Contains(err.Error(), "private-unknown-field") || strings.Contains(err.Error(), data) {
						t.Fatal("public configuration error leaked its contents")
					}
				})
			}
		})
	}
}

func TestLoadAccessGuardEnforcesPublicFileAndEntryLimits(t *testing.T) {
	entries := make([]string, 257)
	for i := range entries {
		entries[i] = fmt.Sprintf(`{"binding_id":"native-key-%d","name":"name"}`, i)
	}
	for name, data := range map[string]string{
		"size":        `{"keys":[]}` + strings.Repeat(" ", 64<<10),
		"entry count": `{"keys":[` + strings.Join(entries, ",") + `]}`,
	} {
		for _, source := range []string{"file", "inline"} {
			t.Run(name+" "+source, func(t *testing.T) {
				if source == "file" {
					prepareAccessGuardConfig(t, data)
				} else {
					prepareConfigTest(t)
					t.Setenv("ACCESS_GUARD_PUBLIC_KEYS", data)
				}
				if _, err := Load(); err == nil {
					t.Fatal("oversized public configuration must fail for both inline and file sources")
				}
			})
		}
	}
}

func TestLoadAccessGuardConfigurationErrorsDoNotLeakSecretsOrPaths(t *testing.T) {
	for _, field := range []string{"ACCESS_GUARD_MANAGEMENT_KEY_FILE", "ACCESS_GUARD_PUBLIC_KEYS_FILE"} {
		t.Run(field, func(t *testing.T) {
			prepareAccessGuardConfig(t, `{"keys":[]}`)
			privatePath := filepath.Join(t.TempDir(), "sensitive-private-path-does-not-exist")
			if field == "ACCESS_GUARD_MANAGEMENT_KEY_FILE" {
				t.Setenv("ACCESS_GUARD_MANAGEMENT_KEY", "")
			}
			t.Setenv(field, privatePath)
			_, err := Load()
			if err == nil {
				t.Fatal("expected missing configuration file to fail")
			}
			if strings.Contains(err.Error(), privatePath) || strings.Contains(err.Error(), "sensitive-private-path") || strings.Contains(err.Error(), "private-management-secret") {
				t.Fatal("configuration error leaked a private path or secret")
			}
		})
	}
	for name, data := range map[string]string{"empty": " \n", "oversized": strings.Repeat("private-secret", 2000), "header newline": "private-secret\r\ninjected"} {
		t.Run(name, func(t *testing.T) {
			prepareAccessGuardConfig(t, `{"keys":[]}`)
			t.Setenv("ACCESS_GUARD_MANAGEMENT_KEY", "")
			t.Setenv("ACCESS_GUARD_MANAGEMENT_KEY_FILE", writeConfigFixture(t, data))
			_, err := Load()
			if err == nil {
				t.Fatal("expected invalid credential file to fail")
			}
			if strings.Contains(err.Error(), "private-secret") || strings.Contains(err.Error(), "injected") {
				t.Fatal("configuration error leaked a credential")
			}
		})
	}
}

func TestLoadDefaultsToPublicAccessWithoutViewerPassword(t *testing.T) {
	prepareConfigTest(t)
	t.Setenv("VIEWER_PUBLIC_ACCESS", "")

	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.PublicAccess {
		t.Fatal("public access should be enabled by default")
	}
	if cfg.ViewerPassword != "" {
		t.Fatal("public mode unexpectedly retained a Viewer password")
	}
}

func TestLoadPublicAccessIgnoresLegacyPasswordFile(t *testing.T) {
	prepareConfigTest(t)
	t.Setenv("VIEWER_PUBLIC_ACCESS", "true")
	t.Setenv("VIEWER_PASSWORD_FILE", "/path/that/does/not/exist")

	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.PublicAccess {
		t.Fatal("public access was not enabled")
	}
}

func TestLoadProtectedModeStillRequiresViewerPassword(t *testing.T) {
	prepareConfigTest(t)
	t.Setenv("VIEWER_PUBLIC_ACCESS", "false")

	_, err := Load()
	if err == nil || !strings.Contains(err.Error(), "VIEWER_PASSWORD") {
		t.Fatalf("expected missing Viewer password error, got %v", err)
	}

	t.Setenv("VIEWER_PASSWORD", "viewer-password")
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.PublicAccess {
		t.Fatal("protected mode unexpectedly enabled public access")
	}
}
