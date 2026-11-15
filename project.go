package main

import (
	"fmt"
	"net/url"
	"strconv"
	"strings"
)

func isNumericProject(project string) bool {
	if project == "" {
		return false
	}
	_, err := strconv.Atoi(project)
	return err == nil
}

func encodeProject(project string) string {
	project = normalizeProject(project)
	if project == "" {
		return ""
	}
	if isNumericProject(project) {
		return project
	}
	return url.PathEscape(project)
}

func (c *Client) projectPath(suffix string) (string, error) {
	project := normalizeProject(c.project)
	if project == "" {
		return "", fmt.Errorf("project is required: set PEGG_GITLAB_PROJECT or plugins.options.gitlab.project (numeric id or group/project)")
	}
	return "/projects/" + encodeProject(project) + suffix, nil
}

func labelsQuery(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	parts := strings.Split(value, ",")
	labels := make([]string, 0, len(parts))
	for _, part := range parts {
		if label := strings.TrimSpace(part); label != "" {
			labels = append(labels, label)
		}
	}
	return strings.Join(labels, ",")
}
