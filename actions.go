package main

import (
	"context"
	"fmt"
	"net/url"
	"strconv"
	"strings"
)

type userRef struct {
	Username string `json:"username"`
}

type issueAPI struct {
	IID         int       `json:"iid"`
	Title       string    `json:"title"`
	Description string    `json:"description"`
	State       string    `json:"state"`
	Author      userRef   `json:"author"`
	Assignees   []userRef `json:"assignees"`
	Labels      []string  `json:"labels"`
	WebURL      string    `json:"web_url"`
	CreatedAt   string    `json:"created_at"`
	UpdatedAt   string    `json:"updated_at"`
}

type noteAPI struct {
	ID int `json:"id"`
}

type mrAPI struct {
	IID    int    `json:"iid"`
	WebURL string `json:"web_url"`
	State  string `json:"state"`
}

type issueCLIView struct {
	IID         int      `json:"iid"`
	Title       string   `json:"title"`
	Description string   `json:"description"`
	State       string   `json:"state"`
	WebURL      string   `json:"web_url"`
	Labels      []string `json:"labels"`
	Assignees   []string `json:"assignees"`
	Author      string   `json:"author"`
	CreatedAt   string   `json:"created_at"`
	UpdatedAt   string   `json:"updated_at"`
}

type issueCommentResult struct {
	IID int    `json:"iid"`
	URL string `json:"url"`
}

type mrCreateInput struct {
	SourceBranch       string
	TargetBranch       string
	Title              string
	Description        string
	RemoveSourceBranch bool
	Squash             bool
}

type mrCreateResult struct {
	IID    int    `json:"iid"`
	WebURL string `json:"web_url"`
	State  string `json:"state"`
}

func listIssues(ctx context.Context, c *Client, state, labels, search string, limit int) ([]issueCLIView, error) {
	path, err := c.projectPath("/issues")
	if err != nil {
		return nil, err
	}
	limit = clampLimit(limit, 30)
	query := url.Values{
		"state":    {normalizeState(state, "opened")},
		"per_page": {perPage(limit)},
	}
	if value := labelsQuery(labels); value != "" {
		query.Set("labels", value)
	}
	if value := strings.TrimSpace(search); value != "" {
		query.Set("search", value)
	}
	items, err := getPaged[issueAPI](ctx, c, path, query, limit)
	if err != nil {
		return nil, err
	}
	views := make([]issueCLIView, 0, len(items))
	for _, item := range items {
		views = append(views, issueCLIView{
			IID:         item.IID,
			Title:       item.Title,
			Description: item.Description,
			State:       item.State,
			WebURL:      item.WebURL,
			Labels:      nonNilStrings(item.Labels),
			Assignees:   usernames(item.Assignees),
			Author:      item.Author.Username,
			CreatedAt:   item.CreatedAt,
			UpdatedAt:   item.UpdatedAt,
		})
	}
	return views, nil
}

func commentOnIssue(ctx context.Context, c *Client, iid int, body string) (issueCommentResult, error) {
	if err := requirePositive(iid, "iid"); err != nil {
		return issueCommentResult{}, err
	}
	body = strings.TrimSpace(body)
	if body == "" {
		return issueCommentResult{}, fmt.Errorf("body is required: pass --body or the body field")
	}
	issuePath, err := c.projectPath("/issues/" + strconv.Itoa(iid))
	if err != nil {
		return issueCommentResult{}, err
	}
	webURL := issueWebURL(c, iid)
	if webURL == "" {
		var issue issueAPI
		if err := c.getJSON(ctx, issuePath, nil, &issue); err != nil {
			return issueCommentResult{}, err
		}
		webURL = issue.WebURL
	}
	var note noteAPI
	if err := c.postJSON(ctx, issuePath+"/notes", map[string]string{"body": body}, &note); err != nil {
		return issueCommentResult{}, err
	}
	return issueCommentResult{IID: iid, URL: webURL + "#note_" + strconv.Itoa(note.ID)}, nil
}

func issueWebURL(c *Client, iid int) string {
	project := normalizeProject(c.project)
	if project == "" || isNumericProject(project) {
		return ""
	}
	return publicBaseURL(c.baseURL) + "/" + project + "/-/issues/" + strconv.Itoa(iid)
}

func createMergeRequest(ctx context.Context, c *Client, input mrCreateInput) (mrCreateResult, error) {
	input.SourceBranch = strings.TrimSpace(input.SourceBranch)
	input.TargetBranch = strings.TrimSpace(input.TargetBranch)
	input.Title = strings.TrimSpace(input.Title)
	if input.SourceBranch == "" {
		return mrCreateResult{}, fmt.Errorf("source branch is required: pass --source or the source field")
	}
	if input.TargetBranch == "" {
		return mrCreateResult{}, fmt.Errorf("target branch is required: pass --target or the target field")
	}
	if input.Title == "" {
		return mrCreateResult{}, fmt.Errorf("title is required: pass --title or the title field")
	}
	path, err := c.projectPath("/merge_requests")
	if err != nil {
		return mrCreateResult{}, err
	}
	payload := map[string]any{
		"source_branch":        input.SourceBranch,
		"target_branch":        input.TargetBranch,
		"title":                input.Title,
		"description":          input.Description,
		"remove_source_branch": input.RemoveSourceBranch,
		"squash":               input.Squash,
	}
	var mr mrAPI
	if err := c.postJSON(ctx, path, payload, &mr); err != nil {
		return mrCreateResult{}, err
	}
	return mrCreateResult{IID: mr.IID, WebURL: mr.WebURL, State: mr.State}, nil
}

func requirePositive(value int, label string) error {
	if value <= 0 {
		return fmt.Errorf("%s is required and must be a positive integer", label)
	}
	return nil
}

func clampLimit(value, def int) int {
	if value <= 0 {
		value = def
	}
	if value > 300 {
		value = 300
	}
	return value
}

func perPage(limit int) string {
	if limit > 100 {
		return "100"
	}
	return strconv.Itoa(limit)
}

func normalizeState(value, def string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "open" {
		return "opened"
	}
	if value == "" {
		return def
	}
	return value
}

func usernames(users []userRef) []string {
	out := make([]string, 0, len(users))
	for _, user := range users {
		if user.Username != "" {
			out = append(out, user.Username)
		}
	}
	return out
}

func nonNilStrings(values []string) []string {
	if values == nil {
		return []string{}
	}
	return values
}

func publicBaseURL(value string) string {
	value = strings.TrimRight(strings.TrimSpace(value), "/")
	for _, suffix := range []string{"/api/v4", "/api"} {
		if strings.HasSuffix(value, suffix) {
			return strings.TrimSuffix(value, suffix)
		}
	}
	return value
}
