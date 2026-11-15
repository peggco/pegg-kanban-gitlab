package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	indexVersion  = 1
	lockRetry     = 25 * time.Millisecond
	lockTimeout   = 2 * time.Second
	staleLockAge  = 10 * time.Second
	defaultRoot   = ".pegg-worktrees"
	excludeMarker = ".pegg-worktrees/"
)

type WorktreeEntry struct {
	Slug      string `json:"slug,omitempty"`
	Path      string `json:"path"`
	Repo      string `json:"repo,omitempty"`
	Branch    string `json:"branch"`
	Base      string `json:"base,omitempty"`
	Issue     int    `json:"issue,omitempty"`
	CardID    string `json:"card_id,omitempty"`
	CreatedAt string `json:"created_at,omitempty"`
	PR        int    `json:"pr,omitempty"`
	PRURL     string `json:"pr_url,omitempty"`
}

type WorktreeListEntry struct {
	WorktreeEntry
	Main bool `json:"main"`
}

type worktreeIndex struct {
	Version   int             `json:"version"`
	Worktrees []WorktreeEntry `json:"worktrees"`
}

type WorktreeManager struct {
	Repo        string
	Root        string
	DefaultBase string
}

type CreateWorktreeOptions struct {
	Slug   string
	Branch string
	Base   string
	Issue  int
	CardID string
}

type WorktreeStatus struct {
	Slug     string `json:"slug"`
	Path     string `json:"path"`
	Branch   string `json:"branch"`
	Head     string `json:"head,omitempty"`
	Last     string `json:"last_commit,omitempty"`
	Dirty    bool   `json:"dirty"`
	Changes  int    `json:"changes"`
	Indexed  bool   `json:"indexed"`
	Exists   bool   `json:"exists"`
	MainTree bool   `json:"main"`
}

func NewWorktreeManager(repo, root, defaultBase string) (*WorktreeManager, error) {
	repo = strings.TrimSpace(repo)
	if repo == "" {
		wd, err := os.Getwd()
		if err != nil {
			return nil, fmt.Errorf("resolve worktree repo: %w", err)
		}
		repo = wd
	}
	abs, err := filepath.Abs(expandHome(repo))
	if err != nil {
		return nil, fmt.Errorf("resolve worktree repo: %w", err)
	}
	out, err := gitOutput(abs, "rev-parse", "--show-toplevel")
	if err != nil {
		return nil, fmt.Errorf("not a git repository: %s", abs)
	}
	abs = strings.TrimSpace(out)
	if abs == "" {
		abs = repo
	}
	if strings.TrimSpace(root) == "" {
		root = defaultRoot
	}
	root = expandHome(root)
	if !filepath.IsAbs(root) {
		root = filepath.Join(abs, root)
	}
	return &WorktreeManager{
		Repo:        abs,
		Root:        filepath.Clean(root),
		DefaultBase: strings.TrimSpace(defaultBase),
	}, nil
}

func (m *WorktreeManager) indexPath() string { return filepath.Join(m.Root, "index.json") }
func (m *WorktreeManager) lockPath() string  { return filepath.Join(m.Root, "index.lock") }
func (m *WorktreeManager) indexTempPath() string {
	return filepath.Join(m.Root, "index.json.tmp")
}

func (m *WorktreeManager) Create(opts CreateWorktreeOptions) (*WorktreeEntry, error) {
	if err := m.ensureRoot(); err != nil {
		return nil, err
	}
	if err := m.ensureExclude(); err != nil {
		return nil, err
	}
	slug := slugify(opts.Slug)
	if slug == "" {
		slug = slugify(opts.Branch)
	}
	if slug == "" {
		slug = fmt.Sprintf("pegg-worktree-%d", time.Now().UnixNano())
	}
	branch := strings.TrimSpace(opts.Branch)
	if branch == "" {
		branch = "pegg/" + slug
	}
	path := filepath.Join(m.Root, slug)

	index, err := m.loadIndex()
	if err != nil {
		return nil, err
	}
	if existing, ok := findEntry(index, slug); ok {
		if stat, statErr := os.Stat(path); statErr == nil && stat.IsDir() && !isEmptyDir(path) {
			if existing.Path == path {
				return &existing, nil
			}
			return nil, fmt.Errorf("worktree slug %q is already registered at %s", slug, existing.Path)
		}
	}
	if stat, statErr := os.Stat(path); statErr == nil && stat.IsDir() && !isEmptyDir(path) {
		return nil, fmt.Errorf("worktree path already exists and is not empty: %s", path)
	}

	base := ""
	branchExists := gitOK(m.Repo, "show-ref", "--verify", "--quiet", "refs/heads/"+branch)
	if branchExists {
		if _, err := gitOutput(m.Repo, "worktree", "add", path, branch); err != nil {
			return nil, err
		}
	} else {
		base = m.resolveBase(opts.Base)
		if _, err := gitOutput(m.Repo, "worktree", "add", "-b", branch, path, base); err != nil {
			return nil, err
		}
	}

	entry := WorktreeEntry{
		Slug:      slug,
		Path:      path,
		Repo:      m.Repo,
		Branch:    branch,
		Base:      base,
		Issue:     opts.Issue,
		CardID:    strings.TrimSpace(opts.CardID),
		CreatedAt: time.Now().UTC().Format(time.RFC3339),
	}
	err = m.withIndex(func(index *worktreeIndex) error {
		replaced := false
		for i := range index.Worktrees {
			if index.Worktrees[i].Slug == slug {
				index.Worktrees[i] = entry
				replaced = true
				break
			}
		}
		if !replaced {
			index.Worktrees = append(index.Worktrees, entry)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &entry, nil
}

func (m *WorktreeManager) List() ([]WorktreeListEntry, error) {
	gitEntries, err := m.gitWorktrees()
	if err != nil {
		return nil, err
	}
	index, err := m.loadIndex()
	if err != nil {
		return nil, err
	}
	byPath := map[string]WorktreeEntry{}
	for _, entry := range index.Worktrees {
		byPath[filepath.Clean(entry.Path)] = entry
	}

	out := make([]WorktreeListEntry, 0, len(gitEntries)+len(index.Worktrees))
	seen := map[string]bool{}
	for i, gitEntry := range gitEntries {
		entry := WorktreeEntry{
			Path:   gitEntry.Path,
			Repo:   m.Repo,
			Branch: gitEntry.Branch,
		}
		if indexed, ok := byPath[filepath.Clean(gitEntry.Path)]; ok {
			entry = indexed
			entry.Path = gitEntry.Path
			if gitEntry.Branch != "" {
				entry.Branch = gitEntry.Branch
			}
		}
		if entry.Slug == "" && i > 0 {
			entry.Slug = slugify(filepath.Base(gitEntry.Path))
		}
		out = append(out, WorktreeListEntry{WorktreeEntry: entry, Main: i == 0})
		seen[filepath.Clean(gitEntry.Path)] = true
	}
	for _, entry := range index.Worktrees {
		if seen[filepath.Clean(entry.Path)] {
			continue
		}
		out = append(out, WorktreeListEntry{WorktreeEntry: entry, Main: false})
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Main != out[j].Main {
			return out[i].Main
		}
		return out[i].Path < out[j].Path
	})
	return out, nil
}

func (m *WorktreeManager) Get(slug string) (*WorktreeListEntry, error) {
	slug = strings.TrimSpace(slug)
	if slug == "" {
		return nil, fmt.Errorf("slug is required")
	}
	entries, err := m.List()
	if err != nil {
		return nil, err
	}
	for i := range entries {
		if entries[i].Slug == slug || filepath.Base(entries[i].Path) == slug {
			return &entries[i], nil
		}
	}
	return nil, fmt.Errorf("worktree not found: %s", slug)
}

func (m *WorktreeManager) Remove(slug string, deleteBranch bool) error {
	slug = strings.TrimSpace(slug)
	if slug == "" {
		return fmt.Errorf("slug is required")
	}
	index, err := m.loadIndex()
	if err != nil {
		return err
	}
	entry, ok := findEntry(index, slug)
	if !ok {
		return fmt.Errorf("worktree not found: %s", slug)
	}
	if _, err := gitOutput(m.Repo, "worktree", "remove", "--force", entry.Path); err != nil {
		if _, pruneErr := gitOutput(m.Repo, "worktree", "prune"); pruneErr != nil {
			return err
		}
	}
	if deleteBranch && strings.TrimSpace(entry.Branch) != "" {
		_, _ = gitOutput(m.Repo, "branch", "-D", entry.Branch)
	}
	return m.withIndex(func(index *worktreeIndex) error {
		filtered := index.Worktrees[:0]
		for _, item := range index.Worktrees {
			if item.Slug == slug {
				continue
			}
			filtered = append(filtered, item)
		}
		index.Worktrees = filtered
		return nil
	})
}

func (m *WorktreeManager) Status(slug string) (*WorktreeStatus, error) {
	entry, err := m.Get(slug)
	if err != nil {
		return nil, err
	}
	status := &WorktreeStatus{
		Slug:     entry.Slug,
		Path:     entry.Path,
		Branch:   entry.Branch,
		Indexed:  entry.Slug != "",
		MainTree: entry.Main,
	}
	if abs, err := filepath.Abs(entry.Path); err == nil {
		status.Path = abs
	}
	if info, err := os.Stat(entry.Path); err == nil && info.IsDir() {
		status.Exists = true
	}
	if !status.Exists {
		return status, nil
	}
	if out, err := gitOutput(entry.Path, "status", "--porcelain"); err == nil {
		lines := 0
		for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
			if strings.TrimSpace(line) != "" {
				lines++
			}
		}
		status.Changes = lines
		status.Dirty = lines > 0
	}
	if out, err := gitOutput(entry.Path, "rev-parse", "--short", "HEAD"); err == nil {
		status.Head = strings.TrimSpace(out)
	}
	if out, err := gitOutput(entry.Path, "log", "-1", "--format=%s"); err == nil {
		status.Last = strings.TrimSpace(out)
	}
	return status, nil
}

func (m *WorktreeManager) resolveBase(explicit string) string {
	if base := strings.TrimSpace(explicit); base != "" {
		return base
	}
	if base := strings.TrimSpace(m.DefaultBase); base != "" {
		return base
	}
	if out, err := gitOutput(m.Repo, "symbolic-ref", "--short", "HEAD"); err == nil {
		if base := strings.TrimSpace(out); base != "" {
			return base
		}
	}
	if out, err := gitOutput(m.Repo, "symbolic-ref", "--short", "refs/remotes/origin/HEAD"); err == nil {
		if base := strings.TrimSpace(out); base != "" {
			return base
		}
	}
	for _, candidate := range []string{"main", "master"} {
		if gitOK(m.Repo, "show-ref", "--verify", "--quiet", "refs/heads/"+candidate) {
			return candidate
		}
	}
	return "HEAD"
}

func (m *WorktreeManager) ensureRoot() error {
	if err := os.MkdirAll(m.Root, 0o755); err != nil {
		return fmt.Errorf("create worktree root: %w", err)
	}
	return nil
}

func (m *WorktreeManager) ensureExclude() error {
	gitDir, err := gitOutput(m.Repo, "rev-parse", "--absolute-git-dir")
	if err != nil || strings.TrimSpace(gitDir) == "" {
		gitDir = filepath.Join(m.Repo, ".git")
	}
	exclude := filepath.Join(strings.TrimSpace(gitDir), "info", "exclude")
	if err := os.MkdirAll(filepath.Dir(exclude), 0o755); err != nil {
		return fmt.Errorf("create git exclude directory: %w", err)
	}
	data, err := os.ReadFile(exclude)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("read git exclude: %w", err)
	}
	for _, line := range strings.Split(string(data), "\n") {
		if strings.TrimSpace(line) == excludeMarker {
			return nil
		}
	}
	file, err := os.OpenFile(exclude, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("open git exclude: %w", err)
	}
	defer file.Close()
	prefix := ""
	if len(data) > 0 && !strings.HasSuffix(string(data), "\n") {
		prefix = "\n"
	}
	if _, err := file.WriteString(prefix + excludeMarker + "\n"); err != nil {
		return fmt.Errorf("write git exclude: %w", err)
	}
	return nil
}

func (m *WorktreeManager) loadIndex() (*worktreeIndex, error) {
	data, err := os.ReadFile(m.indexPath())
	if err != nil {
		if os.IsNotExist(err) {
			return &worktreeIndex{Version: indexVersion}, nil
		}
		return nil, fmt.Errorf("read worktree index: %w", err)
	}
	index := &worktreeIndex{}
	if err := json.Unmarshal(data, index); err != nil {
		return nil, fmt.Errorf("parse worktree index: %w", err)
	}
	if index.Version == 0 {
		index.Version = indexVersion
	}
	return index, nil
}

func (m *WorktreeManager) saveIndex(index *worktreeIndex) error {
	if index.Version == 0 {
		index.Version = indexVersion
	}
	if index.Worktrees == nil {
		index.Worktrees = []WorktreeEntry{}
	}
	data, err := json.MarshalIndent(index, "", "  ")
	if err != nil {
		return fmt.Errorf("encode worktree index: %w", err)
	}
	data = append(data, '\n')
	tmp := m.indexTempPath()
	file, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("write worktree index: %w", err)
	}
	if _, err := file.Write(data); err != nil {
		file.Close()
		_ = os.Remove(tmp)
		return fmt.Errorf("write worktree index: %w", err)
	}
	if err := file.Sync(); err != nil {
		file.Close()
		_ = os.Remove(tmp)
		return fmt.Errorf("sync worktree index: %w", err)
	}
	if err := file.Close(); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("close worktree index: %w", err)
	}
	if err := os.Rename(tmp, m.indexPath()); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("replace worktree index: %w", err)
	}
	return nil
}

func (m *WorktreeManager) withIndex(fn func(*worktreeIndex) error) error {
	if err := os.MkdirAll(m.Root, 0o755); err != nil {
		return fmt.Errorf("create worktree root: %w", err)
	}
	unlock, err := acquireLock(m.lockPath())
	if err != nil {
		return err
	}
	defer unlock()
	index, err := m.loadIndex()
	if err != nil {
		return err
	}
	if err := fn(index); err != nil {
		return err
	}
	return m.saveIndex(index)
}

func acquireLock(path string) (func(), error) {
	deadline := time.Now().Add(lockTimeout)
	for {
		file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
		if err == nil {
			_, _ = file.WriteString(strconv.Itoa(os.Getpid()) + "\n")
			_ = file.Close()
			return func() { _ = os.Remove(path) }, nil
		}
		if !os.IsExist(err) {
			return nil, fmt.Errorf("lock worktree index: %w", err)
		}
		if info, statErr := os.Stat(path); statErr == nil && time.Since(info.ModTime()) > staleLockAge {
			_ = os.Remove(path)
			continue
		}
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("worktree index is locked: %s", path)
		}
		time.Sleep(lockRetry)
	}
}

type gitWorktreeEntry struct {
	Path   string
	Branch string
}

func (m *WorktreeManager) gitWorktrees() ([]gitWorktreeEntry, error) {
	out, err := gitOutput(m.Repo, "worktree", "list", "--porcelain")
	if err != nil {
		return nil, err
	}
	var entries []gitWorktreeEntry
	var current gitWorktreeEntry
	flush := func() {
		if current.Path != "" {
			entries = append(entries, current)
		}
		current = gitWorktreeEntry{}
	}
	scanner := bufio.NewScanner(strings.NewReader(out))
	for scanner.Scan() {
		line := scanner.Text()
		switch {
		case strings.HasPrefix(line, "worktree "):
			flush()
			current.Path = strings.TrimPrefix(line, "worktree ")
		case strings.HasPrefix(line, "branch "):
			ref := strings.TrimPrefix(line, "branch ")
			current.Branch = strings.TrimPrefix(ref, "refs/heads/")
		case strings.TrimSpace(line) == "":
			flush()
		}
	}
	flush()
	return entries, nil
}

func findEntry(index *worktreeIndex, slug string) (WorktreeEntry, bool) {
	for _, entry := range index.Worktrees {
		if entry.Slug == slug {
			return entry, true
		}
	}
	return WorktreeEntry{}, false
}

func isEmptyDir(path string) bool {
	file, err := os.Open(path)
	if err != nil {
		return false
	}
	defer file.Close()
	names, err := file.Readdirnames(1)
	if err != nil {
		return true
	}
	return len(names) == 0
}

var (
	reSlugInvalid = regexp.MustCompile(`[^a-z0-9]+`)
	reSlugTrim    = regexp.MustCompile(`^-+|-+$`)
)

func slugify(value string) string {
	slug := strings.ToLower(strings.TrimSpace(value))
	slug = strings.ReplaceAll(slug, "/", "-")
	slug = strings.ReplaceAll(slug, "_", "-")
	slug = reSlugInvalid.ReplaceAllString(slug, "-")
	slug = reSlugTrim.ReplaceAllString(slug, "")
	slug = strings.Trim(slug, "-")
	if len(slug) > 60 {
		slug = strings.Trim(slug[:60], "-")
	}
	return slug
}

func gitOutput(dir string, args ...string) (string, error) {
	full := append([]string{"-C", dir}, args...)
	cmd := exec.Command("git", full...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return string(out), redactError(fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(string(out))))
	}
	return string(out), nil
}

func gitOK(dir string, args ...string) bool {
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	return cmd.Run() == nil
}
