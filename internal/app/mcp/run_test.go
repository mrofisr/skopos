package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/jawaracloud/skopos/internal/appenv"
)

func testEnv(stdin io.Reader, env map[string]string) appenv.Env {
	var stdout, stderr bytes.Buffer
	return appenv.Env{
		Stdin:  stdin,
		Stdout: &stdout,
		Stderr: &stderr,
		LookupEnv: func(key string) (string, bool) {
			value, ok := env[key]
			return value, ok
		},
	}
}

func TestRunServesStdio(t *testing.T) {
	request := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}` + "\n"
	env := testEnv(strings.NewReader(request), map[string]string{
		"SKOPOS_GATEWAY_URL": "http://127.0.0.1:8080",
	})
	if err := Run(context.Background(), env); err != nil {
		t.Fatalf("Run: %v", err)
	}
	var response struct {
		Result struct {
			ServerInfo map[string]string `json:"serverInfo"`
		} `json:"result"`
	}
	line := strings.TrimSpace(env.Stdout.(*bytes.Buffer).String())
	if err := json.Unmarshal([]byte(line), &response); err != nil {
		t.Fatalf("decode response %q: %v", line, err)
	}
	if response.Result.ServerInfo["name"] != "skopos" {
		t.Fatalf("serverInfo = %#v", response.Result.ServerInfo)
	}
}

func TestRunRejectsInvalidGatewayURL(t *testing.T) {
	env := testEnv(strings.NewReader(""), map[string]string{
		"SKOPOS_GATEWAY_URL": "not-a-url",
	})
	if err := Run(context.Background(), env); err == nil {
		t.Fatal("Run accepted an invalid gateway URL")
	}
}

func TestServeHTTPReturnsListenError(t *testing.T) {
	server, err := NewServer(Config{GatewayURL: "http://127.0.0.1:8080"})
	if err != nil {
		t.Fatal(err)
	}
	err = serveHTTP(context.Background(), "127.0.0.1:not-a-port", server, io.Discard)
	if err == nil {
		t.Fatal("serveHTTP accepted an invalid listen address")
	}
}

func TestServeHTTPShutsDownOnContextCancel(t *testing.T) {
	server, err := NewServer(Config{GatewayURL: "http://127.0.0.1:8080"})
	if err != nil {
		t.Fatal(err)
	}
	reader, writer := io.Pipe()
	defer reader.Close()
	defer writer.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan error, 1)
	go func() {
		done <- serveHTTP(ctx, "127.0.0.1:0", server, writer)
	}()

	line := make([]byte, len("skopos: mcp listening on 127.0.0.1:0\n"))
	if _, err := io.ReadFull(reader, line); err != nil {
		t.Fatalf("read listen message: %v", err)
	}
	cancel()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("serveHTTP: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("serveHTTP did not stop after context cancellation")
	}
}
