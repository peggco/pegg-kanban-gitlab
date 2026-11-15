package main

import (
	"encoding/json"
	"testing"
)

func clearGitLabEnv(t *testing.T) {
	t.Helper()
	for _, name := range []string{
		"PEGG_GITLAB_TOKEN", "GITLAB_TOKEN", "CI_JOB_TOKEN",
		"PEGG_GITLAB_URL", "PEGG_GITLAB_PROJECT",
		"PEGG_GITLAB_TIMEOUT_SECONDS", "PEGG_GITLAB_DISABLED",
	} {
		t.Setenv(name, "")
	}
}

func TestDefaultSettings(t *testing.T) {
	clearGitLabEnv(t)
	settings := defaultSettings()
	if !settings.Enabled {
		t.Fatal("enabled should default to true")
	}
	if settings.BaseURL != "https://gitlab.com/api/v4" {
		t.Fatalf("base_url = %q", settings.BaseURL)
	}
	if settings.RequestTimeoutSeconds != 30 {
		t.Fatalf("request_timeout_seconds = %d", settings.RequestTimeoutSeconds)
	}
	if settings.Workdir == "" {
		t.Fatal("workdir should default to the current directory")
	}
	if settings.WorktreeRoot != ".pegg-worktrees" {
		t.Fatalf("worktree_root = %q", settings.WorktreeRoot)
	}
}

func TestConfigPrecedence(t *testing.T) {
	clearGitLabEnv(t)
	t.Setenv("PEGG_GITLAB_TOKEN", "env-token")
	t.Setenv("PEGG_GITLAB_URL", "https://env.example.com/")

	settings := defaultSettings()
	settings.applyHost([]byte(`{"plugins":{"options":{"gitlab":{"token":"host-token","project":"host/project","request_timeout_seconds":10}}}}`))
	if err := settings.applyOptions(json.RawMessage(`{"project":"group/project","request_timeout_seconds":12}`)); err != nil {
		t.Fatal(err)
	}
	settings.applyEnv()
	settings.normalize()

	if settings.Token != "env-token" {
		t.Fatalf("env token should win, got %q", settings.Token)
	}
	if settings.Project != "group/project" {
		t.Fatalf("options project should win, got %q", settings.Project)
	}
	if settings.RequestTimeoutSeconds != 12 {
		t.Fatalf("options timeout not applied: %d", settings.RequestTimeoutSeconds)
	}
	if settings.BaseURL != "https://env.example.com/api/v4" {
		t.Fatalf("base url not normalized: %q", settings.BaseURL)
	}
}

func TestOptionsOverrideHost(t *testing.T) {
	clearGitLabEnv(t)
	settings := defaultSettings()
	settings.applyHost([]byte(`{"plugins":{"options":{"pegg-gitlab":{"token":"host-token","project":"host/project"}}}}`))
	if err := settings.applyOptions(json.RawMessage(`{"token":"deps-token"}`)); err != nil {
		t.Fatal(err)
	}
	if settings.Token != "deps-token" {
		t.Fatalf("options token should win, got %q", settings.Token)
	}
	if settings.Project != "host/project" {
		t.Fatalf("host project should be retained, got %q", settings.Project)
	}
}

func TestTokenFallbacks(t *testing.T) {
	clearGitLabEnv(t)
	t.Setenv("GITLAB_TOKEN", "gitlab-token")
	settings := defaultSettings()
	settings.applyEnv()
	if settings.Token != "gitlab-token" || settings.TokenSource != "GITLAB_TOKEN" {
		t.Fatalf("GITLAB_TOKEN fallback = %q (%q)", settings.Token, settings.TokenSource)
	}

	t.Setenv("GITLAB_TOKEN", "")
	t.Setenv("CI_JOB_TOKEN", "ci-token")
	settings = defaultSettings()
	settings.applyEnv()
	if settings.Token != "ci-token" || settings.TokenSource != "CI_JOB_TOKEN" {
		t.Fatalf("CI_JOB_TOKEN fallback = %q (%q)", settings.Token, settings.TokenSource)
	}

	t.Setenv("PEGG_GITLAB_TOKEN", "pegg-token")
	settings = defaultSettings()
	settings.applyEnv()
	if settings.Token != "pegg-token" || settings.TokenSource != "PEGG_GITLAB_TOKEN" {
		t.Fatalf("PEGG token should win = %q (%q)", settings.Token, settings.TokenSource)
	}
}

func TestDisabledEnv(t *testing.T) {
	clearGitLabEnv(t)
	t.Setenv("PEGG_GITLAB_DISABLED", "true")
	settings := defaultSettings()
	settings.applyEnv()
	if settings.Enabled {
		t.Fatal("expected plugin to be disabled")
	}
}

func TestEnvProjectAndTimeout(t *testing.T) {
	clearGitLabEnv(t)
	t.Setenv("PEGG_GITLAB_PROJECT", "acme/widgets")
	t.Setenv("PEGG_GITLAB_TIMEOUT_SECONDS", "45")
	settings := defaultSettings()
	settings.applyEnv()
	if settings.Project != "acme/widgets" {
		t.Fatalf("project = %q", settings.Project)
	}
	if settings.RequestTimeoutSeconds != 45 {
		t.Fatalf("timeout = %d", settings.RequestTimeoutSeconds)
	}
}

func TestNormalizeBaseURL(t *testing.T) {
	cases := map[string]string{
		"":                                  "https://gitlab.com/api/v4",
		"https://gitlab.example.com/":       "https://gitlab.example.com/api/v4",
		"https://gitlab.example.com/api/v4": "https://gitlab.example.com/api/v4",
		"http://localhost:8080":             "http://localhost:8080/api/v4",
	}
	for input, want := range cases {
		if got := normalizeBaseURL(input); got != want {
			t.Errorf("normalizeBaseURL(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestRedactSettings(t *testing.T) {
	settings := Settings{Token: "abc123"}
	got := redactSettings("failed with abc123 and abc123", settings)
	if got != "failed with [redacted] and [redacted]" {
		t.Fatalf("unexpected redaction: %q", got)
	}
}

func TestKanbanOptionKeys(t *testing.T) {
	clearGitLabEnv(t)
	for _, key := range []string{"kanban-gitlab", "pegg-kanban-gitlab", "gitlab", "pegg-gitlab"} {
		settings := defaultSettings()
		settings.applyHost([]byte(`{"plugins":{"options":{"` + key + `":{"project":"group/project"}}}}`))
		if settings.Project != "group/project" {
			t.Errorf("key %q not applied: project = %q", key, settings.Project)
		}
	}

	settings := defaultSettings()
	settings.applyHost([]byte(`{"plugins":{"options":{"gitlab":{"project":"legacy/project"},"kanban-gitlab":{"project":"group/project"}}}}`))
	if settings.Project != "group/project" {
		t.Fatalf("canonical key should win: project = %q", settings.Project)
	}
}
