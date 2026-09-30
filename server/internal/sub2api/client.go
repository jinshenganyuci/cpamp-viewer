package sub2api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type Client struct {
	baseURL      string
	adminAPIKey  string
	adminJWT     string
	httpClient   *http.Client
	maxBodyBytes int64
}

type UpstreamError struct {
	Status int
}

func (e *UpstreamError) Error() string {
	return fmt.Sprintf("Sub2API returned HTTP %d", e.Status)
}

func New(baseURL, adminAPIKey, adminJWT string, timeout time.Duration, maxBodyBytes int64) *Client {
	return &Client{
		baseURL:     strings.TrimRight(baseURL, "/"),
		adminAPIKey: strings.TrimSpace(adminAPIKey),
		adminJWT:    strings.TrimSpace(adminJWT),
		httpClient: &http.Client{
			Timeout: timeout,
			CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
		maxBodyBytes: maxBodyBytes,
	}
}

func (c *Client) Health(ctx context.Context) error {
	return c.doJSON(ctx, "/health", nil, nil)
}

func (c *Client) GetJSON(ctx context.Context, path string, query url.Values, target any) error {
	return c.doJSON(ctx, path, query, target)
}

func (c *Client) doJSON(ctx context.Context, path string, query url.Values, target any) error {
	if !strings.HasPrefix(path, "/") || strings.Contains(path, "..") {
		return errors.New("invalid upstream path")
	}
	endpoint := c.baseURL + path
	if len(query) > 0 {
		endpoint += "?" + query.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return err
	}
	if c.adminAPIKey != "" {
		req.Header.Set("X-API-Key", c.adminAPIKey)
	} else {
		req.Header.Set("Authorization", "Bearer "+c.adminJWT)
	}
	req.Header.Set("Accept", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, c.maxBodyBytes+1))
	if err != nil {
		return err
	}
	if int64(len(data)) > c.maxBodyBytes {
		return errors.New("Sub2API response exceeded configured size limit")
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return &UpstreamError{Status: resp.StatusCode}
	}
	if target == nil || len(data) == 0 {
		return nil
	}
	var envelope struct {
		Code *int            `json:"code"`
		Data json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(data, &envelope); err != nil {
		return fmt.Errorf("decode Sub2API response: %w", err)
	}
	if envelope.Code != nil {
		if *envelope.Code != 0 {
			return &UpstreamError{Status: http.StatusBadGateway}
		}
		data = envelope.Data
	}
	if len(data) == 0 || string(data) == "null" {
		return errors.New("Sub2API returned empty data")
	}
	if err := json.Unmarshal(data, target); err != nil {
		return fmt.Errorf("decode Sub2API data: %w", err)
	}
	return nil
}
