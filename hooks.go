package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	peggplugin "github.com/peggco/pegg/pkg/plugin"
)

const (
	HookProviders     = "kanban.providers"
	HookIssueFetch    = "kanban.issues.fetch"
	HookRunPrepare    = "kanban.run.prepare"
	HookCardSucceeded = "kanban.card.succeeded"

	hookPriority = 10
)

type ProviderInfo struct {
	ID            string `json:"id"`
	Label         string `json:"label"`
	Enabled       bool   `json:"enabled"`
	Available     bool   `json:"available"`
	Authenticated bool   `json:"authenticated"`
	Detail        string `json:"detail,omitempty"`
	Target        string `json:"target,omitempty"`
	SyncMinutes   int    `json:"sync_minutes,omitempty"`
	RepoTarget    bool   `json:"repo_target,omitempty"`
}

type HookIssue struct {
	ExternalID string   `json:"external_id,omitempty"`
	Number     int      `json:"number,omitempty"`
	Key        string   `json:"key,omitempty"`
	Title      string   `json:"title"`
	Body       string   `json:"body,omitempty"`
	URL        string   `json:"url,omitempty"`
	Labels     []string `json:"labels,omitempty"`
	Assignees  []string `json:"assignees,omitempty"`
	State      string   `json:"state,omitempty"`
	Priority   string   `json:"priority,omitempty"`
	CreatedAt  string   `json:"created_at,omitempty"`
	UpdatedAt  string   `json:"updated_at,omitempty"`
}

type IssueFetch struct {
	Provider string      `json:"provider"`
	Target   string      `json:"target,omitempty"`
	Labels   []string    `json:"labels,omitempty"`
	State    string      `json:"state,omitempty"`
	Limit    int         `json:"limit,omitempty"`
	Query    string      `json:"query,omitempty"`
	Issues   []HookIssue `json:"issues"`
}

type IssueRef struct {
	Provider string `json:"provider,omitempty"`
	Repo     string `json:"repo"`
	Number   int    `json:"number"`
	Key      string `json:"key,omitempty"`
	URL      string `json:"url"`
}

type Card struct {
	ID        string         `json:"id"`
	Title     string         `json:"title"`
	Body      string         `json:"body,omitempty"`
	Column    string         `json:"column"`
	Position  int            `json:"position"`
	Priority  string         `json:"priority,omitempty"`
	Labels    []string       `json:"labels,omitempty"`
	Assignee  string         `json:"assignee,omitempty"`
	Source    string         `json:"source"`
	Issue     *IssueRef      `json:"issue,omitempty"`
	Worktree  *WorktreeEntry `json:"worktree,omitempty"`
	CreatedAt string         `json:"created_at"`
	UpdatedAt string         `json:"updated_at"`
}

type CardSucceeded struct {
	Card                  Card           `json:"card"`
	Worktree              *WorktreeEntry `json:"worktree,omitempty"`
	Context               *RunContext    `json:"context,omitempty"`
	Summary               string         `json:"summary,omitempty"`
	Feedback              bool           `json:"feedback"`
	CreateChangeRequest   bool           `json:"create_change_request"`
	ChangeRequestProvider string         `json:"change_request_provider,omitempty"`
	TransitionOnSuccess   string         `json:"transition_on_success,omitempty"`
	BaseBranch            string         `json:"base_branch,omitempty"`
}

type RunContext struct {
	Kind   string            `json:"kind,omitempty"`
	Path   string            `json:"path,omitempty"`
	Branch string            `json:"branch,omitempty"`
	Base   string            `json:"base,omitempty"`
	Label  string            `json:"label,omitempty"`
	Env    map[string]string `json:"env,omitempty"`
	Meta   map[string]string `json:"meta,omitempty"`
}

type RunPrepare struct {
	Card                  Card       `json:"card"`
	Repo                  string     `json:"repo"`
	BaseBranch            string     `json:"base_branch,omitempty"`
	ChangeRequestProvider string     `json:"change_request_provider,omitempty"`
	Context               RunContext `json:"context"`
}

func (p *Plugin) Hooks() ([]peggplugin.HookSpec, error) {
	return []peggplugin.HookSpec{
		{
			Name:        HookProviders,
			Kind:        peggplugin.HookFilter,
			Priority:    hookPriority,
			Description: "Advertise GitLab as a Kanban issue provider.",
		},
		{
			Name:        HookIssueFetch,
			Kind:        peggplugin.HookFilter,
			Priority:    hookPriority,
			Description: "Fetch GitLab issues for Kanban sync.",
		},
		{
			Name:        HookRunPrepare,
			Kind:        peggplugin.HookFilter,
			Priority:    hookPriority,
			Description: "Create a git worktree for a Kanban card run.",
		},
		{
			Name:        HookCardSucceeded,
			Kind:        peggplugin.HookAction,
			Priority:    hookPriority,
			Description: "Comment on the issue and open a merge request after a Kanban card succeeds.",
		},
	}, nil
}

func (p *Plugin) ExecuteHook(req peggplugin.HookRequest) (*peggplugin.HookResult, error) {
	settings := p.currentSettings()
	result, err := executeHook(settings, req)
	if err != nil {
		return nil, fmt.Errorf("%s", redactSettings(err.Error(), settings))
	}
	return result, nil
}

func executeHook(settings Settings, req peggplugin.HookRequest) (*peggplugin.HookResult, error) {
	switch req.Name {
	case HookProviders:
		return hookProviders(settings, req)
	case HookIssueFetch:
		return hookIssueFetch(settings, req)
	case HookRunPrepare:
		return hookRunPrepare(settings, req)
	case HookCardSucceeded:
		return hookCardSucceeded(settings, req)
	default:
		return &peggplugin.HookResult{Handled: false}, nil
	}
}

func hookProviders(settings Settings, req peggplugin.HookRequest) (*peggplugin.HookResult, error) {
	providers := []ProviderInfo{}
	if len(req.Value) > 0 && string(req.Value) != "null" {
		if err := json.Unmarshal(req.Value, &providers); err != nil {
			return nil, fmt.Errorf("decode %s payload: %w", HookProviders, err)
		}
	}
	if !settings.Enabled {
		return &peggplugin.HookResult{Handled: false}, nil
	}

	info := providerInfo(settings)
	replaced := false
	for i := range providers {
		if providers[i].ID == pluginName {
			providers[i] = info
			replaced = true
			break
		}
	}
	if !replaced {
		providers = append(providers, info)
	}
	value, err := json.Marshal(providers)
	if err != nil {
		return nil, fmt.Errorf("encode %s payload: %w", HookProviders, err)
	}
	return &peggplugin.HookResult{Value: value, Handled: true}, nil
}

func providerInfo(settings Settings) ProviderInfo {
	authenticated := settings.Token != ""
	target := normalizeProject(settings.Project)
	available := settings.Enabled && authenticated
	return ProviderInfo{
		ID:            pluginName,
		Label:         pluginDisplay,
		Enabled:       settings.Enabled,
		Available:     available,
		Authenticated: authenticated,
		Detail:        providerDetail(settings, authenticated, target),
		Target:        target,
		RepoTarget:    true,
	}
}

func providerDetail(settings Settings, authenticated bool, target string) string {
	switch {
	case !settings.Enabled:
		return "GitLab is disabled; enable the plugin to sync issues."
	case !authenticated:
		return "Set PEGG_GITLAB_TOKEN or plugins.options.kanban-gitlab.token to sync issues."
	case target == "":
		return "Set PEGG_GITLAB_PROJECT or plugins.options.kanban-gitlab.project to sync issues."
	default:
		return "Ready to sync issues from " + target + "."
	}
}

func hookIssueFetch(settings Settings, req peggplugin.HookRequest) (*peggplugin.HookResult, error) {
	var payload IssueFetch
	if len(req.Value) > 0 && string(req.Value) != "null" {
		if err := json.Unmarshal(req.Value, &payload); err != nil {
			return nil, fmt.Errorf("decode %s payload: %w", HookIssueFetch, err)
		}
	}
	if !settings.Enabled || payload.Provider != pluginName {
		return &peggplugin.HookResult{Handled: false}, nil
	}

	settings.Project = hookTarget(payload.Target, settings.Project)
	if settings.Project == "" {
		return &peggplugin.HookResult{Handled: false}, nil
	}
	payload.Target = settings.Project
	payload.State = hookDefault(payload.State, settings.SyncState)
	if len(payload.Labels) == 0 {
		payload.Labels = settings.SyncLabels
	}
	if payload.Limit <= 0 {
		payload.Limit = settings.SyncLimit
	}

	ctx, cancel := hookContext(req, settings)
	defer cancel()

	issues, err := listIssues(ctx, newClient(settings), payload.State, strings.Join(payload.Labels, ","), payload.Query, payload.Limit)
	if err != nil {
		return nil, err
	}
	if payload.Issues == nil {
		payload.Issues = []HookIssue{}
	}
	for _, issue := range issues {
		payload.Issues = append(payload.Issues, HookIssue{
			Number:    issue.IID,
			Title:     issue.Title,
			Body:      issue.Description,
			URL:       issue.WebURL,
			Labels:    issue.Labels,
			Assignees: issue.Assignees,
			State:     issue.State,
			CreatedAt: issue.CreatedAt,
			UpdatedAt: issue.UpdatedAt,
		})
	}
	value, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("encode %s payload: %w", HookIssueFetch, err)
	}
	return &peggplugin.HookResult{Value: value, Handled: true}, nil
}

func hookRunPrepare(settings Settings, req peggplugin.HookRequest) (*peggplugin.HookResult, error) {
	var payload RunPrepare
	if len(req.Value) > 0 && string(req.Value) != "null" {
		if err := json.Unmarshal(req.Value, &payload); err != nil {
			return nil, fmt.Errorf("decode %s payload: %w", HookRunPrepare, err)
		}
	}
	if !settings.Enabled || strings.TrimSpace(payload.Repo) == "" || !handlesChangeRequest(payload.ChangeRequestProvider) || !isGitRepo(payload.Repo) {
		return &peggplugin.HookResult{Handled: false}, nil
	}

	base := strings.TrimSpace(payload.BaseBranch)
	if base == "" {
		base = strings.TrimSpace(settings.DefaultTargetBranch)
	}
	issue := 0
	if payload.Card.Issue != nil {
		issue = payload.Card.Issue.Number
	}
	slug := worktreeSlugForCard(payload.Card)
	manager, err := NewWorktreeManager(payload.Repo, settings.WorktreeRoot, settings.DefaultTargetBranch)
	if err != nil {
		return nil, err
	}
	entry, err := manager.Create(CreateWorktreeOptions{
		Slug:   slug,
		Branch: "pegg/" + slug,
		Base:   base,
		Issue:  issue,
		CardID: payload.Card.ID,
	})
	if err != nil {
		return nil, err
	}
	payload.Context = RunContext{
		Kind:   "worktree",
		Path:   entry.Path,
		Branch: entry.Branch,
		Base:   entry.Base,
		Label:  entry.Slug,
	}
	value, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("encode %s payload: %w", HookRunPrepare, err)
	}
	return &peggplugin.HookResult{Value: value, Handled: true}, nil
}

func worktreeSlugForCard(card Card) string {
	base := slugify(card.Title)
	if base == "" {
		base = "card"
	}
	if len(base) > 50 {
		base = strings.Trim(base[:50], "-")
	}
	suffix := card.ID
	if idx := strings.LastIndex(card.ID, "-"); idx >= 0 && idx+1 < len(card.ID) {
		suffix = card.ID[idx+1:]
	}
	if len(suffix) > 8 {
		suffix = suffix[:8]
	}
	slug := slugify(base + "-" + suffix)
	if slug == "" {
		slug = "card"
	}
	return slug
}

func hookCardSucceeded(settings Settings, req peggplugin.HookRequest) (*peggplugin.HookResult, error) {
	var payload CardSucceeded
	if len(req.Value) > 0 && string(req.Value) != "null" {
		if err := json.Unmarshal(req.Value, &payload); err != nil {
			return nil, fmt.Errorf("decode %s payload: %w", HookCardSucceeded, err)
		}
	}
	if !settings.Enabled {
		return &peggplugin.HookResult{Handled: false}, nil
	}
	issueProvider := ""
	if payload.Card.Issue != nil {
		issueProvider = strings.TrimSpace(payload.Card.Issue.Provider)
	}
	isIssueProvider := strings.EqualFold(issueProvider, pluginName)
	isChangeRequestTarget := changeRequestTarget(payload.ChangeRequestProvider, isIssueProvider)
	if !isIssueProvider && !isChangeRequestTarget {
		return &peggplugin.HookResult{Handled: false}, nil
	}

	repo := ""
	if isIssueProvider && payload.Card.Issue != nil {
		repo = payload.Card.Issue.Repo
	}
	settings.Project = hookTarget(repo, settings.Project)
	if settings.Project == "" {
		return &peggplugin.HookResult{Handled: false}, nil
	}

	ctx, cancel := hookContext(req, settings)
	defer cancel()
	client := newClient(settings)

	if isIssueProvider && payload.Feedback && strings.TrimSpace(payload.Summary) != "" && payload.Card.Issue.Number > 0 {
		if _, err := commentOnIssue(ctx, client, payload.Card.Issue.Number, payload.Summary); err != nil {
			return nil, err
		}
	}

	if isChangeRequestTarget && payload.CreateChangeRequest {
		branch := changeRequestBranch(payload)
		if branch != "" {
			targetBranch := changeRequestBase(payload, settings)
			_, err := createMergeRequest(ctx, client, mrCreateInput{
				SourceBranch: branch,
				TargetBranch: targetBranch,
				Title:        payload.Card.Title,
				Description:  changeRequestBody(payload.Card.Body, payload.Summary),
			})
			if err != nil {
				return nil, err
			}
		}
	}

	return &peggplugin.HookResult{Handled: true}, nil
}

func handlesChangeRequest(provider string) bool {
	provider = strings.TrimSpace(provider)
	if provider == "" {
		return true
	}
	return strings.EqualFold(provider, pluginName)
}

func isGitRepo(path string) bool {
	path = strings.TrimSpace(path)
	if path == "" {
		return false
	}
	_, err := gitOutput(path, "rev-parse", "--show-toplevel")
	return err == nil
}

func changeRequestTarget(provider string, isIssueProvider bool) bool {
	provider = strings.TrimSpace(provider)
	if provider == "" {
		return isIssueProvider
	}
	return strings.EqualFold(provider, pluginName)
}

func changeRequestBranch(payload CardSucceeded) string {
	if payload.Worktree != nil {
		if branch := strings.TrimSpace(payload.Worktree.Branch); branch != "" {
			return branch
		}
	}
	if payload.Context != nil {
		return strings.TrimSpace(payload.Context.Branch)
	}
	return ""
}

func changeRequestBase(payload CardSucceeded, settings Settings) string {
	if payload.Worktree != nil {
		if base := strings.TrimSpace(payload.Worktree.Base); base != "" {
			return base
		}
	}
	if payload.Context != nil {
		if base := strings.TrimSpace(payload.Context.Base); base != "" {
			return base
		}
	}
	if base := strings.TrimSpace(payload.BaseBranch); base != "" {
		return base
	}
	if base := strings.TrimSpace(settings.DefaultTargetBranch); base != "" {
		return base
	}
	return "main"
}

func hookTarget(request, fallback string) string {
	if target := normalizeProject(request); target != "" {
		return target
	}
	return normalizeProject(fallback)
}

func hookDefault(value, fallback string) string {
	if value = strings.TrimSpace(value); value != "" {
		return value
	}
	return fallback
}

func hookContext(req peggplugin.HookRequest, settings Settings) (context.Context, context.CancelFunc) {
	timeout := settings.timeout()
	if req.TimeoutMS > 0 {
		timeout = time.Duration(req.TimeoutMS) * time.Millisecond
	}
	return context.WithTimeout(context.Background(), timeout)
}

func changeRequestBody(body, summary string) string {
	body = strings.TrimSpace(body)
	summary = strings.TrimSpace(summary)
	switch {
	case body == "":
		return summary
	case summary == "":
		return body
	default:
		return body + "\n\n" + summary
	}
}
