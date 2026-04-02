package tools

import (
	"fmt"
	"os"
	"strings"
	"time"
)

const defaultEnvName = "default"

func resolveEnv(value string) string {
	if strings.TrimSpace(value) != "" {
		return value
	}
	if env := strings.TrimSpace(os.Getenv("AMP_ENV")); env != "" {
		return env
	}
	return defaultEnvName
}

func resolveTimeWindow(start, end string) (string, string, error) {
	if start == "" && end == "" {
		return defaultWindow()
	}
	if start == "" || end == "" {
		return "", "", fmt.Errorf("start_time and end_time must be provided together")
	}
	if _, err := time.Parse(time.RFC3339, start); err != nil {
		return "", "", fmt.Errorf("invalid start_time format (use RFC3339)")
	}
	if _, err := time.Parse(time.RFC3339, end); err != nil {
		return "", "", fmt.Errorf("invalid end_time format (use RFC3339)")
	}
	return start, end, nil
}

func defaultWindow() (string, string, error) {
	end := time.Now().UTC()
	start := end.Add(-24 * time.Hour)
	return start.Format(time.RFC3339), end.Format(time.RFC3339), nil
}

func defaultSortOrder(order string) string {
	switch strings.ToLower(strings.TrimSpace(order)) {
	case "asc":
		return "asc"
	default:
		return "desc"
	}
}