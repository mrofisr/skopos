// Package grafanacheck verifies that Grafana provisioned the Skopos datasources
// and that each datasource passes its own health check. It talks only to
// Grafana's HTTP API and never links Grafana code.
package grafanacheck

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const maxBodyBytes = 1 << 20

// Datasource is one datasource from a Grafana provisioning file.
type Datasource struct {
	Name string
	UID  string
	Type string
	URL  string
}

// ParseProvisioning reads the small YAML subset used by the example
// provisioning file. It extracts the datasource name, uid, type, and url and
// rejects entries missing the fields Grafana requires.
func ParseProvisioning(data []byte) ([]Datasource, error) {
	var datasources []Datasource
	inDatasources := false
	for index, raw := range strings.Split(string(data), "\n") {
		line := strings.TrimRight(raw, "\r")
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		if trimmed == "datasources:" {
			inDatasources = true
			continue
		}
		if !inDatasources {
			continue
		}
		if strings.HasPrefix(trimmed, "- ") || trimmed == "-" {
			datasources = append(datasources, Datasource{})
			trimmed = strings.TrimSpace(strings.TrimPrefix(trimmed, "-"))
			if trimmed == "" {
				continue
			}
		}
		if len(datasources) == 0 {
			continue
		}
		key, value, ok := splitField(trimmed)
		if !ok {
			continue
		}
		current := &datasources[len(datasources)-1]
		switch key {
		case "name":
			current.Name = value
		case "uid":
			current.UID = value
		case "type":
			current.Type = value
		case "url":
			current.URL = value
		default:
			continue
		}
		if value == "" {
			return nil, fmt.Errorf("line %d: %s is empty", index+1, key)
		}
	}
	if len(datasources) == 0 {
		return nil, errors.New("provisioning contains no datasources")
	}
	seen := make(map[string]bool, len(datasources))
	for index, datasource := range datasources {
		if datasource.Name == "" {
			return nil, fmt.Errorf("datasource %d is missing name", index+1)
		}
		if datasource.UID == "" {
			return nil, fmt.Errorf("datasource %q is missing uid", datasource.Name)
		}
		if datasource.Type == "" {
			return nil, fmt.Errorf("datasource %q is missing type", datasource.UID)
		}
		if seen[datasource.UID] {
			return nil, fmt.Errorf("datasource uid %q is duplicated", datasource.UID)
		}
		seen[datasource.UID] = true
	}
	return datasources, nil
}

// ParseProvisioningPath reads a provisioning file, or every YAML file under a
// provisioning directory's datasources directory.
func ParseProvisioningPath(path string) ([]Datasource, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("read provisioning: %w", err)
	}
	if !info.IsDir() {
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("read provisioning: %w", err)
		}
		return ParseProvisioning(data)
	}

	var files []string
	for _, pattern := range []string{
		filepath.Join(path, "datasources", "*.yaml"),
		filepath.Join(path, "datasources", "*.yml"),
		filepath.Join(path, "*.yaml"),
		filepath.Join(path, "*.yml"),
	} {
		matches, err := filepath.Glob(pattern)
		if err != nil {
			return nil, fmt.Errorf("read provisioning: %w", err)
		}
		files = append(files, matches...)
	}
	if len(files) == 0 {
		return nil, fmt.Errorf("read provisioning: no YAML files under %s", path)
	}
	sort.Strings(files)

	var datasources []Datasource
	seen := make(map[string]string)
	for _, file := range files {
		data, err := os.ReadFile(file)
		if err != nil {
			return nil, fmt.Errorf("read provisioning %s: %w", file, err)
		}
		parsed, err := ParseProvisioning(data)
		if err != nil {
			return nil, fmt.Errorf("parse provisioning %s: %w", file, err)
		}
		for _, datasource := range parsed {
			if previous, ok := seen[datasource.UID]; ok {
				return nil, fmt.Errorf("datasource uid %q is duplicated in %s and %s", datasource.UID, previous, file)
			}
			seen[datasource.UID] = file
			datasources = append(datasources, datasource)
		}
	}
	return datasources, nil
}

func splitField(line string) (key, value string, ok bool) {
	index := strings.Index(line, ":")
	if index <= 0 {
		return "", "", false
	}
	key = strings.TrimSpace(line[:index])
	value = strings.TrimSpace(line[index+1:])
	value = strings.Trim(value, `"'`)
	return key, value, key != ""
}

// Client queries a Grafana instance.
type Client struct {
	// BaseURL is the Grafana root, for example http://127.0.0.1:3000.
	BaseURL string
	// Username and Password enable Grafana basic auth when set.
	Username string
	Password string
	HTTP     *http.Client
}

// Check verifies that every expected datasource exists and passes Grafana's
// health endpoint. The first failure is returned with the datasource name and
// UID; any HTTP or decode error fails closed.
func (c Client) Check(ctx context.Context, expected []Datasource) ([]Datasource, error) {
	if strings.TrimSpace(c.BaseURL) == "" {
		return nil, errors.New("grafana URL is required")
	}
	if len(expected) == 0 {
		return nil, errors.New("no datasources to check")
	}
	httpClient := c.HTTP
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 30 * time.Second}
	}

	var remote []struct {
		Name string `json:"name"`
		UID  string `json:"uid"`
		Type string `json:"type"`
	}
	status, body, err := c.get(ctx, httpClient, "/api/datasources", &remote)
	if err != nil {
		return nil, fmt.Errorf("grafana: list datasources: %w", err)
	}
	if status != http.StatusOK {
		return nil, fmt.Errorf("grafana: list datasources: HTTP %d: %s", status, bodySummary(body))
	}
	byUID := make(map[string]struct {
		Name string
		Type string
	}, len(remote))
	for _, datasource := range remote {
		byUID[datasource.UID] = struct {
			Name string
			Type string
		}{Name: datasource.Name, Type: datasource.Type}
	}

	checked := make([]Datasource, 0, len(expected))
	for _, datasource := range expected {
		remoteDatasource, ok := byUID[datasource.UID]
		if !ok {
			return nil, fmt.Errorf("grafana: datasource %q (%s) is not provisioned",
				datasource.Name, datasource.UID)
		}
		if remoteDatasource.Type != "" && datasource.Type != "" &&
			!strings.EqualFold(remoteDatasource.Type, datasource.Type) {
			return nil, fmt.Errorf("grafana: datasource %q (%s) has type %q, want %q",
				datasource.Name, datasource.UID, remoteDatasource.Type, datasource.Type)
		}

		var health struct {
			Status  string `json:"status"`
			Message string `json:"message"`
		}
		status, body, err := c.get(ctx, httpClient,
			"/api/datasources/uid/"+url.PathEscape(datasource.UID)+"/health", &health)
		if err != nil {
			return nil, fmt.Errorf("grafana: datasource %q (%s) health check: %w",
				datasource.Name, datasource.UID, err)
		}
		if status != http.StatusOK {
			return nil, fmt.Errorf("grafana: datasource %q (%s) is unhealthy: HTTP %d: %s",
				datasource.Name, datasource.UID, status, bodySummary(body))
		}
		if !strings.EqualFold(health.Status, "OK") {
			return nil, fmt.Errorf("grafana: datasource %q (%s) is unhealthy: status %q: %s",
				datasource.Name, datasource.UID, health.Status, health.Message)
		}
		checked = append(checked, datasource)
	}
	return checked, nil
}

func (c Client) get(ctx context.Context, httpClient *http.Client, path string, out any) (int, []byte, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet,
		strings.TrimRight(c.BaseURL, "/")+path, nil)
	if err != nil {
		return 0, nil, fmt.Errorf("build request: %w", err)
	}
	request.Header.Set("Accept", "application/json")
	if c.Username != "" {
		request.SetBasicAuth(c.Username, c.Password)
	}
	response, err := httpClient.Do(request)
	if err != nil {
		return 0, nil, fmt.Errorf("request %s: %w", path, err)
	}
	body, readErr := io.ReadAll(io.LimitReader(response.Body, maxBodyBytes))
	closeErr := response.Body.Close()
	if readErr != nil {
		return 0, nil, fmt.Errorf("read %s: %w", path, readErr)
	}
	if closeErr != nil {
		return 0, nil, fmt.Errorf("close %s: %w", path, closeErr)
	}
	if response.StatusCode == http.StatusOK && out != nil {
		if err := json.Unmarshal(body, out); err != nil {
			return response.StatusCode, body, fmt.Errorf("decode %s: %w", path, err)
		}
	}
	return response.StatusCode, body, nil
}

func bodySummary(body []byte) string {
	text := strings.Join(strings.Fields(string(body)), " ")
	if len(text) > 300 {
		text = text[:300] + "..."
	}
	return text
}
