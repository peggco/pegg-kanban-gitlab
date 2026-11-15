package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func initGitRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	mustGit(t, dir, "init", "-b", "main")
	mustGit(t, dir, "config", "user.name", "Pegg Test")
	mustGit(t, dir, "config", "user.email", "pegg@example.com")
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("# test\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	mustGit(t, dir, "add", "README.md")
	mustGit(t, dir, "commit", "-m", "initial commit")
	return dir
}

func mustGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v: %s", strings.Join(args, " "), err, out)
	}
	return string(out)
}

func TestWorktreeCreateListRemove(t *testing.T) {
	repo := initGitRepo(t)
	manager, err := NewWorktreeManager(repo, ".pegg-worktrees", "main")
	if err != nil {
		t.Fatal(err)
	}
	entry, err := manager.Create(CreateWorktreeOptions{
		Slug:   "Issue 42: Fix Login!",
		Issue:  42,
		CardID: "card-abc",
	})
	if err != nil {
		t.Fatal(err)
	}
	if entry.Slug != "issue-42-fix-login" {
		t.Fatalf("slug = %q", entry.Slug)
	}
	wantPath := filepath.Join(repo, ".pegg-worktrees", "issue-42-fix-login")
	if entry.Path != wantPath {
		t.Fatalf("path = %q, want %q", entry.Path, wantPath)
	}
	if entry.Branch != "pegg/issue-42-fix-login" {
		t.Fatalf("branch = %q", entry.Branch)
	}
	if entry.Base != "main" {
		t.Fatalf("base = %q", entry.Base)
	}
	if entry.Issue != 42 || entry.CardID != "card-abc" {
		t.Fatalf("metadata = %#v", entry)
	}
	if info, err := os.Stat(entry.Path); err != nil || !info.IsDir() {
		t.Fatalf("worktree path missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(entry.Path, "README.md")); err != nil {
		t.Fatalf("worktree checkout missing file: %v", err)
	}

	exclude, err := os.ReadFile(filepath.Join(repo, ".git", "info", "exclude"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(exclude), ".pegg-worktrees/") {
		t.Fatalf("exclude = %q", exclude)
	}

	indexPath := filepath.Join(repo, ".pegg-worktrees", "index.json")
	data, err := os.ReadFile(indexPath)
	if err != nil {
		t.Fatal(err)
	}
	var index worktreeIndex
	if err := json.Unmarshal(data, &index); err != nil {
		t.Fatal(err)
	}
	if index.Version != 1 || len(index.Worktrees) != 1 || index.Worktrees[0].Slug != entry.Slug {
		t.Fatalf("index = %#v", index)
	}
	if _, err := os.Stat(indexPath + ".tmp"); !os.IsNotExist(err) {
		t.Fatalf("temporary index file left behind: %v", err)
	}

	entries, err := manager.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 {
		t.Fatalf("entries = %#v", entries)
	}
	if !entries[0].Main || entries[0].Path != repo {
		t.Fatalf("main entry = %#v", entries[0])
	}
	if entries[1].Main || entries[1].Slug != entry.Slug {
		t.Fatalf("managed entry = %#v", entries[1])
	}

	status, err := manager.Status(entry.Slug)
	if err != nil {
		t.Fatal(err)
	}
	if !status.Exists || status.Branch != entry.Branch || status.Dirty {
		t.Fatalf("status = %#v", status)
	}
	if status.Head == "" {
		t.Fatalf("status head missing: %#v", status)
	}

	if err := manager.Remove(entry.Slug, true); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(entry.Path); !os.IsNotExist(err) {
		t.Fatalf("worktree path still exists: %v", err)
	}
	data, err = os.ReadFile(indexPath)
	if err != nil {
		t.Fatal(err)
	}
	index = worktreeIndex{}
	if err := json.Unmarshal(data, &index); err != nil {
		t.Fatal(err)
	}
	if len(index.Worktrees) != 0 {
		t.Fatalf("index after remove = %#v", index)
	}
	if cmd := exec.Command("git", "-C", repo, "show-ref", "--verify", "--quiet", "refs/heads/"+entry.Branch); cmd.Run() == nil {
		t.Fatalf("branch %s still exists", entry.Branch)
	}
	if err := manager.Remove(entry.Slug, false); err == nil {
		t.Fatal("removing a missing worktree should fail")
	}
}

func TestWorktreeCreateDuplicateReturnsExisting(t *testing.T) {
	repo := initGitRepo(t)
	manager, err := NewWorktreeManager(repo, ".pegg-worktrees", "main")
	if err != nil {
		t.Fatal(err)
	}
	first, err := manager.Create(CreateWorktreeOptions{Slug: "dup", Branch: "pegg/dup", Base: "main"})
	if err != nil {
		t.Fatal(err)
	}
	second, err := manager.Create(CreateWorktreeOptions{Slug: "dup", Branch: "pegg/dup", Base: "main"})
	if err != nil {
		t.Fatal(err)
	}
	if first.Path != second.Path || first.Branch != second.Branch {
		t.Fatalf("first = %#v second = %#v", first, second)
	}
	entries, err := manager.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 {
		t.Fatalf("entries = %#v", entries)
	}
}

func TestWorktreeCreateWithExistingBranch(t *testing.T) {
	repo := initGitRepo(t)
	mustGit(t, repo, "branch", "feature/login")
	manager, err := NewWorktreeManager(repo, ".pegg-worktrees", "main")
	if err != nil {
		t.Fatal(err)
	}
	entry, err := manager.Create(CreateWorktreeOptions{Slug: "feature-login", Branch: "feature/login"})
	if err != nil {
		t.Fatal(err)
	}
	if entry.Branch != "feature/login" {
		t.Fatalf("branch = %q", entry.Branch)
	}
	if entry.Base != "" {
		t.Fatalf("base should be empty when reusing a branch: %q", entry.Base)
	}
	status, err := manager.Status(entry.Slug)
	if err != nil {
		t.Fatal(err)
	}
	if status.Branch != "feature/login" {
		t.Fatalf("status branch = %q", status.Branch)
	}
}

func TestWorktreeCreateRejectsNonEmptyPath(t *testing.T) {
	repo := initGitRepo(t)
	manager, err := NewWorktreeManager(repo, ".pegg-worktrees", "main")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(repo, ".pegg-worktrees", "taken")
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(path, "file.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Create(CreateWorktreeOptions{Slug: "taken"}); err == nil {
		t.Fatal("expected an error for a non-empty worktree path")
	}
}

func TestSlugify(t *testing.T) {
	cases := map[string]string{
		"Issue 42: Fix Login!":  "issue-42-fix-login",
		"  multiple   spaces  ": "multiple-spaces",
		"pegg/issue-42":         "pegg-issue-42",
		"UPPER_CASE":            "upper-case",
		"---dashes---":          "dashes",
		"emoji-🚀-rocket":        "emoji-rocket",
		"":                      "",
	}
	for input, want := range cases {
		if got := slugify(input); got != want {
			t.Errorf("slugify(%q) = %q, want %q", input, got, want)
		}
	}
	long := slugify(strings.Repeat("a", 100))
	if len(long) != 60 {
		t.Fatalf("long slug length = %d", len(long))
	}
}

func TestWorktreeIndexSchemaRoundTrip(t *testing.T) {
	repo := initGitRepo(t)
	manager, err := NewWorktreeManager(repo, ".pegg-worktrees", "main")
	if err != nil {
		t.Fatal(err)
	}
	entry, err := manager.Create(CreateWorktreeOptions{Slug: "schema", Issue: 7, CardID: "card-7"})
	if err != nil {
		t.Fatal(err)
	}
	entry.PR = 12
	entry.PRURL = "https://github.com/o/r/pull/12"
	if err := manager.withIndex(func(index *worktreeIndex) error {
		for i := range index.Worktrees {
			if index.Worktrees[i].Slug == entry.Slug {
				index.Worktrees[i] = *entry
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	entries, err := manager.List()
	if err != nil {
		t.Fatal(err)
	}
	var found *WorktreeListEntry
	for i := range entries {
		if entries[i].Slug == entry.Slug {
			found = &entries[i]
		}
	}
	if found == nil || found.PR != 12 || found.PRURL == "" || found.Issue != 7 || found.CardID != "card-7" {
		t.Fatalf("entry = %#v", found)
	}
}
