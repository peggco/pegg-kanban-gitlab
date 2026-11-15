package main

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func testClient(t *testing.T, handler http.HandlerFunc) *Client {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	settings := defaultSettings()
	settings.BaseURL = server.URL
	settings.Token = "test-token-value"
	settings.Project = "group/project"
	return newClient(settings)
}

func TestClientHeadersAndDecode(t *testing.T) {
	client := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("method = %s", r.Method)
		}
		if r.URL.Path != "/user" {
			t.Errorf("path = %s", r.URL.Path)
		}
		if got := r.Header.Get("PRIVATE-TOKEN"); got != "test-token-value" {
			t.Errorf("private token = %q", got)
		}
		if got := r.Header.Get("Accept"); got != "application/json" {
			t.Errorf("accept = %q", got)
		}
		if got := r.Header.Get("User-Agent"); got != userAgent {
			t.Errorf("user agent = %q", got)
		}
		fmt.Fprint(w, `{"username":"octocat","name":"The Octocat"}`)
	})
	var user struct {
		Username string `json:"username"`
	}
	if err := client.getJSON(context.Background(), "/user", nil, &user); err != nil {
		t.Fatal(err)
	}
	if user.Username != "octocat" {
		t.Fatalf("username = %q", user.Username)
	}
}

func TestClientErrors(t *testing.T) {
	cases := []struct {
		status int
		body   string
		want   string
	}{
		{401, `{"message":"401 Unauthorized"}`, "PEGG_GITLAB_TOKEN"},
		{403, `{"message":"403 Forbidden"}`, "api/read_api"},
		{404, `{"message":"404 Project Not Found"}`, "check the project"},
	}
	for _, tc := range cases {
		t.Run(fmt.Sprint(tc.status), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tc.status)
				fmt.Fprint(w, tc.body)
			}))
			defer server.Close()
			settings := defaultSettings()
			settings.BaseURL = server.URL
			settings.Token = "secret-token-value"
			err := newClient(settings).getJSON(context.Background(), "/projects/x", nil, nil)
			if err == nil {
				t.Fatal("expected error")
			}
			msg := err.Error()
			if !strings.Contains(msg, fmt.Sprintf("HTTP %d", tc.status)) {
				t.Fatalf("missing status: %v", err)
			}
			if !strings.Contains(msg, tc.want) {
				t.Fatalf("missing hint %q: %v", tc.want, err)
			}
			if strings.Contains(msg, "secret-token-value") {
				t.Fatalf("error leaked token: %v", err)
			}
		})
	}
}

func TestClientErrorDecodesMessage(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		fmt.Fprint(w, `{"message":{"name":["is too long"]}}`)
	}))
	defer server.Close()
	settings := defaultSettings()
	settings.BaseURL = server.URL
	err := newClient(settings).getJSON(context.Background(), "/projects/x", nil, nil)
	if err == nil || !strings.Contains(err.Error(), "name") {
		t.Fatalf("message not decoded: %v", err)
	}
}

func TestPaginationCap(t *testing.T) {
	requests := 0
	client := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.URL.Query().Get("page") == "" {
			w.Header().Set("X-Next-Page", "2")
		} else {
			w.Header().Set("X-Next-Page", fmt.Sprint(requests+1))
		}
		fmt.Fprintf(w, `[%d]`, requests)
	})
	items, err := getPaged[int](context.Background(), client, "/projects", url.Values{"per_page": {"1"}}, 0)
	if err != nil {
		t.Fatal(err)
	}
	if requests != maxPages {
		t.Fatalf("requests = %d, want %d", requests, maxPages)
	}
	if len(items) != maxPages {
		t.Fatalf("items = %d, want %d", len(items), maxPages)
	}
}

func TestPaginationStopsAtLimit(t *testing.T) {
	requests := 0
	client := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		requests++
		w.Header().Set("X-Next-Page", "2")
		fmt.Fprint(w, `[1,2,3]`)
	})
	items, err := getPaged[int](context.Background(), client, "/projects", nil, 3)
	if err != nil {
		t.Fatal(err)
	}
	if requests != 1 {
		t.Fatalf("requests = %d, want 1", requests)
	}
	if len(items) != 3 {
		t.Fatalf("items = %d, want 3", len(items))
	}
}

func TestPaginationStopsWithoutNextPage(t *testing.T) {
	requests := 0
	client := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		requests++
		fmt.Fprint(w, `[1]`)
	})
	items, err := getPaged[int](context.Background(), client, "/projects", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	if requests != 1 || len(items) != 1 {
		t.Fatalf("requests = %d items = %d", requests, len(items))
	}
}
