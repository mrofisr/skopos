package ghapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

type item struct {
	ID int `json:"id"`
}

func testClient(t *testing.T, handler http.Handler, mutate func(*Config)) *Client {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	cfg := Config{
		BaseURL: server.URL,
		Repo:    "owner/repo",
		Token:   "secret-token",
		HTTP:    server.Client(),
		Sleep:   func(context.Context, time.Duration) error { return nil },
	}
	if mutate != nil {
		mutate(&cfg)
	}
	client, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return client
}

func TestNewRequiresToken(t *testing.T) {
	_, err := New(Config{Repo: "owner/repo"})
	if !errors.Is(err, ErrNoToken) {
		t.Fatalf("New without token = %v, want ErrNoToken", err)
	}
}

func TestNewValidatesRepository(t *testing.T) {
	for _, repo := range []string{"", "owner", "owner/repo/extra", "/repo", "owner/"} {
		t.Run(repo, func(t *testing.T) {
			_, err := New(Config{Repo: repo, Token: "token"})
			if err == nil {
				t.Fatalf("New(repo=%q) succeeded, want an error", repo)
			}
		})
	}
}

func TestNewValidatesBaseURL(t *testing.T) {
	for _, base := range []string{"not a url", "ftp://example.com", "https://"} {
		t.Run(base, func(t *testing.T) {
			_, err := New(Config{BaseURL: base, Repo: "owner/repo", Token: "token"})
			if err == nil {
				t.Fatalf("New(base=%q) succeeded, want an error", base)
			}
		})
	}
}

func TestGetAllFollowsPagination(t *testing.T) {
	pages := [][]item{{{ID: 1}}, {{ID: 2}}, {{ID: 3}}}
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		page := 1
		if raw := r.URL.Query().Get("page"); raw != "" {
			fmt.Sscanf(raw, "%d", &page)
		}
		if page < len(pages) {
			w.Header().Set("Link", fmt.Sprintf(`<%s/repos/owner/repo/items?page=%d>; rel="next"`, server.URL, page+1))
		}
		_ = json.NewEncoder(w).Encode(pages[page-1])
	}))
	t.Cleanup(server.Close)
	client, err := New(Config{
		BaseURL: server.URL,
		Repo:    "owner/repo",
		Token:   "secret-token",
		HTTP:    server.Client(),
		Sleep:   func(context.Context, time.Duration) error { return nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	got, err := GetAll[item](context.Background(), client, "/repos/owner/repo/items", url.Values{"per_page": {"1"}})
	if err != nil {
		t.Fatalf("GetAll: %v", err)
	}
	if len(got) != 3 || got[0].ID != 1 || got[2].ID != 3 {
		t.Fatalf("items = %#v, want ids 1,2,3", got)
	}
}

func TestGetRetriesAfterRetryAfter(t *testing.T) {
	attempts := 0
	var slept []time.Duration
	client := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		if attempts == 1 {
			w.Header().Set("Retry-After", "2")
			w.WriteHeader(http.StatusForbidden)
			return
		}
		_, _ = io.WriteString(w, `{"id":7}`)
	}), func(cfg *Config) {
		cfg.Sleep = func(_ context.Context, d time.Duration) error {
			slept = append(slept, d)
			return nil
		}
	})
	var got item
	if err := client.Get(context.Background(), "/repos/owner/repo/items/7", nil, &got); err != nil {
		t.Fatalf("Get: %v", err)
	}
	if attempts != 2 {
		t.Fatalf("attempts = %d, want 2", attempts)
	}
	if len(slept) != 1 || slept[0] != 2*time.Second {
		t.Fatalf("slept = %v, want [2s]", slept)
	}
	if got.ID != 7 {
		t.Fatalf("item = %#v, want id 7", got)
	}
}

func TestNotFoundIsTyped(t *testing.T) {
	client := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = io.WriteString(w, `{"message":"Not Found"}`)
	}), nil)
	err := client.Get(context.Background(), "/repos/owner/repo/issues/404", nil, nil)
	var status *StatusError
	if !errors.As(err, &status) {
		t.Fatalf("error = %v, want *StatusError", err)
	}
	if !status.IsNotFound() || status.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %#v, want 404", status)
	}
}

func TestTokenNeverAppearsInErrors(t *testing.T) {
	const token = "super-secret-token"
	client := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = io.WriteString(w, `{"message":"bad credentials `+token+`"}`)
	}), func(cfg *Config) { cfg.Token = token })
	err := client.Get(context.Background(), "/repos/owner/repo/issues/1", nil, nil)
	if err == nil {
		t.Fatal("Get succeeded, want an error")
	}
	if strings.Contains(err.Error(), token) {
		t.Fatalf("error leaks the token: %v", err)
	}
	if !strings.Contains(err.Error(), "[redacted]") {
		t.Fatalf("error does not show the redaction marker: %v", err)
	}
}

func TestCancelledContextStopsRetries(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	attempts := 0
	client := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		w.WriteHeader(http.StatusServiceUnavailable)
	}), func(cfg *Config) {
		cfg.Sleep = func(context.Context, time.Duration) error {
			cancel()
			return ctx.Err()
		}
	})
	err := client.Get(ctx, "/repos/owner/repo/issues/1", nil, nil)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want context.Canceled", err)
	}
	if attempts != 1 {
		t.Fatalf("attempts = %d, want 1", attempts)
	}
}

func TestRetriesAreBounded(t *testing.T) {
	attempts := 0
	client := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		w.WriteHeader(http.StatusServiceUnavailable)
	}), func(cfg *Config) { cfg.MaxRetries = 2 })
	err := client.Get(context.Background(), "/repos/owner/repo/issues/1", nil, nil)
	if err == nil {
		t.Fatal("Get succeeded, want an error")
	}
	if attempts != 3 {
		t.Fatalf("attempts = %d, want 3 (initial + 2 retries)", attempts)
	}
}

func TestMalformedLinkFailsLoudly(t *testing.T) {
	client := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Link", "not a link")
		_, _ = io.WriteString(w, `[]`)
	}), nil)
	_, err := GetAll[item](context.Background(), client, "/repos/owner/repo/items", nil)
	if err == nil || !strings.Contains(err.Error(), "Link") {
		t.Fatalf("error = %v, want a Link header error", err)
	}
}

func TestDecodeError(t *testing.T) {
	client := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"id":`)
	}), nil)
	var got item
	err := client.Get(context.Background(), "/repos/owner/repo/items/1", nil, &got)
	if err == nil || !strings.Contains(err.Error(), "decode") {
		t.Fatalf("error = %v, want a decode error", err)
	}
}

func TestDoRejectsNonAbsolutePath(t *testing.T) {
	client := testClient(t, http.NotFoundHandler(), nil)
	err := client.Get(context.Background(), "repos/owner/repo/issues/1", nil, nil)
	if err == nil || !strings.Contains(err.Error(), "path") {
		t.Fatalf("error = %v, want a path error", err)
	}
}

func TestRepoPath(t *testing.T) {
	client := testClient(t, http.NotFoundHandler(), nil)
	if got := client.RepoPath("issues/%d", 7); got != "/repos/owner/repo/issues/7" {
		t.Fatalf("RepoPath = %q", got)
	}
}

func TestNextLink(t *testing.T) {
	current, err := url.Parse("https://api.github.com/repos/o/r/items?page=1")
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name    string
		header  string
		want    string
		wantOK  bool
		wantErr bool
	}{
		{name: "empty"},
		{name: "next", header: `<https://api.github.com/repos/o/r/items?page=2>; rel="next"`, want: "https://api.github.com/repos/o/r/items?page=2", wantOK: true},
		{name: "last only", header: `<https://api.github.com/repos/o/r/items?page=9>; rel="last"`},
		{name: "relative", header: `<../items?page=2>; rel="next"`, want: "https://api.github.com/repos/o/items?page=2", wantOK: true},
		{name: "multiple", header: `<https://api.github.com/repos/o/r/items?page=2>; rel="next", <https://api.github.com/repos/o/r/items?page=9>; rel="last"`, want: "https://api.github.com/repos/o/r/items?page=2", wantOK: true},
		{name: "garbage", header: "not a link", wantErr: true},
		{name: "bad url", header: `<://bad>; rel="next"`, wantErr: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			next, err := nextLink(test.header, current)
			got := ""
			if next != nil {
				got = next.String()
			}
			if test.wantErr {
				if err == nil {
					t.Fatalf("nextLink(%q) succeeded, want an error", test.header)
				}
				return
			}
			if err != nil {
				t.Fatalf("nextLink(%q): %v", test.header, err)
			}
			if test.wantOK && got != test.want {
				t.Fatalf("nextLink(%q) = %q, want %q", test.header, got, test.want)
			}
			if !test.wantOK && got != "" {
				t.Fatalf("nextLink(%q) = %q, want empty", test.header, got)
			}
		})
	}
}
