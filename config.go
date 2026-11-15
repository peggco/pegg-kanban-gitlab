package main

import (
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const (
	defaultBaseURL = "https://gitlab.com/api/v4"
)

type Settings struct {
	Enabled               bool     `json:"enabled"`
	Token                 string   `json:"token"`
	BaseURL               string   `json:"base_url"`
	Project               string   `json:"project"`
	DefaultTargetBranch   string   `json:"default_target_branch"`
	WorktreeRoot          string   `json:"worktree_root"`
	SyncState             string   `json:"sync_state"`
	SyncLabels            []string `json:"sync_labels"`
	SyncLimit             int      `json:"sync_limit"`
	RequestTimeoutSeconds int      `json:"request_timeout_seconds"`
	Workdir               string   `json:"-"`
	TokenSource           string   `json:"-"`
}

func defaultSettings() Settings {
	wd, _ := os.Getwd()
	return Settings{
		Enabled:               true,
		BaseURL:               defaultBaseURL,
		DefaultTargetBranch:   "main",
		WorktreeRoot:          ".pegg-worktrees",
		SyncState:             "opened",
		SyncLimit:             50,
		RequestTimeoutSeconds: 30,
		Workdir:               wd,
	}
}

func (s *Settings) applyHost(configJSON []byte) {
	if len(configJSON) == 0 {
		return
	}
	var host struct {
		Plugins struct {
			Options map[string]json.RawMessage `json:"options"`
		} `json:"plugins"`
	}
	if err := json.Unmarshal(configJSON, &host); err != nil {
		return
	}
	for _, key := range []string{"gitlab", "pegg-gitlab", "pegg-kanban-gitlab", "kanban-gitlab"} {
		raw := host.Plugins.Options[key]
		if len(raw) == 0 || string(raw) == "null" {
			continue
		}
		_ = json.Unmarshal(raw, s)
	}
}

func (s *Settings) applyOptions(options json.RawMessage) error {
	if len(options) == 0 || string(options) == "null" {
		return nil
	}
	if err := json.Unmarshal(options, s); err != nil {
		return fmt.Errorf("parse %s options: %w", pluginName, err)
	}
	return nil
}

func (s *Settings) applyEnv() {
	if v := strings.TrimSpace(os.Getenv("PEGG_GITLAB_TOKEN")); v != "" {
		s.Token = v
		s.TokenSource = "PEGG_GITLAB_TOKEN"
	}
	if s.Token == "" {
		for _, name := range []string{"GITLAB_TOKEN", "CI_JOB_TOKEN"} {
			if v := strings.TrimSpace(os.Getenv(name)); v != "" {
				s.Token = v
				s.TokenSource = name
				break
			}
		}
	}
	if v := strings.TrimSpace(os.Getenv("PEGG_GITLAB_URL")); v != "" {
		s.BaseURL = v
	}
	if v := strings.TrimSpace(os.Getenv("PEGG_GITLAB_PROJECT")); v != "" {
		s.Project = v
	}
	if v := strings.TrimSpace(os.Getenv("PEGG_GITLAB_WORKTREE_ROOT")); v != "" {
		s.WorktreeRoot = v
	}
	if v := strings.TrimSpace(os.Getenv("PEGG_GITLAB_TIMEOUT_SECONDS")); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			s.RequestTimeoutSeconds = n
		}
	}
	if v := strings.TrimSpace(os.Getenv("PEGG_GITLAB_DISABLED")); v != "" {
		if disabled, err := strconv.ParseBool(v); err == nil {
			s.Enabled = !disabled
		}
	}
	s.normalize()
}

func (s *Settings) normalize() {
	s.BaseURL = normalizeBaseURL(s.BaseURL)
	s.Token = strings.TrimSpace(s.Token)
	s.Project = normalizeProject(s.Project)
	s.DefaultTargetBranch = strings.TrimSpace(s.DefaultTargetBranch)
	if s.DefaultTargetBranch == "" {
		s.DefaultTargetBranch = "main"
	}
	if strings.TrimSpace(s.WorktreeRoot) == "" {
		s.WorktreeRoot = ".pegg-worktrees"
	}
	s.WorktreeRoot = expandHome(s.WorktreeRoot)
	s.SyncState = strings.TrimSpace(s.SyncState)
	if s.SyncState == "" {
		s.SyncState = "opened"
	}
	if s.SyncLimit <= 0 {
		s.SyncLimit = 50
	}
	if s.RequestTimeoutSeconds <= 0 {
		s.RequestTimeoutSeconds = 30
	}
	if strings.TrimSpace(s.Workdir) == "" {
		s.Workdir, _ = os.Getwd()
	}
	s.Workdir = expandHome(s.Workdir)
}

func normalizeBaseURL(value string) string {
	value = strings.TrimRight(strings.TrimSpace(value), "/")
	if value == "" {
		return defaultBaseURL
	}
	parsed, err := url.Parse(value)
	if err != nil {
		return value
	}
	if parsed.Path == "" || parsed.Path == "/" {
		return strings.TrimRight(parsed.Scheme+"://"+parsed.Host, "/") + "/api/v4"
	}
	return value
}

func normalizeProject(value string) string {
	value = strings.TrimSpace(value)
	value = strings.TrimPrefix(value, "/")
	return strings.TrimSuffix(value, "/")
}

func (s Settings) timeout() time.Duration {
	if s.RequestTimeoutSeconds <= 0 {
		return 30 * time.Second
	}
	return time.Duration(s.RequestTimeoutSeconds) * time.Second
}

func expandHome(path string) string {
	if path == "~" || strings.HasPrefix(path, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, strings.TrimPrefix(path, "~"))
		}
	}
	return path
}

func redactSettings(msg string, s Settings) string {
	if s.Token != "" {
		msg = strings.ReplaceAll(msg, s.Token, "[redacted]")
	}
	return msg
}

func redactError(err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("%s", err.Error())
}
