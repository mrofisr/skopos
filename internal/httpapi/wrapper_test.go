package httpapi

import (
	"bufio"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

var (
	_ http.Flusher  = (*statusRecorder)(nil)
	_ http.Hijacker = (*statusRecorder)(nil)
	_ http.Pusher   = (*statusRecorder)(nil)
	_ io.ReaderFrom = (*statusRecorder)(nil)

	_ http.Flusher  = (*recoveryWriter)(nil)
	_ http.Hijacker = (*recoveryWriter)(nil)
	_ http.Pusher   = (*recoveryWriter)(nil)
	_ io.ReaderFrom = (*recoveryWriter)(nil)
)

type wrapperFlushRecorder struct {
	*httptest.ResponseRecorder
	flushed bool
}

func (f *wrapperFlushRecorder) Flush() { f.flushed = true }

type wrapperPushRecorder struct {
	*httptest.ResponseRecorder
	target string
}

func (p *wrapperPushRecorder) Push(target string, _ *http.PushOptions) error {
	p.target = target
	return nil
}

type wrapperReadFromRecorder struct {
	*httptest.ResponseRecorder
	called bool
}

func (r *wrapperReadFromRecorder) ReadFrom(src io.Reader) (int64, error) {
	r.called = true
	return io.Copy(r.ResponseRecorder, src)
}

type wrapperHijackRecorder struct {
	*httptest.ResponseRecorder
	conn net.Conn
	rw   *bufio.ReadWriter
}

func (h *wrapperHijackRecorder) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	return h.conn, h.rw, nil
}

func TestResponseWrappersPreserveInterfaces(t *testing.T) {
	base := &wrapperFlushRecorder{ResponseRecorder: httptest.NewRecorder()}
	status := &statusRecorder{ResponseWriter: base}
	recovery := &recoveryWriter{ResponseWriter: status}

	if got := recovery.Unwrap(); got != status {
		t.Fatalf("recovery Unwrap = %T, want the status recorder", got)
	}
	if got := status.Unwrap(); got != base {
		t.Fatalf("status Unwrap = %T, want the base writer", got)
	}

	if err := http.NewResponseController(recovery).Flush(); err != nil {
		t.Fatalf("ResponseController.Flush: %v", err)
	}
	if !base.flushed {
		t.Fatal("Flush did not reach the base writer")
	}
	if status.status != http.StatusOK || !recovery.wroteHeader {
		t.Fatalf("flush status = %d/%v, want 200 and a started response", status.status, recovery.wroteHeader)
	}

	pusher := &wrapperPushRecorder{ResponseRecorder: httptest.NewRecorder()}
	status = &statusRecorder{ResponseWriter: pusher}
	recovery = &recoveryWriter{ResponseWriter: status}
	if err := recovery.Push("/asset", nil); err != nil {
		t.Fatalf("Push: %v", err)
	}
	if pusher.target != "/asset" {
		t.Fatalf("push target = %q", pusher.target)
	}

	reader := &wrapperReadFromRecorder{ResponseRecorder: httptest.NewRecorder()}
	status = &statusRecorder{ResponseWriter: reader}
	recovery = &recoveryWriter{ResponseWriter: status}
	n, err := recovery.ReadFrom(strings.NewReader("body"))
	if err != nil || n != 4 {
		t.Fatalf("ReadFrom = %d, %v", n, err)
	}
	if !reader.called || reader.Body.String() != "body" {
		t.Fatalf("ReadFrom did not reach the base writer: called=%v body=%q", reader.called, reader.Body.String())
	}
	if status.bytes != 4 {
		t.Fatalf("counted bytes = %d, want 4", status.bytes)
	}

	server, client := net.Pipe()
	t.Cleanup(func() {
		_ = server.Close()
		_ = client.Close()
	})
	hijacker := &wrapperHijackRecorder{
		ResponseRecorder: httptest.NewRecorder(),
		conn:             server,
		rw:               bufio.NewReadWriter(bufio.NewReader(server), bufio.NewWriter(server)),
	}
	status = &statusRecorder{ResponseWriter: hijacker}
	recovery = &recoveryWriter{ResponseWriter: status}
	conn, _, err := recovery.Hijack()
	if err != nil || conn != server {
		t.Fatalf("Hijack = %v, %v; want the base connection", conn, err)
	}
	if !recovery.wroteHeader {
		t.Fatal("a successful hijack did not mark the response as started")
	}

	plainStatus := &statusRecorder{ResponseWriter: httptest.NewRecorder()}
	plainStatus.Flush()
	if err := plainStatus.Push("/asset", nil); err != http.ErrNotSupported {
		t.Fatalf("Push on a plain writer = %v, want ErrNotSupported", err)
	}
	n, err = plainStatus.ReadFrom(strings.NewReader("fallback"))
	if err != nil || n != 8 {
		t.Fatalf("fallback ReadFrom = %d, %v", n, err)
	}
	if plainStatus.bytes != 8 {
		t.Fatalf("fallback counted bytes = %d, want 8", plainStatus.bytes)
	}

	plainRecovery := &recoveryWriter{ResponseWriter: httptest.NewRecorder()}
	plainRecovery.Flush()
	if err := plainRecovery.Push("/asset", nil); err != http.ErrNotSupported {
		t.Fatalf("Push on a plain recovery writer = %v, want ErrNotSupported", err)
	}
	n, err = plainRecovery.ReadFrom(strings.NewReader("fallback"))
	if err != nil || n != 8 {
		t.Fatalf("fallback recovery ReadFrom = %d, %v", n, err)
	}

	failing := &statusRecorder{ResponseWriter: failingHijack{ResponseRecorder: httptest.NewRecorder()}}
	recovery = &recoveryWriter{ResponseWriter: failing}
	if _, _, err := recovery.Hijack(); err == nil {
		t.Fatal("Hijack succeeded through a failing underlying writer")
	}

	plainRecovery = &recoveryWriter{ResponseWriter: &statusRecorder{ResponseWriter: httptest.NewRecorder()}}
	if _, _, err := plainRecovery.Hijack(); err != http.ErrNotSupported {
		t.Fatalf("Hijack on a plain writer = %v, want ErrNotSupported", err)
	}

	bare := &recoveryWriter{ResponseWriter: httptest.NewRecorder()}
	if _, _, err := bare.Hijack(); err != http.ErrNotSupported {
		t.Fatalf("Hijack with no underlying hijacker = %v, want ErrNotSupported", err)
	}

	fresh := &statusRecorder{ResponseWriter: httptest.NewRecorder()}
	if n, err := fresh.ReadFrom(strings.NewReader("fresh")); err != nil || n != 5 {
		t.Fatalf("fresh ReadFrom = %d, %v", n, err)
	}
	if fresh.status != http.StatusOK {
		t.Fatalf("fresh ReadFrom status = %d, want 200", fresh.status)
	}

	dupe := &statusRecorder{ResponseWriter: httptest.NewRecorder()}
	dupe.WriteHeader(http.StatusCreated)
	dupe.WriteHeader(http.StatusInternalServerError)
	if dupe.status != http.StatusCreated {
		t.Fatalf("second WriteHeader changed status to %d", dupe.status)
	}
}

type failingHijack struct {
	*httptest.ResponseRecorder
}

func (f failingHijack) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	return nil, nil, errors.New("hijack failed")
}
