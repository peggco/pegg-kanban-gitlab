package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	peggplugin "github.com/peggco/pegg/pkg/plugin"
)

func hookPlugin(t *testing.T, handler http.HandlerFunc) *Plugin {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	settings := defaultSettings()
	settings.BaseURL = server.URL
	settings.Token = "hook-token"
	settings.Project = "group/project"
	plugin := &Plugin{}
	plugin.settings = settings
	return plugin
}

func executeHookT(t *testing.T, plugin *Plugin, name string, kind peggplugin.HookKind, value any) *peggplugin.HookResult {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	result, err := plugin.ExecuteHook(peggplugin.HookRequest{Name: name, Kind: kind, Value: raw, TimeoutMS: 5000})
	if err != nil {
		t.Fatal(err)
	}
	if result == nil {
		t.Fatalf("%s returned nil result", name)
	}
	return result
}

func TestHooksSpecsAndCapability(t *testing.T) {
	plugin := &Plugin{}
	specs, err := plugin.Hooks()
	if err != nil {
		t.Fatal(err)
	}
	if len(specs) != 4 {
		t.Fatalf("specs = %#v", specs)
	}
	want := map[string]peggplugin.HookKind{
		HookProviders:     peggplugin.HookFilter,
		HookIssueFetch:    peggplugin.HookFilter,
		HookRunPrepare:    peggplugin.HookFilter,
		HookCardSucceeded: peggplugin.HookAction,
	}
	for _, spec := range specs {
		kind, ok := want[spec.Name]
		if !ok {
			t.Fatalf("unexpected spec %#v", spec)
		}
		if spec.Kind != kind {
			t.Errorf("%s kind = %q, want %q", spec.Name, spec.Kind, kind)
		}
		if spec.Priority != 10 {
			t.Errorf("%s priority = %d, want 10", spec.Name, spec.Priority)
		}
	}
	capabilities := plugin.Capabilities()
	if len(capabilities) != 1 || capabilities[0] != string(peggplugin.CapabilityHooks) {
		t.Fatalf("capabilities = %#v", capabilities)
	}
}

func TestHooksProviders(t *testing.T) {
	plugin := hookPlugin(t, func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("providers hook made an API call: %s", r.URL.Path)
	})
	existing := []ProviderInfo{{ID: "other", Label: "Other", Enabled: true, Available: true}}
	result := executeHookT(t, plugin, HookProviders, peggplugin.HookFilter, existing)
	if !result.Handled {
		t.Fatal("providers hook was not handled")
	}
	var providers []ProviderInfo
	if err := json.Unmarshal(result.Value, &providers); err != nil {
		t.Fatal(err)
	}
	if len(providers) != 2 {
		t.Fatalf("providers = %#v", providers)
	}
	gitlab := providers[1]
	if gitlab.ID != pluginName || gitlab.Label != "GitLab" || !gitlab.Enabled || !gitlab.Available || !gitlab.Authenticated {
		t.Fatalf("gitlab provider = %#v", gitlab)
	}
	if gitlab.Target != "group/project" || gitlab.Detail == "" {
		t.Fatalf("gitlab provider = %#v", gitlab)
	}
	if gitlab.SyncMinutes != 0 {
		t.Fatalf("sync_minutes = %d, want 0", gitlab.SyncMinutes)
	}

	result = executeHookT(t, plugin, HookProviders, peggplugin.HookFilter, providers)
	if err := json.Unmarshal(result.Value, &providers); err != nil {
		t.Fatal(err)
	}
	if len(providers) != 2 {
		t.Fatalf("providers duplicated on second pass: %#v", providers)
	}
}

func TestHooksProvidersUnavailable(t *testing.T) {
	plugin := &Plugin{}
	plugin.settings = defaultSettings()
	plugin.settings.Enabled = true
	result := executeHookT(t, plugin, HookProviders, peggplugin.HookFilter, []ProviderInfo{})
	if !result.Handled {
		t.Fatal("providers hook was not handled")
	}
	var providers []ProviderInfo
	if err := json.Unmarshal(result.Value, &providers); err != nil {
		t.Fatal(err)
	}
	if len(providers) != 1 {
		t.Fatalf("providers = %#v", providers)
	}
	if providers[0].Available || providers[0].Authenticated {
		t.Fatalf("provider should be unavailable: %#v", providers[0])
	}
	if !strings.Contains(providers[0].Detail, "PEGG_GITLAB_TOKEN") {
		t.Fatalf("detail = %q", providers[0].Detail)
	}
}

func TestHooksDisabledNoOp(t *testing.T) {
	plugin := hookPlugin(t, func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("disabled plugin made an API call: %s", r.URL.Path)
	})
	plugin.settings.Enabled = false

	providers := executeHookT(t, plugin, HookProviders, peggplugin.HookFilter, []ProviderInfo{})
	if providers.Handled {
		t.Fatal("providers hook should not be handled when disabled")
	}

	fetch := executeHookT(t, plugin, HookIssueFetch, peggplugin.HookFilter, IssueFetch{Provider: pluginName})
	if fetch.Handled {
		t.Fatal("issues fetch should not be handled when disabled")
	}

	card := executeHookT(t, plugin, HookCardSucceeded, peggplugin.HookAction, CardSucceeded{
		Card: Card{Issue: &IssueRef{Provider: pluginName, Number: 1}},
	})
	if card.Handled {
		t.Fatal("card hook should not be handled when disabled")
	}
}

func TestHooksOtherProviderNoOp(t *testing.T) {
	plugin := hookPlugin(t, func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("other provider made an API call: %s", r.URL.Path)
	})
	fetch := executeHookT(t, plugin, HookIssueFetch, peggplugin.HookFilter, IssueFetch{Provider: "github"})
	if fetch.Handled || len(fetch.Value) != 0 {
		t.Fatalf("fetch = %#v", fetch)
	}
	card := executeHookT(t, plugin, HookCardSucceeded, peggplugin.HookAction, CardSucceeded{
		Card: Card{Issue: &IssueRef{Provider: "github", Number: 1}},
	})
	if card.Handled || len(card.Value) != 0 {
		t.Fatalf("card = %#v", card)
	}
	unknown := executeHookT(t, plugin, "unknown.hook", peggplugin.HookAction, nil)
	if unknown.Handled {
		t.Fatal("unknown hook should not be handled")
	}
}

func TestHookIssueFetchMapsIssues(t *testing.T) {
	plugin := hookPlugin(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("method = %s", r.Method)
		}
		if got := r.URL.EscapedPath(); got != "/projects/group%2Fproject/issues" {
			t.Errorf("escaped path = %q", got)
		}
		query := r.URL.Query()
		if query.Get("state") != "opened" {
			t.Errorf("state = %q", query.Get("state"))
		}
		if query.Get("labels") != "bug,urgent" {
			t.Errorf("labels = %q", query.Get("labels"))
		}
		if query.Get("search") != "widget" {
			t.Errorf("search = %q", query.Get("search"))
		}
		if query.Get("per_page") != "5" {
			t.Errorf("per_page = %q", query.Get("per_page"))
		}
		fmt.Fprint(w, `[{"iid":12,"title":"Broken widget","description":"Details","state":"opened","web_url":"https://gitlab.com/group/project/-/issues/12","labels":["bug"],"assignees":[{"username":"alice"}],"author":{"username":"bob"},"created_at":"2026-01-01T00:00:00Z","updated_at":"2026-01-02T00:00:00Z"}]`)
	})

	result := executeHookT(t, plugin, HookIssueFetch, peggplugin.HookFilter, IssueFetch{
		Provider: pluginName,
		Labels:   []string{"bug", "urgent"},
		State:    "opened",
		Limit:    5,
		Query:    "widget",
		Issues:   []HookIssue{{Title: "Existing"}},
	})
	if !result.Handled {
		t.Fatal("issues fetch was not handled")
	}
	var payload IssueFetch
	if err := json.Unmarshal(result.Value, &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Provider != pluginName {
		t.Fatalf("provider = %q", payload.Provider)
	}
	if len(payload.Issues) != 2 {
		t.Fatalf("issues = %#v", payload.Issues)
	}
	issue := payload.Issues[1]
	if issue.Number != 12 || issue.Title != "Broken widget" || issue.Body != "Details" || issue.State != "opened" {
		t.Fatalf("issue = %#v", issue)
	}
	if issue.URL != "https://gitlab.com/group/project/-/issues/12" {
		t.Fatalf("url = %q", issue.URL)
	}
	if len(issue.Labels) != 1 || issue.Labels[0] != "bug" || len(issue.Assignees) != 1 || issue.Assignees[0] != "alice" {
		t.Fatalf("issue = %#v", issue)
	}
	if issue.CreatedAt != "2026-01-01T00:00:00Z" || issue.UpdatedAt != "2026-01-02T00:00:00Z" {
		t.Fatalf("issue = %#v", issue)
	}
}

func TestHookIssueFetchUsesTargetOverride(t *testing.T) {
	plugin := hookPlugin(t, func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.EscapedPath(); got != "/projects/team%2Fapp/issues" {
			t.Errorf("escaped path = %q", got)
		}
		query := r.URL.Query()
		if query.Get("state") != "opened" {
			t.Errorf("state = %q", query.Get("state"))
		}
		if query.Get("per_page") != "50" {
			t.Errorf("per_page = %q", query.Get("per_page"))
		}
		fmt.Fprint(w, `[]`)
	})
	plugin.settings.SyncLabels = []string{"kanban"}
	result := executeHookT(t, plugin, HookIssueFetch, peggplugin.HookFilter, IssueFetch{
		Provider: pluginName,
		Target:   "team/app",
	})
	if !result.Handled {
		t.Fatal("issues fetch was not handled")
	}
	var payload IssueFetch
	if err := json.Unmarshal(result.Value, &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Target != "team/app" || payload.State != "opened" || payload.Limit != 50 {
		t.Fatalf("payload = %#v", payload)
	}
	if len(payload.Labels) != 1 || payload.Labels[0] != "kanban" {
		t.Fatalf("labels = %#v", payload.Labels)
	}
}

func TestHookCardSucceededCommentsAndCreatesMR(t *testing.T) {
	var noted, merged bool
	plugin := hookPlugin(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.EscapedPath() == "/projects/group%2Fproject/issues/12/notes":
			noted = true
			var body struct {
				Body string `json:"body"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Error(err)
			}
			if body.Body != "All tests passed." {
				t.Errorf("comment body = %q", body.Body)
			}
			fmt.Fprint(w, `{"id":99}`)
		case r.Method == http.MethodPost && r.URL.EscapedPath() == "/projects/group%2Fproject/merge_requests":
			merged = true
			var body struct {
				SourceBranch string `json:"source_branch"`
				TargetBranch string `json:"target_branch"`
				Title        string `json:"title"`
				Description  string `json:"description"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Error(err)
			}
			if body.SourceBranch != "feat/fix-widget" || body.TargetBranch != "main" {
				t.Errorf("branches = %q -> %q", body.SourceBranch, body.TargetBranch)
			}
			if body.Title != "Fix widget" {
				t.Errorf("title = %q", body.Title)
			}
			if body.Description != "Fixes the widget.\n\nAll tests passed." {
				t.Errorf("description = %q", body.Description)
			}
			fmt.Fprint(w, `{"iid":7,"web_url":"https://gitlab.com/group/project/-/merge_requests/7","state":"opened"}`)
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		}
	})

	result := executeHookT(t, plugin, HookCardSucceeded, peggplugin.HookAction, CardSucceeded{
		Card: Card{
			ID:    "card-1",
			Title: "Fix widget",
			Body:  "Fixes the widget.",
			Issue: &IssueRef{Provider: pluginName, Repo: "group/project", Number: 12},
		},
		Worktree:            &WorktreeEntry{Slug: "fix-widget", Branch: "feat/fix-widget"},
		Summary:             "All tests passed.",
		Feedback:            true,
		CreateChangeRequest: true,
	})
	if !result.Handled {
		t.Fatal("card hook was not handled")
	}
	if !noted || !merged {
		t.Fatalf("noted = %t, merged = %t", noted, merged)
	}
}

func TestWorktreePrepareHook(t *testing.T) {
	repo := initGitRepo(t)
	plugin := &Plugin{settings: Settings{
		Enabled:             true,
		WorktreeRoot:        ".pegg-worktrees",
		DefaultTargetBranch: "main",
	}}
	payload, _ := json.Marshal(RunPrepare{
		Card:       Card{ID: "card-abc", Title: "Fix login"},
		Repo:       repo,
		BaseBranch: "main",
		Context:    RunContext{Kind: "repo", Path: repo},
	})
	result, err := plugin.ExecuteHook(peggplugin.HookRequest{Name: HookRunPrepare, Value: payload})
	if err != nil {
		t.Fatal(err)
	}
	if result == nil || !result.Handled {
		t.Fatalf("result = %#v", result)
	}
	var prepared RunPrepare
	if err := json.Unmarshal(result.Value, &prepared); err != nil {
		t.Fatal(err)
	}
	if prepared.Context.Kind != "worktree" || prepared.Context.Path == "" || !strings.Contains(prepared.Context.Path, ".pegg-worktrees") {
		t.Fatalf("context = %#v", prepared.Context)
	}
	if prepared.Context.Branch == "" || !strings.HasPrefix(prepared.Context.Branch, "pegg/") {
		t.Fatalf("branch = %q", prepared.Context.Branch)
	}
	if prepared.Context.Base != "main" {
		t.Fatalf("base = %q", prepared.Context.Base)
	}
}

func TestWorktreePrepareHookNoOp(t *testing.T) {
	plugin := &Plugin{settings: Settings{Enabled: false}}
	payload, _ := json.Marshal(RunPrepare{
		Card: Card{ID: "card-abc", Title: "Fix login"},
		Repo: "/tmp/repo",
	})
	result, err := plugin.ExecuteHook(peggplugin.HookRequest{Name: HookRunPrepare, Value: payload})
	if err != nil {
		t.Fatal(err)
	}
	if result.Handled {
		t.Fatal("disabled plugin should not handle run prepare")
	}

	plugin.settings.Enabled = true
	payload, _ = json.Marshal(RunPrepare{Card: Card{ID: "card-abc", Title: "Fix login"}})
	result, err = plugin.ExecuteHook(peggplugin.HookRequest{Name: HookRunPrepare, Value: payload})
	if err != nil {
		t.Fatal(err)
	}
	if result.Handled {
		t.Fatal("empty repo should not be handled")
	}
}

func TestHookCardSucceededRoutedFromOtherProvider(t *testing.T) {
	var noted, merged bool
	plugin := hookPlugin(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.EscapedPath() == "/projects/group%2Fproject/issues/12/notes":
			noted = true
			fmt.Fprint(w, `{"id":99}`)
		case r.Method == http.MethodPost && r.URL.EscapedPath() == "/projects/group%2Fproject/merge_requests":
			merged = true
			fmt.Fprint(w, `{"iid":7,"web_url":"https://gitlab.com/group/project/-/merge_requests/7","state":"opened"}`)
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		}
	})

	result := executeHookT(t, plugin, HookCardSucceeded, peggplugin.HookAction, CardSucceeded{
		Card: Card{
			ID:    "card-1",
			Title: "Fix widget",
			Body:  "Fixes the widget.",
			Issue: &IssueRef{Provider: "jira", Repo: "ABC", Key: "ABC-12"},
		},
		Worktree:              &WorktreeEntry{Slug: "fix-widget", Branch: "feat/fix-widget"},
		Summary:               "All tests passed.",
		Feedback:              true,
		CreateChangeRequest:   true,
		ChangeRequestProvider: "gitlab",
	})
	if !result.Handled {
		t.Fatal("card hook was not handled")
	}
	if noted {
		t.Error("gitlab commented on a non-gitlab issue")
	}
	if !merged {
		t.Fatal("merge request was not created")
	}
}

func TestHookCardSucceededNotRouted(t *testing.T) {
	plugin := hookPlugin(t, func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
	})
	result := executeHookT(t, plugin, HookCardSucceeded, peggplugin.HookAction, CardSucceeded{
		Card: Card{
			ID:    "card-1",
			Title: "Fix widget",
			Issue: &IssueRef{Provider: "jira", Repo: "ABC", Key: "ABC-12"},
		},
		Worktree:              &WorktreeEntry{Slug: "fix-widget", Branch: "feat/fix-widget"},
		CreateChangeRequest:   true,
		ChangeRequestProvider: "bitbucket",
	})
	if result.Handled {
		t.Fatal("card hook should not be handled for another change request provider")
	}
}

func TestHookRunPrepareRouting(t *testing.T) {
	repo := initGitRepo(t)
	plugin := &Plugin{settings: Settings{
		Enabled:             true,
		WorktreeRoot:        ".pegg-worktrees",
		DefaultTargetBranch: "main",
	}}
	payload, _ := json.Marshal(RunPrepare{
		Card:                  Card{ID: "card-abc", Title: "Fix login"},
		Repo:                  repo,
		BaseBranch:            "main",
		ChangeRequestProvider: "bitbucket",
		Context:               RunContext{Kind: "repo", Path: repo},
	})
	result, err := plugin.ExecuteHook(peggplugin.HookRequest{Name: HookRunPrepare, Value: payload})
	if err != nil {
		t.Fatal(err)
	}
	if result.Handled {
		t.Fatal("run.prepare should not be handled for another change request provider")
	}

	payload, _ = json.Marshal(RunPrepare{
		Card:                  Card{ID: "card-abc", Title: "Fix login"},
		Repo:                  repo,
		BaseBranch:            "main",
		ChangeRequestProvider: "gitlab",
		Context:               RunContext{Kind: "repo", Path: repo},
	})
	result, err = plugin.ExecuteHook(peggplugin.HookRequest{Name: HookRunPrepare, Value: payload})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Handled {
		t.Fatal("run.prepare should be handled for gitlab")
	}
}

func TestHookRunPrepareSkipsNonGitRepo(t *testing.T) {
	dir := t.TempDir()
	plugin := &Plugin{settings: Settings{Enabled: true, WorktreeRoot: ".pegg-worktrees", DefaultTargetBranch: "main"}}
	payload, _ := json.Marshal(RunPrepare{
		Card:                  Card{ID: "card-abc", Title: "Fix login"},
		Repo:                  dir,
		BaseBranch:            "main",
		ChangeRequestProvider: "gitlab",
		Context:               RunContext{Kind: "repo", Path: dir},
	})
	result, err := plugin.ExecuteHook(peggplugin.HookRequest{Name: HookRunPrepare, Value: payload})
	if err != nil {
		t.Fatal(err)
	}
	if result.Handled {
		t.Fatal("run.prepare should be skipped for a non-git repository")
	}
}
