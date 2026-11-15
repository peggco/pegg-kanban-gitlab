<div align="center">

<img src="https://cdn.pegg.dev/logo/infinity.gif" alt="Pegg" width="420" />

# Pegg Kanban GitLab

### GitLab issues on your Kanban board.

A Kanban integration for GitLab. It imports issues, prepares worktrees, and comments or opens a merge request when a card succeeds.

[Plugins docs](https://docs.pegg.dev/pegg/configurations/plugins/) · [Releases](https://github.com/peggco/pegg-kanban-gitlab/releases) · [Website](https://pegg.dev) · [Discussions](https://github.com/peggco/pegg/discussions)

[![Latest release](https://img.shields.io/github/v/release/peggco/pegg-kanban-gitlab?style=flat-square&sort=semver)](https://github.com/peggco/pegg-kanban-gitlab/releases)
[![Go 1.26.5+](https://img.shields.io/badge/go-1.26.5%2B-00ADD8?style=flat-square&logo=go&logoColor=white)](https://github.com/peggco/pegg-kanban-gitlab)
[![Plugin protocol v2](https://img.shields.io/badge/plugin%20protocol-v2-black?style=flat-square)](https://docs.pegg.dev/pegg/configurations/plugins/)
[![License: Apache-2.0](https://img.shields.io/badge/license-Apache--2.0-black?style=flat-square)](https://github.com/peggco/pegg-kanban-gitlab/blob/HEAD/LICENSE)

**Install**

```bash
pegg plugins install peggco/pegg-kanban
pegg plugins install peggco/pegg-kanban-gitlab
```

Hooks-only. Install `pegg-kanban` alongside this plugin; it contributes no agent tools.
</div>

## Build and install

```sh
go mod tidy
go vet ./...
go test ./...
go build -trimpath -ldflags "-s -w" -o bin/pegg-kanban-gitlab .
install -m 0755 bin/pegg-kanban-gitlab "$HOME/.pegg/plugins/pegg-kanban-gitlab"
pegg plugins list
```

## Hooks

Provider id: `gitlab` (unchanged for kanban compatibility).

| Hook | Kind | Priority | Purpose |
|---|---|---|---|
| `kanban.providers` | filter | 10 | Advertise GitLab as a Kanban issue provider. |
| `kanban.issues.fetch` | filter | 10 | Fetch GitLab issues for Kanban sync. |
| `kanban.run.prepare` | filter | 10 | Create a git worktree with a `pegg/<slug>` branch for a card run. |
| `kanban.card.succeeded` | action | 10 | Comment on the linked issue and open a merge request. |

All hooks are no-ops when the plugin is disabled.

### Worktrees

`kanban.run.prepare` reads the repository from `repo` in the incoming payload. It creates a
worktree under `worktree_root` (default `.pegg-worktrees`), using `base_branch` when set and
`default_target_branch` otherwise. The new branch is named `pegg/<slug>`, where the slug is
derived from the card title and ID.

The returned payload adds a `context` object:

```json
{
  "kind": "worktree",
  "path": "/repo/.pegg-worktrees/fix-login-abc",
  "branch": "pegg/fix-login-abc",
  "base": "main",
  "label": "fix-login-abc"
}
```

### Merge requests

`kanban.card.succeeded` comments on the linked GitLab issue when feedback is enabled, then
creates a merge request from the worktree branch. It accepts the worktree data either as
`worktree` or as `context`, matching the other Pegg Kanban providers. The target branch is
the worktree base, then `base_branch`, then `default_target_branch`.

## Configuration

`~/.pegg/pegg.json`:

```json
{
  "plugins": {
    "options": {
      "kanban-gitlab": {
        "enabled": true,
        "token": "glpat-...",
        "base_url": "https://gitlab.com/api/v4",
        "project": "group/project",
        "default_target_branch": "main",
        "worktree_root": ".pegg-worktrees",
        "sync_state": "opened",
        "sync_labels": ["kanban"],
        "sync_limit": 50,
        "request_timeout_seconds": 30
      }
    }
  }
}
```

The option key `pegg-kanban-gitlab` is also accepted. Legacy keys `gitlab` and
`pegg-gitlab` remain as fallbacks, with `kanban-gitlab` taking precedence.

Environment overrides:

| Variable | Purpose |
|---|---|
| `PEGG_GITLAB_TOKEN` | Preferred access token. |
| `GITLAB_TOKEN` | Token fallback. |
| `CI_JOB_TOKEN` | Read-only token fallback. |
| `PEGG_GITLAB_URL` | GitLab API base URL. |
| `PEGG_GITLAB_PROJECT` | Project path or numeric ID. |
| `PEGG_GITLAB_WORKTREE_ROOT` | Worktree directory. |
| `PEGG_GITLAB_TIMEOUT_SECONDS` | API timeout. |
| `PEGG_GITLAB_DISABLED` | Set `true` to disable the plugin. |

## API and token scopes

The plugin uses GitLab REST API v4 with the `PRIVATE-TOKEN` header.

- Use a personal, project, or group access token with the `api` scope for comments and merge
  requests.
- `read_api` is enough for issue sync.
- `CI_JOB_TOKEN` is read-only; writes fail with `403`.

Tokens are never printed. Hook errors are redacted before they are returned to the host.

## License

Apache-2.0. See [LICENSE](https://github.com/peggco/pegg-kanban-gitlab/blob/HEAD/LICENSE).
