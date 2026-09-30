package config

import (
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

type AccessGuardPublicKey struct {
	BindingID string `json:"binding_id"`
	Name      string `json:"name"`
}

type Config struct {
	HTTPAddr                 string
	CPAMPBaseURL             string
	CPAMPAdminKey            string
	Sub2APIBaseURL           string
	Sub2APIAdminAPIKey       string
	Sub2APIAdminJWT          string
	PublicAccess             bool
	ViewerPassword           string
	SessionSecret            []byte
	SessionTTL               time.Duration
	SecureCookies            bool
	RequestTimeout           time.Duration
	MaxUpstreamBodyBytes     int64
	MaxAnalyticsRange        time.Duration
	MaxEventsPage            int
	AccessGuardBaseURL       string
	AccessGuardManagementKey string
	AccessGuardViaCPAMP      bool
	AccessGuardPublicAll     bool
	AccessGuardPublicKeys    []AccessGuardPublicKey
}

func Load() (Config, error) {
	adminKey, err := readSecret("CPAMP_ADMIN_KEY", "CPAMP_ADMIN_KEY_FILE")
	if err != nil {
		return Config{}, err
	}
	if adminKey == "" {
		return Config{}, errors.New("CPAMP_ADMIN_KEY or CPAMP_ADMIN_KEY_FILE is required")
	}
	sub2APIBaseURL := strings.TrimSpace(os.Getenv("SUB2API_BASE_URL"))
	var sub2APIAdminAPIKey, sub2APIAdminJWT string
	if sub2APIBaseURL != "" {
		sub2APIBaseURL, err = validateServiceURL(sub2APIBaseURL, "SUB2API_BASE_URL")
		if err != nil {
			return Config{}, err
		}
		sub2APIAdminAPIKey, sub2APIAdminJWT, err = loadSub2APICredentials()
		if err != nil {
			return Config{}, err
		}
		if sub2APIAdminAPIKey == "" && sub2APIAdminJWT == "" {
			return Config{}, errors.New("SUB2API_ADMIN_API_KEY, SUB2API_ADMIN_API_KEY_FILE, or SUB2API_ADMIN_JWT is required when SUB2API_BASE_URL is set")
		}
	} else if strings.TrimSpace(os.Getenv("SUB2API_ADMIN_API_KEY")) != "" || strings.TrimSpace(os.Getenv("SUB2API_ADMIN_API_KEY_FILE")) != "" || strings.TrimSpace(os.Getenv("SUB2API_ADMIN_KEY")) != "" || strings.TrimSpace(os.Getenv("SUB2API_ADMIN_KEY_FILE")) != "" || strings.TrimSpace(os.Getenv("SUB2API_ADMIN_JWT")) != "" || strings.TrimSpace(os.Getenv("SUB2API_ADMIN_JWT_FILE")) != "" {
		return Config{}, errors.New("SUB2API_BASE_URL is required when Sub2API credentials are configured")
	}

	publicAccess := boolEnv("VIEWER_PUBLIC_ACCESS", true)
	var viewerPassword string
	if !publicAccess {
		viewerPassword, err = readSecret("VIEWER_PASSWORD", "VIEWER_PASSWORD_FILE")
		if err != nil {
			return Config{}, err
		}
		if viewerPassword == "" {
			return Config{}, errors.New("VIEWER_PASSWORD or VIEWER_PASSWORD_FILE is required when VIEWER_PUBLIC_ACCESS=false")
		}
		if len(viewerPassword) < 10 {
			return Config{}, errors.New("VIEWER_PASSWORD must contain at least 10 characters")
		}
	}

	sessionSecretRaw, err := readSecret("VIEWER_SESSION_SECRET", "VIEWER_SESSION_SECRET_FILE")
	if err != nil {
		return Config{}, err
	}
	var sessionSecret []byte
	if sessionSecretRaw == "" {
		sessionSecret = make([]byte, 32)
		if _, err := rand.Read(sessionSecret); err != nil {
			return Config{}, fmt.Errorf("generate session secret: %w", err)
		}
	} else {
		decoded, decodeErr := base64.StdEncoding.DecodeString(sessionSecretRaw)
		if decodeErr == nil && len(decoded) >= 32 {
			sessionSecret = decoded
		} else {
			sessionSecret = []byte(sessionSecretRaw)
		}
		if len(sessionSecret) < 32 {
			return Config{}, errors.New("VIEWER_SESSION_SECRET must be at least 32 bytes or base64-encoded 32 bytes")
		}
	}

	cpampBaseURL := strings.TrimRight(strings.TrimSpace(getenv("CPAMP_BASE_URL", "http://cpa-manager-plus:18317")), "/")
	if !strings.HasPrefix(cpampBaseURL, "http://") && !strings.HasPrefix(cpampBaseURL, "https://") {
		return Config{}, errors.New("CPAMP_BASE_URL must start with http:// or https://")
	}
	cfg := Config{
		HTTPAddr:             getenv("HTTP_ADDR", "0.0.0.0:18417"),
		CPAMPBaseURL:         cpampBaseURL,
		CPAMPAdminKey:        adminKey,
		Sub2APIBaseURL:       sub2APIBaseURL,
		Sub2APIAdminAPIKey:   sub2APIAdminAPIKey,
		Sub2APIAdminJWT:      sub2APIAdminJWT,
		PublicAccess:         publicAccess,
		ViewerPassword:       viewerPassword,
		SessionSecret:        sessionSecret,
		SessionTTL:           durationEnv("VIEWER_SESSION_TTL", 12*time.Hour),
		SecureCookies:        boolEnv("VIEWER_SECURE_COOKIES", false),
		RequestTimeout:       durationEnv("CPAMP_REQUEST_TIMEOUT", 25*time.Second),
		MaxUpstreamBodyBytes: int64Env("VIEWER_MAX_UPSTREAM_BODY_BYTES", 12<<20),
		MaxAnalyticsRange:    durationEnv("VIEWER_MAX_ANALYTICS_RANGE", 366*24*time.Hour),
		MaxEventsPage:        int(int64Env("VIEWER_MAX_EVENTS_PAGE", 200)),
	}
	if err := loadAccessGuard(&cfg); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func loadSub2APICredentials() (string, string, error) {
	apiKey, err := readSecret("SUB2API_ADMIN_API_KEY", "SUB2API_ADMIN_API_KEY_FILE")
	if err != nil {
		return "", "", err
	}
	if apiKey == "" {
		apiKey, err = readSecret("SUB2API_ADMIN_KEY", "SUB2API_ADMIN_KEY_FILE")
		if err != nil {
			return "", "", err
		}
	}
	jwt, err := readSecret("SUB2API_ADMIN_JWT", "SUB2API_ADMIN_JWT_FILE")
	if err != nil {
		return "", "", err
	}
	return apiKey, jwt, nil
}

func validateServiceURL(value, name string) (string, error) {
	invalid := fmt.Errorf("%s must be an http(s) URL with a host and no query or fragment", name)
	parsed, err := url.Parse(value)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" || parsed.Hostname() == "" || parsed.User != nil || parsed.Opaque != "" || strings.ContainsAny(value, "?#\\") {
		return "", invalid
	}
	if strings.Contains(parsed.Path, "//") || strings.Contains(parsed.EscapedPath(), "%") {
		return "", invalid
	}
	for _, segment := range strings.Split(parsed.Path, "/") {
		if segment == "." || segment == ".." {
			return "", invalid
		}
	}
	if port := parsed.Port(); port != "" {
		n, err := strconv.Atoi(port)
		if err != nil || n < 1 || n > 65535 {
			return "", invalid
		}
	}
	return strings.TrimRight(parsed.String(), "/"), nil
}

func loadAccessGuard(cfg *Config) error {
	baseURL := strings.TrimSpace(os.Getenv("ACCESS_GUARD_BASE_URL"))
	managementKey := strings.TrimSpace(os.Getenv("ACCESS_GUARD_MANAGEMENT_KEY"))
	keyFile := strings.TrimSpace(os.Getenv("ACCESS_GUARD_MANAGEMENT_KEY_FILE"))
	publicKeysJSON := os.Getenv("ACCESS_GUARD_PUBLIC_KEYS")
	hasInlineKeys := strings.TrimSpace(publicKeysJSON) != ""
	publicKeysFile := strings.TrimSpace(os.Getenv("ACCESS_GUARD_PUBLIC_KEYS_FILE"))
	var publicAll bool
	switch strings.TrimSpace(os.Getenv("ACCESS_GUARD_PUBLIC_ALL")) {
	case "", "false":
	case "true":
		publicAll = true
	default:
		return errors.New("ACCESS_GUARD_PUBLIC_ALL must be true or false")
	}
	if publicAll && (hasInlineKeys || publicKeysFile != "") {
		return errors.New("ACCESS_GUARD_PUBLIC_ALL=true cannot be combined with ACCESS_GUARD_PUBLIC_KEYS or ACCESS_GUARD_PUBLIC_KEYS_FILE")
	}
	if hasInlineKeys && publicKeysFile != "" {
		return errors.New("ACCESS_GUARD_PUBLIC_KEYS and ACCESS_GUARD_PUBLIC_KEYS_FILE cannot be combined")
	}
	hasSelection := publicAll || hasInlineKeys || publicKeysFile != ""
	hasConnection := baseURL != "" || managementKey != "" || keyFile != ""
	if !hasSelection && !hasConnection {
		return nil
	}
	if !hasSelection {
		return errors.New("Access Guard requires an explicit public selection: ACCESS_GUARD_PUBLIC_ALL=true, ACCESS_GUARD_PUBLIC_KEYS, or ACCESS_GUARD_PUBLIC_KEYS_FILE")
	}

	var keys []AccessGuardPublicKey
	if !publicAll {
		var data []byte
		if hasInlineKeys {
			if len(publicKeysJSON) > 64<<10 {
				return errors.New("ACCESS_GUARD_PUBLIC_KEYS exceeds the size limit")
			}
			data = []byte(publicKeysJSON)
		} else {
			var err error
			data, err = readLimitedConfigFile(publicKeysFile, 64<<10)
			if err != nil {
				return errors.New("ACCESS_GUARD_PUBLIC_KEYS_FILE could not be read or exceeds the size limit")
			}
		}
		var err error
		keys, err = decodeAccessGuardPublicKeys(data)
		if err != nil {
			return err
		}
	}

	var validatedURL string
	if hasConnection {
		if baseURL == "" || (managementKey == "" && keyFile == "") {
			return errors.New("an independent Access Guard connection requires ACCESS_GUARD_BASE_URL and ACCESS_GUARD_MANAGEMENT_KEY or ACCESS_GUARD_MANAGEMENT_KEY_FILE")
		}
		var err error
		validatedURL, err = validateAccessGuardURL(baseURL)
		if err != nil {
			return err
		}
		if managementKey == "" {
			data, err := readLimitedConfigFile(keyFile, 16<<10)
			if err != nil {
				return errors.New("ACCESS_GUARD_MANAGEMENT_KEY_FILE could not be read or exceeds the size limit")
			}
			managementKey = strings.TrimSpace(string(data))
		}
		if managementKey == "" || len(managementKey) > 16<<10 || strings.IndexFunc(managementKey, unicode.IsControl) >= 0 {
			return errors.New("ACCESS_GUARD_MANAGEMENT_KEY must be nonempty, contain no control characters, and fit within the size limit")
		}
	}
	cfg.AccessGuardBaseURL = validatedURL
	cfg.AccessGuardManagementKey = managementKey
	cfg.AccessGuardViaCPAMP = !hasConnection
	cfg.AccessGuardPublicAll = publicAll
	cfg.AccessGuardPublicKeys = keys
	return nil
}

func validateAccessGuardURL(value string) (string, error) {
	invalid := errors.New("ACCESS_GUARD_BASE_URL must be an http(s) URL with a host and an optional plain path prefix; credentials, query, fragment, and ambiguous paths are forbidden")
	parsed, err := url.Parse(value)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" || parsed.Hostname() == "" || parsed.User != nil || parsed.Opaque != "" {
		return "", invalid
	}
	if strings.ContainsAny(value, "?#\\") || strings.Contains(parsed.EscapedPath(), "%") || strings.Contains(parsed.Path, "//") {
		return "", invalid
	}
	for _, segment := range strings.Split(parsed.Path, "/") {
		if segment == "." || segment == ".." {
			return "", invalid
		}
	}
	if strings.HasSuffix(parsed.Host, ":") {
		return "", invalid
	}
	if port := parsed.Port(); port != "" {
		n, err := strconv.Atoi(port)
		if err != nil || n < 1 || n > 65535 {
			return "", invalid
		}
	}
	return strings.TrimRight(parsed.String(), "/"), nil
}

func readLimitedConfigFile(path string, limit int64) ([]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, errors.New("configuration file could not be read")
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, limit+1))
	if err != nil || int64(len(data)) > limit {
		return nil, errors.New("configuration file could not be read or exceeds the size limit")
	}
	return data, nil
}

func decodeAccessGuardPublicKeys(data []byte) ([]AccessGuardPublicKey, error) {
	invalid := errors.New("Access Guard public keys must contain only a keys array of valid binding_id and name entries")
	document, ok := decodeAccessGuardObject(data)
	if !utf8.Valid(data) || !ok || len(document) != 1 {
		return nil, invalid
	}
	var entries []json.RawMessage
	if json.Unmarshal(document["keys"], &entries) != nil || entries == nil {
		return nil, invalid
	}
	if len(entries) > 256 {
		return nil, errors.New("Access Guard public keys exceed the public entry limit")
	}
	keys := make([]AccessGuardPublicKey, len(entries))
	seen := make(map[string]struct{}, len(keys))
	for i, entry := range entries {
		fields, ok := decodeAccessGuardObject(entry)
		key := &keys[i]
		if !ok || len(fields) != 2 || json.Unmarshal(fields["binding_id"], &key.BindingID) != nil || json.Unmarshal(fields["name"], &key.Name) != nil {
			return nil, invalid
		}
		key.BindingID = strings.TrimSpace(key.BindingID)
		key.Name = strings.TrimSpace(key.Name)
		if key.BindingID == "" || len(key.BindingID) > 128 || key.Name == "" || len(key.Name) > 256 || strings.IndexFunc(key.BindingID, unicode.IsControl) >= 0 || strings.IndexFunc(key.Name, unicode.IsControl) >= 0 {
			return nil, invalid
		}
		if _, exists := seen[key.BindingID]; exists {
			return nil, errors.New("Access Guard public keys contain duplicate binding IDs")
		}
		seen[key.BindingID] = struct{}{}
	}
	return keys, nil
}

// Decoding fields explicitly rejects duplicate names and case-insensitive aliases.
func decodeAccessGuardObject(data []byte) (map[string]json.RawMessage, bool) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	if token, err := decoder.Token(); err != nil || token != json.Delim('{') {
		return nil, false
	}
	fields := make(map[string]json.RawMessage)
	for decoder.More() {
		token, err := decoder.Token()
		name, ok := token.(string)
		if err != nil || !ok {
			return nil, false
		}
		if _, exists := fields[name]; exists {
			return nil, false
		}
		var value json.RawMessage
		if decoder.Decode(&value) != nil {
			return nil, false
		}
		fields[name] = value
	}
	if token, err := decoder.Token(); err != nil || token != json.Delim('}') {
		return nil, false
	}
	var extra any
	if decoder.Decode(&extra) != io.EOF {
		return nil, false
	}
	return fields, true
}

func readSecret(valueKey, fileKey string) (string, error) {
	if value := strings.TrimSpace(os.Getenv(valueKey)); value != "" {
		return value, nil
	}
	path := strings.TrimSpace(os.Getenv(fileKey))
	if path == "" {
		return "", nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read %s: %w", fileKey, err)
	}
	return strings.TrimSpace(string(data)), nil
}

func getenv(key, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		return value
	}
	return fallback
}

func boolEnv(key string, fallback bool) bool {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	parsed, err := strconv.ParseBool(value)
	if err != nil {
		return fallback
	}
	return parsed
}

func durationEnv(key string, fallback time.Duration) time.Duration {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	parsed, err := time.ParseDuration(value)
	if err != nil || parsed <= 0 {
		return fallback
	}
	return parsed
}

func int64Env(key string, fallback int64) int64 {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	parsed, err := strconv.ParseInt(value, 10, 64)
	if err != nil || parsed <= 0 {
		return fallback
	}
	return parsed
}
