package config

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"strings"
	"unicode"
	"unicode/utf8"
)

const maxQuotaSources = 16

type QuotaSource struct {
	Type        string
	Name        string
	BaseURL     string
	AdminKey    string
	AdminAPIKey string
	AdminJWT    string
}

type quotaSourceInput struct {
	Type            string `json:"type"`
	Name            string `json:"name"`
	BaseURL         string `json:"base_url"`
	AdminKey        string `json:"admin_key"`
	AdminKeyFile    string `json:"admin_key_file"`
	AdminAPIKey     string `json:"admin_api_key"`
	AdminAPIKeyFile string `json:"admin_api_key_file"`
	AdminJWT        string `json:"admin_jwt"`
	AdminJWTFile    string `json:"admin_jwt_file"`
}

func loadQuotaSources(cfg Config) ([]QuotaSource, error) {
	inline := strings.TrimSpace(os.Getenv("VIEWER_QUOTA_SOURCES"))
	file := strings.TrimSpace(os.Getenv("VIEWER_QUOTA_SOURCES_FILE"))
	if inline != "" && file != "" {
		return nil, errors.New("configure only one of VIEWER_QUOTA_SOURCES and VIEWER_QUOTA_SOURCES_FILE")
	}
	if inline == "" && file == "" {
		return nil, nil
	}
	data := []byte(inline)
	if file != "" {
		var err error
		data, err = readLimitedConfigFile(file, 64<<10)
		if err != nil {
			return nil, errors.New("VIEWER_QUOTA_SOURCES_FILE could not be read or exceeds the size limit")
		}
	}
	invalid := errors.New("VIEWER_QUOTA_SOURCES must be a JSON array of valid source configurations")
	if len(data) > 64<<10 || !utf8.Valid(data) {
		return nil, invalid
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var inputs []quotaSourceInput
	var trailing any
	if decoder.Decode(&inputs) != nil || inputs == nil || decoder.Decode(&trailing) != io.EOF {
		return nil, invalid
	}
	counts := map[string]int{"cpamp": 1}
	seen := map[string]bool{"cpamp:" + quotaSourceURLIdentity(cfg.CPAMPBaseURL): true}
	total := 1
	if cfg.Sub2APIBaseURL != "" {
		counts["sub2api"] = 1
		seen["sub2api:"+quotaSourceURLIdentity(cfg.Sub2APIBaseURL)] = true
		total++
	}
	if total+len(inputs) > maxQuotaSources {
		return nil, fmt.Errorf("quota sources exceed the limit of %d including legacy sources", maxQuotaSources)
	}
	sources := make([]QuotaSource, 0, len(inputs))
	for index, input := range inputs {
		source, err := parseQuotaSource(input)
		if err != nil {
			return nil, fmt.Errorf("quota source %d: %w", index+1, err)
		}
		identity := source.Type + ":" + quotaSourceURLIdentity(source.BaseURL)
		if seen[identity] {
			return nil, fmt.Errorf("quota source %d duplicates an existing source", index+1)
		}
		seen[identity] = true
		counts[source.Type]++
		if source.Name == "" {
			label := "CPA Manager Plus"
			if source.Type == "sub2api" {
				label = "Sub2API"
			}
			source.Name = fmt.Sprintf("%s %d", label, counts[source.Type])
		}
		sources = append(sources, source)
	}
	return sources, nil
}

func parseQuotaSource(input quotaSourceInput) (QuotaSource, error) {
	source := QuotaSource{Type: strings.ToLower(strings.TrimSpace(input.Type)), Name: strings.TrimSpace(input.Name)}
	if source.Type == "cpa" {
		source.Type = "cpamp"
	}
	if source.Type != "cpamp" && source.Type != "sub2api" {
		return QuotaSource{}, errors.New("type must be cpa, cpamp or sub2api")
	}
	if len(source.Name) > 80 || strings.IndexFunc(source.Name, unicode.IsControl) >= 0 {
		return QuotaSource{}, errors.New("name must be at most 80 bytes and contain no control characters")
	}
	var err error
	source.BaseURL, err = validateServiceURL(strings.TrimSpace(input.BaseURL), "base_url")
	if err != nil {
		return QuotaSource{}, err
	}
	source.BaseURL = quotaSourceURLIdentity(source.BaseURL)
	if source.Type == "cpamp" {
		if input.AdminAPIKey != "" || input.AdminAPIKeyFile != "" || input.AdminJWT != "" || input.AdminJWTFile != "" {
			return QuotaSource{}, errors.New("CPA requires admin_key or admin_key_file")
		}
		source.AdminKey, err = quotaSourceSecret(input.AdminKey, input.AdminKeyFile)
		if err != nil || source.AdminKey == "" {
			return QuotaSource{}, errors.New("a valid admin_key or admin_key_file is required")
		}
	} else {
		if input.AdminKey != "" || input.AdminKeyFile != "" {
			return QuotaSource{}, errors.New("Sub2API requires admin_api_key or admin_jwt, optionally via a _file field")
		}
		source.AdminAPIKey, err = quotaSourceSecret(input.AdminAPIKey, input.AdminAPIKeyFile)
		if err != nil {
			return QuotaSource{}, errors.New("invalid admin_api_key or admin_api_key_file")
		}
		source.AdminJWT, err = quotaSourceSecret(input.AdminJWT, input.AdminJWTFile)
		if err != nil || (source.AdminAPIKey == "" && source.AdminJWT == "") || (source.AdminAPIKey != "" && source.AdminJWT != "") {
			return QuotaSource{}, errors.New("configure exactly one valid Sub2API admin_api_key or admin_jwt, optionally via a _file field")
		}
	}
	return source, nil
}

func quotaSourceSecret(value, path string) (string, error) {
	value, path = strings.TrimSpace(value), strings.TrimSpace(path)
	if value != "" && path != "" {
		return "", errors.New("ambiguous credential")
	}
	if path != "" {
		data, err := readLimitedConfigFile(path, 16<<10)
		if err != nil {
			return "", errors.New("unreadable credential")
		}
		value = strings.TrimSpace(string(data))
	}
	if len(value) > 16<<10 || !utf8.ValidString(value) || strings.IndexFunc(value, unicode.IsControl) >= 0 {
		return "", errors.New("invalid credential")
	}
	return value, nil
}

func quotaSourceURLIdentity(value string) string {
	parsed, err := url.Parse(value)
	if err != nil {
		return value
	}
	parsed.Scheme = strings.ToLower(parsed.Scheme)
	parsed.Host = strings.ToLower(parsed.Host)
	if (parsed.Scheme == "https" && parsed.Port() == "443") || (parsed.Scheme == "http" && parsed.Port() == "80") {
		parsed.Host = strings.TrimSuffix(parsed.Host, ":"+parsed.Port())
	}
	parsed.Path = strings.TrimRight(parsed.Path, "/")
	return parsed.String()
}
