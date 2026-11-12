// Command admin serves a den build and a same-origin API for the admin page.
//
// -dist is required and is not embedded: go test can pass a temporary
// directory, and a clean tree does not need the page to have been built.
// The password is the constant den, an example value, not a secret.
package main

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
)

// csp is the policy den writes into index.html and sends from den dev
// (cmd/den). It is copied here so this example does not import that main
// package. A test checks the header against this string.
const csp = "default-src 'self'; script-src 'self'; style-src 'self'; img-src 'self' data: https:; " +
	"object-src 'none'; base-uri 'none'; form-action 'self'; " +
	"require-trusted-types-for 'script'; trusted-types 'none'"

// demoPassword is the sign-in password. It is an example, not a secret.
const demoPassword = "den"

// maxEvents is how many state and log events a new subscriber can see.
// Capping the buffer caps log events at 50 as well.
const maxEvents = 50

func main() {
	dist := flag.String("dist", "", "directory of a den build (required)")
	addr := flag.String("addr", "localhost:8091", "listen address")
	flag.Parse()
	if *dist == "" {
		fmt.Fprintln(os.Stderr, "admin: -dist is required")
		os.Exit(2)
	}
	info, err := os.Stat(*dist)
	if err != nil || !info.IsDir() {
		fmt.Fprintln(os.Stderr, "admin: -dist must be a directory")
		os.Exit(2)
	}
	if err := http.ListenAndServe(*addr, newHandler(*dist)); err != nil {
		fmt.Fprintln(os.Stderr, "admin:", err)
		os.Exit(1)
	}
}

type service struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Running bool   `json:"running"`
}

type stateBody struct {
	Services []service `json:"services"`
	Error    string    `json:"error"`
}

type logBody struct {
	ID   int    `json:"id"`
	Text string `json:"text"`
}

type sseEvent struct {
	ID   int
	Name string
	Data string
}

type server struct {
	dist     string
	mu       sync.Mutex
	sessions map[string]struct{}
	services []service
	seq      int
	buf      []sseEvent
	subs     map[chan sseEvent]struct{}
}

func newHandler(dist string) http.Handler {
	return &server{
		dist:     dist,
		sessions: map[string]struct{}{},
		services: []service{
			{ID: "web", Name: "Web", Running: false},
			{ID: "worker", Name: "Worker", Running: false},
		},
		subs: map[chan sseEvent]struct{}{},
	}
}

func (s *server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Security-Policy", csp)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "no-store")
	p := path.Clean("/" + r.URL.Path)
	if p == "/api" || strings.HasPrefix(p, "/api/") {
		s.serveAPI(w, r, p)
		return
	}
	s.serveAsset(w, r, p)
}

func (s *server) serveAPI(w http.ResponseWriter, r *http.Request, p string) {
	switch {
	case p == "/api/login" && r.Method == http.MethodPost:
		s.serveLogin(w, r)
	case p == "/api/services" && r.Method == http.MethodGet:
		s.serveServices(w, r)
	case p == "/api/events" && r.Method == http.MethodGet:
		s.serveEvents(w, r)
	default:
		if id, start, ok := serviceOp(p); ok && r.Method == http.MethodPost {
			s.serveToggle(w, r, id, start)
			return
		}
		writeErr(w, http.StatusNotFound, "not_found", "Not found")
	}
}

func serviceOp(p string) (string, bool, bool) {
	rest, ok := strings.CutPrefix(p, "/api/services/")
	if !ok {
		return "", false, false
	}
	id, op, ok := strings.Cut(rest, "/")
	if !ok || id == "" || strings.Contains(op, "/") {
		return "", false, false
	}
	switch op {
	case "start":
		return id, true, true
	case "stop":
		return id, false, true
	default:
		return "", false, false
	}
}

func (s *server) serveLogin(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Password string `json:"password"`
	}
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096))
	if err := dec.Decode(&body); err != nil || body.Password != demoPassword {
		writeErr(w, http.StatusUnauthorized, "login", "Wrong password")
		return
	}
	tok, err := newToken()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "login", "Could not start a session")
		return
	}
	s.mu.Lock()
	s.sessions[tok] = struct{}{}
	s.mu.Unlock()
	http.SetCookie(w, &http.Cookie{
		Name:     "session",
		Value:    tok,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})
	http.SetCookie(w, &http.Cookie{
		Name:     "csrf",
		Value:    tok,
		Path:     "/",
		SameSite: http.SameSiteLaxMode,
	})
	writeJSON(w, http.StatusOK, map[string]string{"csrf": tok})
}

func newToken() (string, error) {
	var b [32]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(b[:]), nil
}

func (s *server) authed(r *http.Request) bool {
	c, err := r.Cookie("session")
	if err != nil || c.Value == "" {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ok := s.sessions[c.Value]
	return ok
}

func csrfOK(r *http.Request) bool {
	c, err := r.Cookie("csrf")
	if err != nil || c.Value == "" {
		return false
	}
	return r.Header.Get("X-CSRF") == c.Value
}

func (s *server) serveServices(w http.ResponseWriter, r *http.Request) {
	if !s.authed(r) {
		writeErr(w, http.StatusForbidden, "session", "Sign in")
		return
	}
	s.mu.Lock()
	snap := cloneServices(s.services)
	s.mu.Unlock()
	writeJSON(w, http.StatusOK, snap)
}

func (s *server) serveToggle(w http.ResponseWriter, r *http.Request, id string, start bool) {
	if !s.authed(r) {
		writeErr(w, http.StatusForbidden, "session", "Sign in")
		return
	}
	if !csrfOK(r) {
		writeErr(w, http.StatusForbidden, "csrf", "Bad CSRF token")
		return
	}
	status, code, msg := s.apply(id, start)
	if status != http.StatusOK {
		writeErr(w, status, code, msg)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *server) apply(id string, start bool) (int, string, string) {
	s.mu.Lock()
	ix := -1
	for i := range s.services {
		if s.services[i].ID == id {
			ix = i
			break
		}
	}
	if ix < 0 {
		s.mu.Unlock()
		return http.StatusNotFound, "missing", "No such service"
	}
	if start && s.services[ix].Running {
		snap := cloneServices(s.services)
		s.mu.Unlock()
		s.emit(snap, "Already running", id+" already running")
		return http.StatusUnprocessableEntity, "running", "Already running"
	}
	s.services[ix].Running = start
	snap := cloneServices(s.services)
	s.mu.Unlock()
	text := id + " stopped"
	if start {
		text = id + " started"
	}
	s.emit(snap, "", text)
	return http.StatusOK, "", ""
}

func cloneServices(in []service) []service {
	out := make([]service, len(in))
	copy(out, in)
	return out
}

func (s *server) emit(snap []service, errText, logText string) {
	body, err := json.Marshal(stateBody{Services: snap, Error: errText})
	if err != nil {
		return
	}
	s.publish("state", string(body))
	s.publishLog(logText)
}

func (s *server) publish(name, data string) {
	s.mu.Lock()
	s.seq++
	ev := sseEvent{ID: s.seq, Name: name, Data: data}
	subs := s.enqueueLocked(ev)
	s.mu.Unlock()
	deliver(subs, ev)
}

func (s *server) publishLog(text string) {
	s.mu.Lock()
	s.seq++
	id := s.seq
	data, err := json.Marshal(logBody{ID: id, Text: text})
	if err != nil {
		s.mu.Unlock()
		return
	}
	ev := sseEvent{ID: id, Name: "log", Data: string(data)}
	subs := s.enqueueLocked(ev)
	s.mu.Unlock()
	deliver(subs, ev)
}

func (s *server) enqueueLocked(ev sseEvent) []chan sseEvent {
	s.buf = append(s.buf, ev)
	if len(s.buf) > maxEvents {
		s.buf = append([]sseEvent(nil), s.buf[len(s.buf)-maxEvents:]...)
	}
	subs := make([]chan sseEvent, 0, len(s.subs))
	for ch := range s.subs {
		subs = append(subs, ch)
	}
	return subs
}

func deliver(subs []chan sseEvent, ev sseEvent) {
	for _, ch := range subs {
		ch <- ev
	}
}

func (s *server) listen(last int) (chan sseEvent, []sseEvent) {
	s.mu.Lock()
	defer s.mu.Unlock()
	ch := make(chan sseEvent, 64)
	s.subs[ch] = struct{}{}
	var replay []sseEvent
	for _, ev := range s.buf {
		if ev.ID > last {
			replay = append(replay, ev)
		}
	}
	return ch, replay
}

func (s *server) drop(ch chan sseEvent) {
	s.mu.Lock()
	delete(s.subs, ch)
	s.mu.Unlock()
}

func (s *server) serveEvents(w http.ResponseWriter, r *http.Request) {
	if !s.authed(r) {
		writeErr(w, http.StatusForbidden, "session", "Sign in")
		return
	}
	fl, ok := w.(http.Flusher)
	if !ok {
		writeErr(w, http.StatusInternalServerError, "stream", "Streaming is not available")
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	last := 0
	if h := r.Header.Get("Last-Event-ID"); h != "" {
		if n, err := strconv.Atoi(h); err == nil {
			last = n
		}
	}
	ch, replay := s.listen(last)
	defer s.drop(ch)
	for _, ev := range replay {
		writeSSE(w, ev)
		fl.Flush()
	}
	for {
		select {
		case <-r.Context().Done():
			return
		case ev := <-ch:
			writeSSE(w, ev)
			fl.Flush()
		}
	}
}

func writeSSE(w http.ResponseWriter, ev sseEvent) {
	fmt.Fprintf(w, "id: %d\nevent: %s\ndata: %s\n\n", ev.ID, ev.Name, ev.Data)
}

func (s *server) serveAsset(w http.ResponseWriter, r *http.Request, p string) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	base := path.Base(p)
	rel := strings.TrimPrefix(p, "/")
	if strings.Contains(base, ".") {
		if !s.serveFile(w, r, rel) {
			http.NotFound(w, r)
		}
		return
	}
	if rel != "" && s.isFile(rel) {
		if !s.serveFile(w, r, rel) {
			http.NotFound(w, r)
		}
		return
	}
	// No "." in the last segment, and not a file that is already there:
	// the app page, through ServeContent, so a URL is not rewritten onto
	// FileServer (that would redirect a path ending in /index.html).
	if !s.serveFile(w, r, "index.html") {
		http.NotFound(w, r)
	}
}

func (s *server) isFile(rel string) bool {
	f, _, err := s.open(rel)
	if err != nil {
		return false
	}
	f.Close()
	return true
}

func (s *server) serveFile(w http.ResponseWriter, r *http.Request, rel string) bool {
	f, info, err := s.open(rel)
	if err != nil {
		return false
	}
	defer f.Close()
	http.ServeContent(w, r, info.Name(), info.ModTime(), f)
	return true
}

func (s *server) open(rel string) (*os.File, os.FileInfo, error) {
	rel = path.Clean(rel)
	if rel == "." || rel == ".." || strings.HasPrefix(rel, "../") {
		return nil, nil, os.ErrNotExist
	}
	full := filepath.Join(s.dist, filepath.FromSlash(rel))
	f, err := os.Open(full)
	if err != nil {
		return nil, nil, err
	}
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() {
		f.Close()
		if err == nil {
			err = os.ErrNotExist
		}
		return nil, nil, err
	}
	return f, info, nil
}

type apiError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func writeErr(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, map[string]apiError{
		"error": {Code: code, Message: message},
	})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
