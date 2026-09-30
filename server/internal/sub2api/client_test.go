package sub2api

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"
)

func TestClientUnwrapsDataAndUsesAdminAPIKey(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/api/v1/admin/accounts" || r.URL.Query().Get("page") != "2" || r.Header.Get("X-API-Key") != "private-key" || r.Header.Get("Authorization") != "" {
			t.Errorf("unexpected request: %s %s %#v", r.Method, r.URL, r.Header)
		}
		_, _ = io.WriteString(w, `{"code":0,"message":"ok","data":{"items":[{"id":1}]}}`)
	}))
	defer upstream.Close()
	client := New(upstream.URL, "private-key", "ignored-jwt", time.Second, 1<<20)
	var response struct {
		Items []struct {
			ID int `json:"id"`
		} `json:"items"`
	}
	if err := client.GetJSON(context.Background(), "/api/v1/admin/accounts", url.Values{"page": {"2"}}, &response); err != nil {
		t.Fatal(err)
	}
	if len(response.Items) != 1 || response.Items[0].ID != 1 {
		t.Fatalf("unexpected decoded response: %#v", response)
	}
}

func TestClientUsesJWTWithoutFollowingRedirect(t *testing.T) {
	redirected := false
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/redirected" {
			redirected = true
			return
		}
		if r.Header.Get("Authorization") != "Bearer private-jwt" || r.Header.Get("X-API-Key") != "" {
			t.Errorf("unexpected JWT headers: %#v", r.Header)
		}
		http.Redirect(w, r, "/redirected", http.StatusFound)
	}))
	defer upstream.Close()
	client := New(upstream.URL, "", "private-jwt", time.Second, 1<<20)
	var response json.RawMessage
	if err := client.GetJSON(context.Background(), "/api/v1/admin/accounts", nil, &response); err == nil {
		t.Fatal("redirect was accepted")
	}
	if redirected {
		t.Fatal("client followed an upstream redirect")
	}
}
