package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestLogin(t *testing.T) {
	s := newServer(t)
	bad := s.post(t, "/api/login", `{"password":"no"}`, nil)
	if bad.Code != http.StatusUnauthorized {
		t.Fatalf("wrong password status %d", bad.Code)
	}
	code, _ := errCode(t, bad)
	if code != "login" {
		t.Fatalf("wrong password code %q", code)
	}
	if len(bad.Result().Cookies()) != 0 {
		t.Fatalf("wrong password set cookies: %v", bad.Header().Get("Set-Cookie"))
	}

	tok, session, csrf, rec := s.login(t)
	if rec.Code != http.StatusOK {
		t.Fatalf("login status %d body %s", rec.Code, rec.Body.Bytes())
	}
	if tok == "" || session == nil || csrf == nil {
		t.Fatalf("login tok %q session %v csrf %v", tok, session, csrf)
	}
	if session.Value != tok || csrf.Value != tok {
		t.Fatalf("cookie values session %q csrf %q tok %q", session.Value, csrf.Value, tok)
	}
	if !session.HttpOnly || session.SameSite != http.SameSiteLaxMode || session.Path != "/" {
		t.Fatalf("session cookie %+v", session)
	}
	if csrf.HttpOnly || csrf.SameSite != http.SameSiteLaxMode || csrf.Path != "/" {
		t.Fatalf("csrf cookie %+v", csrf)
	}
	var body struct {
		CSRF string `json:"csrf"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.CSRF != tok {
		t.Fatalf("body csrf %q", body.CSRF)
	}
}

func TestServices(t *testing.T) {
	s := newServer(t)
	no := s.get(t, "/api/services", nil, nil)
	if no.Code != http.StatusForbidden {
		t.Fatalf("no session status %d", no.Code)
	}
	code, _ := errCode(t, no)
	if code != "session" {
		t.Fatalf("no session code %q", code)
	}

	_, session, csrf, _ := s.login(t)
	rec := s.get(t, "/api/services", session, csrf)
	if rec.Code != http.StatusOK {
		t.Fatalf("list status %d body %s", rec.Code, rec.Body.Bytes())
	}
	var list []service
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 || list[0].ID != "web" || list[1].ID != "worker" {
		t.Fatalf("list %+v", list)
	}
	if list[0].Name == "" || list[1].Name == "" {
		t.Fatalf("names %+v", list)
	}
}

func TestStartStop(t *testing.T) {
	s := newServer(t)
	_, session, csrf, _ := s.login(t)

	noHeader := s.call(t, http.MethodPost, "/api/services/web/start", "", session, csrf, "")
	if noHeader.Code != http.StatusForbidden {
		t.Fatalf("no csrf status %d", noHeader.Code)
	}
	code, _ := errCode(t, noHeader)
	if code != "csrf" {
		t.Fatalf("no csrf code %q", code)
	}
	mismatch := s.call(t, http.MethodPost, "/api/services/web/start", "", session, csrf, "other")
	if mismatch.Code != http.StatusForbidden {
		t.Fatalf("bad csrf status %d", mismatch.Code)
	}
	if code, _ = errCode(t, mismatch); code != "csrf" {
		t.Fatalf("bad csrf code %q", code)
	}

	ok := s.call(t, http.MethodPost, "/api/services/web/start", "", session, csrf, csrf.Value)
	if ok.Code != http.StatusOK {
		t.Fatalf("start status %d body %s", ok.Code, ok.Body.Bytes())
	}
	var body struct {
		OK bool `json:"ok"`
	}
	if err := json.Unmarshal(ok.Body.Bytes(), &body); err != nil || !body.OK {
		t.Fatalf("start body %s", ok.Body.Bytes())
	}

	again := s.call(t, http.MethodPost, "/api/services/web/start", "", session, csrf, csrf.Value)
	if again.Code != http.StatusUnprocessableEntity {
		t.Fatalf("second start status %d body %s", again.Code, again.Body.Bytes())
	}
	code, msg := errCode(t, again)
	if code != "running" || msg != "Already running" {
		t.Fatalf("second start %q %q", code, msg)
	}

	stopped := s.call(t, http.MethodPost, "/api/services/web/stop", "", session, csrf, csrf.Value)
	if stopped.Code != http.StatusOK {
		t.Fatalf("stop status %d body %s", stopped.Code, stopped.Body.Bytes())
	}
	list := s.get(t, "/api/services", session, csrf)
	var services []service
	if err := json.Unmarshal(list.Body.Bytes(), &services); err != nil {
		t.Fatal(err)
	}
	if services[0].Running {
		t.Fatalf("web still running: %+v", services)
	}

	missing := s.call(t, http.MethodPost, "/api/services/nope/start", "", session, csrf, csrf.Value)
	if missing.Code != http.StatusNotFound {
		t.Fatalf("unknown id status %d", missing.Code)
	}
}

func TestEventsReplay(t *testing.T) {
	s := newServer(t)
	no := httptest.NewRecorder()
	s.ServeHTTP(no, httptest.NewRequest(http.MethodGet, "/api/events", nil))
	if no.Code != http.StatusForbidden {
		t.Fatalf("no session status %d", no.Code)
	}
	if code, _ := errCode(t, no); code != "session" {
		t.Fatalf("no session code %q body %s", code, no.Body.Bytes())
	}

	_, session, csrf, _ := s.login(t)
	w, stop := s.stream(t, "", session)
	defer stop()
	s.call(t, http.MethodPost, "/api/services/web/start", "", session, csrf, csrf.Value)
	got := waitEvents(t, w, 2)
	if len(got) != 2 || got[0].Event != "state" || got[1].Event != "log" {
		t.Fatalf("after start: %+v", got)
	}
	var st stateBody
	if err := json.Unmarshal([]byte(got[0].Data), &st); err != nil {
		t.Fatal(err)
	}
	if st.Error != "" || len(st.Services) != 2 || !st.Services[0].Running {
		t.Fatalf("state %+v", st)
	}
	var row logBody
	if err := json.Unmarshal([]byte(got[1].Data), &row); err != nil {
		t.Fatal(err)
	}
	if row.ID != got[1].ID || !strings.Contains(row.Text, "started") {
		t.Fatalf("log %+v id %d", row, got[1].ID)
	}
	if ct := w.hdr.Get("Content-Type"); ct != "text/event-stream" {
		t.Fatalf("content type %q", ct)
	}
	last := got[1].ID
	stop()

	s.call(t, http.MethodPost, "/api/services/web/stop", "", session, csrf, csrf.Value)
	w2, stop2 := s.stream(t, strconv.Itoa(last), session)
	defer stop2()
	next := waitEvents(t, w2, 2)
	if len(next) != 2 {
		t.Fatalf("replay len %d body %q", len(next), w2.String())
	}
	for _, ev := range next {
		if ev.ID <= last {
			t.Fatalf("replay included id %d, Last-Event-ID was %d (the server ignored the header)", ev.ID, last)
		}
	}
	if next[0].Event != "state" || next[1].Event != "log" {
		t.Fatalf("replay events %+v", next)
	}
	if err := json.Unmarshal([]byte(next[0].Data), &st); err != nil {
		t.Fatal(err)
	}
	if st.Services[0].Running {
		t.Fatalf("stop did not clear running: %+v", st)
	}
}

func TestEventCap(t *testing.T) {
	s := newServer(t)
	_, session, csrf, _ := s.login(t)
	// 60 successful start/stop pairs would be 120, but 30 pairs is 60
	// toggles and 120 events: state then log each time.
	const toggles = 60
	for i := 0; i < toggles; i++ {
		op := "start"
		if i%2 == 1 {
			op = "stop"
		}
		rec := s.call(t, http.MethodPost, "/api/services/web/"+op, "", session, csrf, csrf.Value)
		if rec.Code != http.StatusOK {
			t.Fatalf("toggle %d status %d body %s", i, rec.Code, rec.Body.Bytes())
		}
	}
	w, stop := s.stream(t, "", session)
	defer stop()
	got := waitEvents(t, w, maxEvents)
	if len(got) != maxEvents {
		t.Fatalf("new subscriber saw %d events, want %d", len(got), maxEvents)
	}
	wantFirst := toggles*2 - maxEvents + 1
	if got[0].ID != wantFirst || got[len(got)-1].ID != toggles*2 {
		t.Fatalf("id window %d..%d, want %d..%d", got[0].ID, got[len(got)-1].ID, wantFirst, toggles*2)
	}
	logs := 0
	for _, ev := range got {
		if ev.Event == "log" {
			logs++
		}
	}
	if logs > maxEvents {
		t.Fatalf("saw %d log events", logs)
	}
}

func TestPages(t *testing.T) {
	dir := t.TempDir()
	const sentinel = "sentinel-index"
	if err := os.WriteFile(filepath.Join(dir, "index.html"), []byte(sentinel), 0o644); err != nil {
		t.Fatal(err)
	}
	s := newHandler(dir).(*server)

	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET / status %d", rec.Code)
	}
	if rec.Body.String() != sentinel {
		t.Fatalf("GET / body %q", rec.Body.String())
	}
	if loc := rec.Header().Get("Location"); loc != "" {
		t.Fatalf("GET / redirected to %s", loc)
	}
	if rec.Header().Get("Content-Security-Policy") != csp {
		t.Fatalf("CSP %q", rec.Header().Get("Content-Security-Policy"))
	}
	if rec.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Fatalf("nosniff %q", rec.Header().Get("X-Content-Type-Options"))
	}

	head := httptest.NewRecorder()
	s.ServeHTTP(head, httptest.NewRequest(http.MethodHead, "/", nil))
	if head.Code != http.StatusOK || head.Body.Len() != 0 {
		t.Fatalf("HEAD / status %d body %q", head.Code, head.Body.String())
	}
	if head.Header().Get("Content-Security-Policy") != csp {
		t.Fatalf("HEAD CSP %q", head.Header().Get("Content-Security-Policy"))
	}

	index := httptest.NewRecorder()
	s.ServeHTTP(index, httptest.NewRequest(http.MethodGet, "/index.html", nil))
	if index.Code != http.StatusOK || index.Body.String() != sentinel {
		t.Fatalf("GET /index.html status %d body %q", index.Code, index.Body.String())
	}
	if loc := index.Header().Get("Location"); loc != "" {
		t.Fatalf("GET /index.html redirected to %s", loc)
	}

	missing := httptest.NewRecorder()
	s.ServeHTTP(missing, httptest.NewRequest(http.MethodGet, "/missing.js", nil))
	if missing.Code != http.StatusNotFound {
		t.Fatalf("missing.js status %d", missing.Code)
	}
	if strings.Contains(missing.Body.String(), sentinel) {
		t.Fatal("missing.js served the index")
	}

	api := httptest.NewRecorder()
	s.ServeHTTP(api, httptest.NewRequest(http.MethodGet, "/api/nope", nil))
	if api.Code == http.StatusOK || strings.Contains(api.Body.String(), sentinel) {
		t.Fatalf("/api/nope served the index: %d %q", api.Code, api.Body.String())
	}
}

type apiServer struct{ *server }

func newServer(t *testing.T) *apiServer {
	t.Helper()
	h := newHandler(t.TempDir())
	return &apiServer{h.(*server)}
}

func (s *apiServer) post(t *testing.T, target, body string, session *http.Cookie) *httptest.ResponseRecorder {
	t.Helper()
	return s.call(t, http.MethodPost, target, body, session, nil, "")
}

func (s *apiServer) get(t *testing.T, target string, session, csrf *http.Cookie) *httptest.ResponseRecorder {
	t.Helper()
	return s.call(t, http.MethodGet, target, "", session, csrf, "")
}

func (s *apiServer) call(t *testing.T, method, target, body string, session, csrf *http.Cookie, csrfHeader string) *httptest.ResponseRecorder {
	t.Helper()
	var r io.Reader
	if body != "" {
		r = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, target, r)
	if session != nil {
		req.AddCookie(session)
	}
	if csrf != nil {
		req.AddCookie(csrf)
	}
	if csrfHeader != "" {
		req.Header.Set("X-CSRF", csrfHeader)
	}
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, req)
	return rec
}

func (s *apiServer) login(t *testing.T) (string, *http.Cookie, *http.Cookie, *httptest.ResponseRecorder) {
	t.Helper()
	rec := s.post(t, "/api/login", `{"password":"`+demoPassword+`"}`, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("login status %d body %s", rec.Code, rec.Body.Bytes())
	}
	var body struct {
		CSRF string `json:"csrf"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	var session, csrf *http.Cookie
	for _, c := range rec.Result().Cookies() {
		switch c.Name {
		case "session":
			session = c
		case "csrf":
			csrf = c
		}
	}
	if session == nil || csrf == nil || body.CSRF == "" {
		t.Fatalf("login cookies session %v csrf %v body %q", session, csrf, body.CSRF)
	}
	return body.CSRF, session, csrf, rec
}

func errCode(t *testing.T, rec *httptest.ResponseRecorder) (string, string) {
	t.Helper()
	var env struct {
		Error apiError `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("error json: %v body %s", err, rec.Body.Bytes())
	}
	return env.Error.Code, env.Error.Message
}

// streamWriter is an http.ResponseWriter and http.Flusher. SSE writes are
// visible to the test as soon as the handler flushes, without Listen.
type streamWriter struct {
	mu    sync.Mutex
	hdr   http.Header
	code  int
	buf   bytes.Buffer
	flush chan struct{}
}

func newStreamWriter() *streamWriter {
	return &streamWriter{hdr: make(http.Header), flush: make(chan struct{}, 128)}
}

func (s *streamWriter) Header() http.Header { return s.hdr }

func (s *streamWriter) WriteHeader(code int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.code == 0 {
		s.code = code
	}
}

func (s *streamWriter) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.code == 0 {
		s.code = http.StatusOK
	}
	return s.buf.Write(p)
}

func (s *streamWriter) Flush() {
	select {
	case s.flush <- struct{}{}:
	default:
	}
}

func (s *streamWriter) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buf.String()
}

func (s *apiServer) stream(t *testing.T, last string, session *http.Cookie) (*streamWriter, func()) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	req := httptest.NewRequestWithContext(ctx, http.MethodGet, "/api/events", nil)
	if last != "" {
		req.Header.Set("Last-Event-ID", last)
	}
	req.AddCookie(session)
	w := newStreamWriter()
	done := make(chan struct{})
	go func() {
		s.ServeHTTP(w, req)
		close(done)
	}()
	var once sync.Once
	stop := func() {
		once.Do(func() {
			cancel()
			select {
			case <-done:
			case <-time.After(2 * time.Second):
				t.Errorf("event stream did not return")
			}
		})
	}
	t.Cleanup(stop)
	return w, stop
}

type sseMsg struct {
	ID    int
	Event string
	Data  string
}

func parseSSE(s string) []sseMsg {
	if !strings.HasSuffix(s, "\n\n") {
		i := strings.LastIndex(s, "\n\n")
		if i < 0 {
			return nil
		}
		s = s[:i+2]
	}
	var out []sseMsg
	for _, block := range strings.Split(s, "\n\n") {
		block = strings.TrimSpace(block)
		if block == "" {
			continue
		}
		var m sseMsg
		for _, line := range strings.Split(block, "\n") {
			switch {
			case strings.HasPrefix(line, "id: "):
				m.ID, _ = strconv.Atoi(strings.TrimPrefix(line, "id: "))
			case strings.HasPrefix(line, "event: "):
				m.Event = strings.TrimPrefix(line, "event: ")
			case strings.HasPrefix(line, "data: "):
				m.Data = strings.TrimPrefix(line, "data: ")
			}
		}
		if m.Event != "" {
			out = append(out, m)
		}
	}
	return out
}

func waitEvents(t *testing.T, w *streamWriter, n int) []sseMsg {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	last, stable := -1, 0
	for time.Now().Before(deadline) {
		msgs := parseSSE(w.String())
		if len(msgs) >= n {
			if len(msgs) == last {
				stable++
				if stable >= 3 {
					return msgs
				}
			} else {
				stable = 0
				last = len(msgs)
			}
		}
		select {
		case <-w.flush:
		case <-time.After(5 * time.Millisecond):
		}
	}
	t.Fatalf("timeout with %d events, body %q", len(parseSSE(w.String())), w.String())
	return nil
}
