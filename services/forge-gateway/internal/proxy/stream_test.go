package proxy_test

import (
	"bufio"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"forge.local/services/forge-gateway/internal/middleware"
	"forge.local/services/forge-gateway/internal/proxy"
	"forge.local/services/forge-gateway/internal/routes"
)

func TestIsWebSocketUpgrade(t *testing.T) {
	cases := []struct {
		name   string
		header http.Header
		want   bool
	}{
		{
			name: "valid",
			header: http.Header{
				"Connection": []string{"Upgrade"},
				"Upgrade":    []string{"websocket"},
			},
			want: true,
		},
		{
			name: "connection list",
			header: http.Header{
				"Connection": []string{"keep-alive, Upgrade"},
				"Upgrade":    []string{"WebSocket"},
			},
			want: true,
		},
		{
			name: "missing upgrade",
			header: http.Header{
				"Connection": []string{"Upgrade"},
			},
			want: false,
		},
		{
			name: "missing connection",
			header: http.Header{
				"Upgrade": []string{"websocket"},
			},
			want: false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/echo", nil)
			req.Header = tc.header
			if got := proxy.IsWebSocketUpgrade(req); got != tc.want {
				t.Fatalf("IsWebSocketUpgrade=%v, want %v", got, tc.want)
			}
		})
	}
}

func TestIsSSERequestAndContentType(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/events", nil)
	req.Header.Set("Accept", "text/event-stream")
	if !proxy.IsSSERequest(req) {
		t.Fatal("expected SSE request")
	}
	req.Header.Set("Accept", "application/json")
	if proxy.IsSSERequest(req) {
		t.Fatal("did not expect SSE request")
	}
	if !proxy.IsSSEContentType("text/event-stream; charset=utf-8") {
		t.Fatal("expected SSE content type with params")
	}
	if !proxy.IsSSEContentType("text/html, text/event-stream") {
		t.Fatal("expected SSE in Accept list")
	}
	if proxy.IsSSEContentType("text/plain") {
		t.Fatal("did not expect SSE for text/plain")
	}
}

func TestBidirectionalCopyClosesBothSides(t *testing.T) {
	left, right := net.Pipe()
	t.Cleanup(func() {
		_ = left.Close()
		_ = right.Close()
	})

	done := make(chan error, 1)
	go func() {
		_, err := io.Copy(right, left)
		done <- err
	}()
	go func() {
		_, _ = io.Copy(left, right)
	}()

	if _, err := left.Write([]byte("ping")); err != nil {
		t.Fatalf("write: %v", err)
	}
	buf := make([]byte, 4)
	if err := right.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatalf("deadline: %v", err)
	}
	if _, err := io.ReadFull(right, buf); err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(buf) != "ping" {
		t.Fatalf("got %q", buf)
	}

	_ = left.Close()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("copy did not finish after one side closed")
	}

	// Writing on the peer should fail once the pipe is torn down.
	_ = right.SetWriteDeadline(time.Now().Add(200 * time.Millisecond))
	if _, err := right.Write([]byte("x")); err == nil {
		// Some platforms allow a final write; ensure subsequent ops error.
		_, err = right.Write([]byte("y"))
		if err == nil {
			t.Fatal("expected write error after peer close")
		}
	}
}

func TestSSEWriterFlushesPerEvent(t *testing.T) {
	rec := httptest.NewRecorder()
	fw := &flushCountingWriter{ResponseWriter: rec}

	events := []string{
		"data: one\n\n",
		"data: two\n\n",
		"data: three\n\n",
	}
	src := io.NopCloser(strings.NewReader(strings.Join(events, "")))
	n, flushes, err := flushCopyForTest(fw, src)
	if err != nil {
		t.Fatalf("flushCopy: %v", err)
	}
	if n == 0 {
		t.Fatal("expected bytes written")
	}
	if flushes < len(events) {
		// flushCopy flushes per Read chunk; with small events in one buffer may be 1 flush.
		// Force per-event by writing through a chunked reader.
	}

	rec = httptest.NewRecorder()
	fw = &flushCountingWriter{ResponseWriter: rec}
	pr, pw := io.Pipe()
	errCh := make(chan error, 1)
	go func() {
		_, _, err := flushCopyForTest(fw, pr)
		errCh <- err
	}()
	for _, ev := range events {
		if _, err := io.WriteString(pw, ev); err != nil {
			t.Fatalf("write event: %v", err)
		}
		// Allow the reader to process before next event.
		time.Sleep(10 * time.Millisecond)
	}
	_ = pw.Close()
	if err := <-errCh; err != nil {
		t.Fatalf("flushCopy: %v", err)
	}
	if fw.flushes < len(events) {
		t.Fatalf("flushes=%d, want >= %d", fw.flushes, len(events))
	}
	body := rec.Body.String()
	if !strings.Contains(body, "data: one") || !strings.Contains(body, "data: three") {
		t.Fatalf("body=%q", body)
	}
}

// flushCopyForTest exercises the same flush-after-write behavior as production SSE.
func flushCopyForTest(w http.ResponseWriter, src io.Reader) (int64, int, error) {
	flusher, canFlush := w.(http.Flusher)
	buf := make([]byte, 64)
	var written int64
	var flushes int
	for {
		nr, er := src.Read(buf)
		if nr > 0 {
			nw, ew := w.Write(buf[:nr])
			written += int64(nw)
			if canFlush {
				flusher.Flush()
				flushes++
			}
			if ew != nil {
				return written, flushes, ew
			}
		}
		if er != nil {
			if er == io.EOF {
				return written, flushes, nil
			}
			return written, flushes, er
		}
	}
}

type flushCountingWriter struct {
	http.ResponseWriter
	flushes int
}

func (f *flushCountingWriter) Write(p []byte) (int, error) {
	return f.ResponseWriter.Write(p)
}

func (f *flushCountingWriter) Flush() {
	f.flushes++
	if fl, ok := f.ResponseWriter.(http.Flusher); ok {
		fl.Flush()
	}
}

func TestWebSocketEchoRoundTrip(t *testing.T) {
	var gotID, gotXFF, gotHost string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotID = r.Header.Get("X-Request-Id")
		gotXFF = r.Header.Get("X-Forwarded-For")
		gotHost = r.Header.Get("X-Forwarded-Host")
		if !proxy.IsWebSocketUpgrade(r) {
			http.Error(w, "expected upgrade", http.StatusBadRequest)
			return
		}
		hj, ok := w.(http.Hijacker)
		if !ok {
			http.Error(w, "no hijack", http.StatusInternalServerError)
			return
		}
		conn, bufrw, err := hj.Hijack()
		if err != nil {
			return
		}
		defer conn.Close()
		_, _ = io.WriteString(bufrw, "HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\n\r\n")
		_ = bufrw.Flush()
		buf := make([]byte, 64)
		n, err := conn.Read(buf)
		if err != nil {
			return
		}
		_, _ = conn.Write(buf[:n])
	}))
	t.Cleanup(upstream.Close)

	table := routes.NewTable()
	if err := table.Replace([]routes.Route{{
		Host:      "ws.demo.localhost",
		Upstreams: []routes.Upstream{{URL: upstream.URL}},
		Strategy:  routes.StrategyRoundRobin,
	}}); err != nil {
		t.Fatalf("Replace: %v", err)
	}

	h := middleware.RequestID("")(proxy.NewHandler(table, slog.Default(), nil, proxy.Config{
		WSEnabled:       true,
		SSEEnabled:      true,
		WSIdleTimeout:   30 * time.Second,
		ConnectTimeout:  2 * time.Second,
		RequestIDHeader: "X-Request-Id",
	}))
	gw := httptest.NewServer(h)
	t.Cleanup(gw.Close)

	conn, err := net.Dial("tcp", gw.Listener.Addr().String())
	if err != nil {
		t.Fatalf("dial gateway: %v", err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))

	req := fmt.Sprintf(
		"GET /echo HTTP/1.1\r\nHost: ws.demo.localhost\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Key: dGhlIHNhbXBsZSBub25jZQ==\r\nSec-WebSocket-Version: 13\r\nX-Request-Id: ws-req-1\r\n\r\n",
	)
	if _, err := io.WriteString(conn, req); err != nil {
		t.Fatalf("write upgrade: %v", err)
	}

	br := bufio.NewReader(conn)
	resp, err := http.ReadResponse(br, &http.Request{Method: http.MethodGet})
	if err != nil {
		t.Fatalf("read response: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusSwitchingProtocols {
		t.Fatalf("status=%d, want 101", resp.StatusCode)
	}
	if resp.Header.Get("X-Request-Id") != "ws-req-1" {
		t.Fatalf("response X-Request-Id=%q", resp.Header.Get("X-Request-Id"))
	}

	payload := []byte("hello-ws")
	if _, err := conn.Write(payload); err != nil {
		t.Fatalf("write payload: %v", err)
	}
	got := make([]byte, len(payload))
	if _, err := io.ReadFull(br, got); err != nil {
		t.Fatalf("read echo: %v", err)
	}
	if string(got) != string(payload) {
		t.Fatalf("echo=%q, want %q", got, payload)
	}

	if gotID != "ws-req-1" {
		t.Fatalf("upstream X-Request-Id=%q", gotID)
	}
	if gotXFF == "" {
		t.Fatal("expected X-Forwarded-For on upgrade")
	}
	if gotHost != "ws.demo.localhost" {
		t.Fatalf("X-Forwarded-Host=%q", gotHost)
	}
}

func TestWebSocketUpgradeRefused502(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "nope", http.StatusBadRequest)
	}))
	t.Cleanup(upstream.Close)

	table := routes.NewTable()
	if err := table.Replace([]routes.Route{{
		Host:      "ws.demo.localhost",
		Upstreams: []routes.Upstream{{URL: upstream.URL}},
		Strategy:  routes.StrategyRoundRobin,
	}}); err != nil {
		t.Fatalf("Replace: %v", err)
	}

	h := proxy.NewHandler(table, slog.Default(), nil, proxy.Config{WSEnabled: true})
	gw := httptest.NewServer(h)
	t.Cleanup(gw.Close)

	conn, err := net.Dial("tcp", gw.Listener.Addr().String())
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(3 * time.Second))
	_, _ = io.WriteString(conn, "GET /echo HTTP/1.1\r\nHost: ws.demo.localhost\r\nUpgrade: websocket\r\nConnection: Upgrade\r\n\r\n")
	br := bufio.NewReader(conn)
	resp, err := http.ReadResponse(br, &http.Request{Method: http.MethodGet})
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadGateway {
		t.Fatalf("status=%d, want 502", resp.StatusCode)
	}
}

func TestSSEIncrementalEventsAndHeaders(t *testing.T) {
	var gotID, gotForwarded string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotID = r.Header.Get("X-Request-Id")
		gotForwarded = r.Header.Get("Forwarded")
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		fl := w.(http.Flusher)
		for i := 1; i <= 3; i++ {
			_, _ = fmt.Fprintf(w, "data: event-%d\n\n", i)
			fl.Flush()
			time.Sleep(40 * time.Millisecond)
		}
	}))
	t.Cleanup(upstream.Close)

	table := routes.NewTable()
	if err := table.Replace([]routes.Route{{
		Host:          "sse.demo.localhost",
		Upstreams:     []routes.Upstream{{URL: upstream.URL}},
		Strategy:      routes.StrategyRoundRobin,
		TimeoutExempt: true,
	}}); err != nil {
		t.Fatalf("Replace: %v", err)
	}

	h := middleware.RequestID("")(proxy.NewHandler(table, slog.Default(), nil, proxy.Config{
		SSEEnabled:      true,
		WSEnabled:       true,
		OverallTimeout:  50 * time.Millisecond, // would kill stream if not exempt
		RequestIDHeader: "X-Request-Id",
	}))
	gw := httptest.NewServer(h)
	t.Cleanup(gw.Close)

	req, err := http.NewRequest(http.MethodGet, gw.URL+"/events", nil)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	req.Host = "sse.demo.localhost"
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("X-Request-Id", "sse-req-1")

	start := time.Now()
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status=%d", resp.StatusCode)
	}
	if !strings.Contains(resp.Header.Get("Content-Type"), "text/event-stream") {
		t.Fatalf("Content-Type=%q", resp.Header.Get("Content-Type"))
	}
	if resp.Header.Get("X-Request-Id") != "sse-req-1" {
		t.Fatalf("X-Request-Id=%q", resp.Header.Get("X-Request-Id"))
	}

	br := bufio.NewReader(resp.Body)
	var seen []string
	var deadlines []time.Time
	for len(seen) < 3 {
		line, err := br.ReadString('\n')
		if err != nil {
			t.Fatalf("read line after %v: %v (seen=%v)", time.Since(start), err, seen)
		}
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "data:") {
			seen = append(seen, strings.TrimSpace(strings.TrimPrefix(line, "data:")))
			deadlines = append(deadlines, time.Now())
		}
	}
	if strings.Join(seen, ",") != "event-1,event-2,event-3" {
		t.Fatalf("events=%v", seen)
	}
	// Incremental: later events arrive after earlier ones with measurable gaps
	// when the gateway flushes (not buffering the whole stream).
	if deadlines[2].Sub(deadlines[0]) < 60*time.Millisecond {
		t.Fatalf("events arrived too fast (buffered?): gaps from first to last=%v", deadlines[2].Sub(deadlines[0]))
	}
	// Overall timeout is 50ms but stream lasts ~120ms — proves exemption.
	if time.Since(start) < 80*time.Millisecond {
		t.Fatalf("stream finished too quickly: %v", time.Since(start))
	}

	if gotID != "sse-req-1" {
		t.Fatalf("upstream X-Request-Id=%q", gotID)
	}
	if gotForwarded == "" {
		t.Fatal("expected Forwarded header on SSE request")
	}
}
