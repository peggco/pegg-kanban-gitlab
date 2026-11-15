package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

const (
	userAgent       = "pegg-kanban-gitlab-plugin"
	maxPages        = 3
	maxResponseSize = 8 << 20
)

type Client struct {
	baseURL string
	token   string
	project string
	http    *http.Client
}

func newClient(s Settings) *Client {
	return &Client{
		baseURL: strings.TrimRight(s.BaseURL, "/"),
		token:   strings.TrimSpace(s.Token),
		project: normalizeProject(s.Project),
		http:    &http.Client{Timeout: s.timeout()},
	}
}

type apiResponse struct {
	status int
	header http.Header
	body   []byte
}

func (c *Client) request(ctx context.Context, method, path string, query url.Values, payload any) (*apiResponse, error) {
	target := c.baseURL + path
	if len(query) > 0 {
		target += "?" + query.Encode()
	}
	var body io.Reader
	if payload != nil {
		encoded, err := json.Marshal(payload)
		if err != nil {
			return nil, fmt.Errorf("encode request: %w", err)
		}
		body = bytes.NewReader(encoded)
	}
	req, err := http.NewRequestWithContext(ctx, method, target, body)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", userAgent)
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.token != "" {
		req.Header.Set("PRIVATE-TOKEN", c.token)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%s %s: %w", method, path, err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseSize))
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}
	out := &apiResponse{status: resp.StatusCode, header: resp.Header, body: data}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return out, apiError(method, path, resp.StatusCode, data)
	}
	return out, nil
}

func apiError(method, path string, status int, body []byte) error {
	message := extractMessage(body)
	if len(message) > 400 {
		message = message[:400] + "..."
	}
	base := fmt.Sprintf("GitLab API %s %s: HTTP %d", method, path, status)
	if message != "" {
		base += ": " + message
	}
	switch status {
	case http.StatusUnauthorized:
		return fmt.Errorf("%s (token is missing or invalid; set PEGG_GITLAB_TOKEN or plugins.options.gitlab.token)", base)
	case http.StatusForbidden:
		return fmt.Errorf("%s (token lacks access or the required api/read_api scope)", base)
	case http.StatusNotFound:
		return fmt.Errorf("%s (check the project id/path and object id)", base)
	case http.StatusTooManyRequests:
		return fmt.Errorf("%s (rate limited; retry later)", base)
	default:
		return fmt.Errorf("%s", base)
	}
}

func extractMessage(body []byte) string {
	trimmed := strings.TrimSpace(string(body))
	if trimmed == "" {
		return ""
	}
	var payload struct {
		Message any    `json:"message"`
		Error   string `json:"error"`
	}
	if err := json.Unmarshal(body, &payload); err == nil {
		switch value := payload.Message.(type) {
		case string:
			if value != "" {
				return value
			}
		case map[string]any:
			parts := make([]string, 0, len(value))
			for key, item := range value {
				parts = append(parts, fmt.Sprintf("%s: %v", key, item))
			}
			if len(parts) > 0 {
				return strings.Join(parts, "; ")
			}
		}
		if payload.Error != "" {
			return payload.Error
		}
	}
	if len(trimmed) > 400 {
		return trimmed[:400]
	}
	return trimmed
}

func (c *Client) do(ctx context.Context, method, path string, query url.Values, payload any, out any) error {
	resp, err := c.request(ctx, method, path, query, payload)
	if err != nil {
		return err
	}
	if out == nil || len(resp.body) == 0 {
		return nil
	}
	if err := json.Unmarshal(resp.body, out); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}
	return nil
}

func (c *Client) getJSON(ctx context.Context, path string, query url.Values, out any) error {
	return c.do(ctx, http.MethodGet, path, query, nil, out)
}

func (c *Client) postJSON(ctx context.Context, path string, payload any, out any) error {
	return c.do(ctx, http.MethodPost, path, nil, payload, out)
}

func (c *Client) putJSON(ctx context.Context, path string, payload any, out any) error {
	return c.do(ctx, http.MethodPut, path, nil, payload, out)
}

func (c *Client) text(ctx context.Context, method, path string, query url.Values) (string, error) {
	resp, err := c.request(ctx, method, path, query, nil)
	if err != nil {
		return "", err
	}
	return string(resp.body), nil
}

func (c *Client) total(ctx context.Context, path string, query url.Values) (int, error) {
	resp, err := c.request(ctx, http.MethodGet, path, query, nil)
	if err != nil {
		return 0, err
	}
	value := strings.TrimSpace(resp.header.Get("X-Total"))
	if value == "" {
		return 0, nil
	}
	n, err := strconv.Atoi(value)
	if err != nil {
		return 0, nil
	}
	return n, nil
}

func getPaged[T any](ctx context.Context, c *Client, path string, query url.Values, limit int) ([]T, error) {
	items := []T{}
	nextQuery := url.Values{}
	for key, values := range query {
		nextQuery[key] = append([]string(nil), values...)
	}
	for page := 0; page < maxPages; page++ {
		resp, err := c.request(ctx, http.MethodGet, path, nextQuery, nil)
		if err != nil {
			return nil, err
		}
		var batch []T
		if len(resp.body) > 0 {
			if err := json.Unmarshal(resp.body, &batch); err != nil {
				return nil, fmt.Errorf("decode response: %w", err)
			}
		}
		items = append(items, batch...)
		if limit > 0 && len(items) >= limit {
			break
		}
		next := strings.TrimSpace(resp.header.Get("X-Next-Page"))
		if next == "" || next == "0" || next == nextQuery.Get("page") {
			break
		}
		nextQuery.Set("page", next)
	}
	if limit > 0 && len(items) > limit {
		items = items[:limit]
	}
	return items, nil
}
