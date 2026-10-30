// Package web serves prototypes, the agent API and the overview.
//
// Access follows docs/adr/0002-authentication.md:
//
//   - Every request must name an allowed Host (DNS rebinding).
//   - Pages, hub.js, the overview and /api/health are open to the browser.
//   - A page reports into its own inbox with its event token.
//   - Everything else needs the agent token and the X-Vitrine header.
//
// vitrine never sends CORS headers, so other websites cannot read responses
// or send the headers above.
package web

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/obuchmann/vitrine/internal/auth"
	"github.com/obuchmann/vitrine/internal/store"
)

//go:embed hub.js
var hubJS []byte

//go:embed comments.js
var commentsJS []byte

//go:embed overview.html
var overviewHTML string

// EventTokenHeader carries the event token of a page.
const EventTokenHeader = "X-Vitrine-Event-Token"

// Waits are bounded so a forgotten curl does not hold a connection forever.
const (
	DefaultNextWait = 10 * time.Minute
	MaxNextWait     = time.Hour
	keepAlive       = 25 * time.Second
)

// Server is the HTTP side of vitrine.
type Server struct {
	Store        *store.Store
	PublicURL    string   // e.g. http://127.0.0.1:8720, no trailing slash
	Token        string   // the agent token
	AuthFile     string   // where agents find the token; shown in error hints
	AllowedHosts []string // accepted Host headers, e.g. 127.0.0.1:8720
	Version      string
	Log          *slog.Logger

	hosts    map[string]bool
	overview *template.Template
}

// Handler returns all routes.
func (s *Server) Handler() http.Handler {
	s.hosts = map[string]bool{}
	for _, h := range s.AllowedHosts {
		s.hosts[strings.ToLower(h)] = true
	}
	s.overview = template.Must(template.New("overview").Funcs(template.FuncMap{
		"at": func(t time.Time) string { return t.Local().Format("Jan 2 15:04") },
		"kb": func(n int) string { return fmt.Sprintf("%.0f KB", float64(n)/1024+0.5) },
	}).Parse(overviewHTML))

	mux := http.NewServeMux()
	// Open to the browser.
	mux.HandleFunc("GET /{$}", s.handleOverview)
	mux.HandleFunc("GET /favicon.ico", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
	mux.HandleFunc("GET /api/health", s.handleHealth)
	mux.HandleFunc("GET /_hub/hub.js", s.handleHubJS)
	mux.HandleFunc("GET /_hub/comments.js", s.handleCommentsJS)
	mux.HandleFunc("GET /p/{repo}/{name}", s.handleProtoRedirect)
	mux.HandleFunc("GET /p/{repo}/{name}/{$}", s.handleProto)
	// A page with its event token, or an agent.
	mux.HandleFunc("POST /api/events/{repo}/{name}", s.handleEmit)
	mux.HandleFunc("GET /api/events/stream", s.handleStream)
	// Agents only.
	mux.HandleFunc("GET /api/p", s.agent(s.handleList))
	mux.HandleFunc("GET /api/p/{repo}/{name}", s.agent(s.handleGet))
	mux.HandleFunc("POST /api/p/{repo}/{name}", s.agent(s.handleUpload))
	mux.HandleFunc("DELETE /api/p/{repo}/{name}", s.agent(s.handleDelete))
	mux.HandleFunc("POST /api/p/{repo}/{name}/extend", s.agent(s.handleExtend))
	mux.HandleFunc("POST /api/p/{repo}/{name}/message", s.agent(s.handleMessage))
	mux.HandleFunc("POST /api/p/{repo}/{name}/reply", s.agent(s.handleReply))
	mux.HandleFunc("POST /api/p/{repo}/{name}/status", s.agent(s.handleStatus))
	mux.HandleFunc("POST /api/p/{repo}/{name}/point", s.agent(s.handlePoint))
	mux.HandleFunc("GET /api/events/{repo}/{name}", s.agent(s.handleListEvents))
	mux.HandleFunc("GET /api/events/next", s.agent(s.handleNext))
	return s.checkHost(mux)
}

// checkHost rejects requests for other host names. A website that rebinds
// its DNS name to 127.0.0.1 still sends its own name.
func (s *Server) checkHost(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !s.hosts[strings.ToLower(r.Host)] {
			writeJSON(w, http.StatusForbidden, apiError{"host_not_allowed", "Use " + s.PublicURL})
			return
		}
		w.Header().Set("X-Content-Type-Options", "nosniff")
		next.ServeHTTP(w, r)
	})
}

func (s *Server) isAgent(r *http.Request) bool {
	return r.Header.Get(auth.Header) != "" && auth.Equal(auth.Bearer(r), s.Token)
}

func (s *Server) agent(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !s.isAgent(r) {
			s.unauthorized(w)
			return
		}
		h(w, r)
	}
}

func (s *Server) unauthorized(w http.ResponseWriter) {
	hint := "Send the headers Authorization: Bearer <token> and " + auth.Header + ": 1."
	if s.AuthFile != "" {
		hint = fmt.Sprintf("Send the headers from the token file: curl -H @%q …", s.AuthFile)
	}
	writeJSON(w, http.StatusUnauthorized, apiError{"unauthorized", hint})
}

func pathName(r *http.Request) string { return r.PathValue("repo") + "/" + r.PathValue("name") }

// URL is the address of a prototype.
func (s *Server) URL(name string) string { return s.PublicURL + "/p/" + name + "/" }

type apiError struct {
	Error string `json:"error"`
	Hint  string `json:"hint,omitempty"`
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	enc.Encode(v)
}

// writeErr maps store errors to status codes and hints agents can act on.
func (s *Server) writeErr(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, store.ErrUnknown):
		writeJSON(w, http.StatusNotFound, apiError{"unknown_prototype", "Not (or no longer) there: it expired or was deleted. Publish it again from the repository."})
	case errors.Is(err, store.ErrTooLarge):
		writeJSON(w, http.StatusRequestEntityTooLarge, apiError{err.Error(), ""})
	case errors.Is(err, store.ErrStoreFull):
		writeJSON(w, http.StatusInsufficientStorage, apiError{"store_full", "vitrine's storage limit is reached. Delete prototypes you no longer need, or fetch and acknowledge their unread events."})
	case errors.Is(err, store.ErrInboxFull):
		writeJSON(w, http.StatusTooManyRequests, apiError{"inbox_full", "The inbox of this prototype is full of unread events. The agent has to fetch and acknowledge them first."})
	case errors.Is(err, store.ErrInvalidName), errors.Is(err, store.ErrInvalidType), errors.Is(err, store.ErrEmptyContent),
		errors.Is(err, store.ErrReservedType), errors.Is(err, store.ErrInvalidState), errors.Is(err, store.ErrUnknownThread),
		errors.Is(err, store.ErrNoTarget), errors.Is(err, store.ErrEmptyText):
		writeJSON(w, http.StatusBadRequest, apiError{err.Error(), ""})
	default:
		s.Log.Error("request failed", "err", err)
		writeJSON(w, http.StatusInternalServerError, apiError{"internal_error", "See the vitrine log."})
	}
}

func badRequest(w http.ResponseWriter, msg, hint string) {
	writeJSON(w, http.StatusBadRequest, apiError{msg, hint})
}

func (s *Server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"service": "vitrine", "version": s.Version})
}

// PublishResult is returned by the upload.
type PublishResult struct {
	Name      string    `json:"name"`
	URL       string    `json:"url"`
	Version   int       `json:"version"`
	ExpiresAt time.Time `json:"expires_at"`
	KeepFor   string    `json:"keep_for,omitempty"`
	Warning   string    `json:"warning,omitempty"`
}

// NewPublishResult builds the answer for an upload.
func (s *Server) NewPublishResult(info store.Info, warning string) PublishResult {
	return PublishResult{Name: info.Name, URL: s.URL(info.Name), Version: info.Version,
		ExpiresAt: info.ExpiresAt, KeepFor: info.KeepFor, Warning: warning}
}

// handleUpload accepts multipart (-F html=@index.html -F keep_for=10d
// -F context='{"branch":"feat/x"}') or a raw HTML body with keep_for and
// context as query parameters.
func (s *Server) handleUpload(w http.ResponseWriter, r *http.Request) {
	name := pathName(r)
	lim := s.Store.Limits()
	r.Body = http.MaxBytesReader(w, r.Body, int64(lim.MaxHTMLBytes)+64<<10)
	in := store.PublishInput{Name: name}
	keepFor, ctxJSON := r.URL.Query().Get("keep_for"), r.URL.Query().Get("context")

	mt, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if mt == "multipart/form-data" {
		if err := r.ParseMultipartForm(int64(lim.MaxHTMLBytes)); err != nil {
			badRequest(w, "invalid multipart body: "+err.Error(), "Use -F html=@path/to/index.html")
			return
		}
		if f, _, err := r.FormFile("html"); err == nil {
			in.HTML, err = io.ReadAll(f)
			f.Close()
			if err != nil {
				s.writeErr(w, fmt.Errorf("%w: %v", store.ErrTooLarge, err))
				return
			}
		} else {
			in.HTML = []byte(r.FormValue("html"))
		}
		if v := r.FormValue("keep_for"); v != "" {
			keepFor = v
		}
		if v := r.FormValue("context"); v != "" {
			ctxJSON = v
		}
	} else {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			s.writeErr(w, fmt.Errorf("%w: %v", store.ErrTooLarge, err))
			return
		}
		in.HTML = body
	}
	if keepFor != "" {
		d, err := store.ParseDuration(keepFor)
		if err != nil {
			badRequest(w, err.Error(), "")
			return
		}
		in.KeepFor = &d
	}
	if ctxJSON != "" {
		if err := json.Unmarshal([]byte(ctxJSON), &in.Context); err != nil {
			badRequest(w, "context must be a JSON object of strings", `e.g. {"branch":"feat/x","task":"https://…"}`)
			return
		}
	}
	info, warning, err := s.Store.Publish(in)
	if err != nil {
		s.writeErr(w, err)
		return
	}
	s.Log.Info("published", "name", name, "version", info.Version, "html_bytes", len(in.HTML))
	writeJSON(w, http.StatusOK, s.NewPublishResult(info, warning))
}

func (s *Server) handleGet(w http.ResponseWriter, r *http.Request) {
	info, err := s.Store.Get(pathName(r))
	if err != nil {
		s.writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, info)
}

func (s *Server) handleList(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, s.Store.List())
}

func (s *Server) handleDelete(w http.ResponseWriter, r *http.Request) {
	if err := s.Store.Delete(pathName(r)); err != nil {
		s.writeErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleExtend(w http.ResponseWriter, r *http.Request) {
	d, err := store.ParseDuration(r.URL.Query().Get("keep_for"))
	if err != nil {
		badRequest(w, err.Error(), "")
		return
	}
	info, warning, err := s.Store.Extend(pathName(r), d)
	if err != nil {
		s.writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, s.NewPublishResult(info, warning))
}

// MessageInput is what an agent sends to the open tabs of a prototype.
type MessageInput struct {
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
}

// MessageResult says how many open tabs received a message.
type MessageResult struct {
	DeliveredTo int `json:"delivered_to"`
}

func (s *Server) handleMessage(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, int64(s.Store.Limits().MaxEventBytes)+4<<10)
	var in MessageInput
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil || in.Message == "" {
		badRequest(w, "invalid message", `{"message":"Thanks, noted!","data":{...}}`)
		return
	}
	if string(in.Data) == "null" {
		in.Data = nil
	}
	n, err := s.Store.Message(pathName(r), in.Message, in.Data)
	if err != nil {
		s.writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, MessageResult{DeliveredTo: n})
}

// ReplyResult identifies a stored reply.
type ReplyResult struct {
	ID     string `json:"id"`
	Thread string `json:"thread"`
}

func (s *Server) handleReply(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, int64(s.Store.Limits().MaxEventBytes)+4<<10)
	var in store.ReplyInput
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		badRequest(w, "invalid reply: "+err.Error(), `{"thread":"t1","text":"Done: the step is in.","state":"done"}`)
		return
	}
	ev, err := s.Store.Reply(pathName(r), in)
	if err != nil {
		s.writeErr(w, err)
		return
	}
	var d struct{ Thread string }
	json.Unmarshal(ev.Data, &d)
	writeJSON(w, http.StatusOK, ReplyResult{ID: ev.ID, Thread: d.Thread})
}

// StatusInput is what the agent says it is doing.
type StatusInput struct {
	Text string `json:"text"`
}

func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 4<<10)
	var in StatusInput
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		badRequest(w, "invalid status: "+err.Error(), `{"text":"changing the flow …"}`)
		return
	}
	n, err := s.Store.Status(pathName(r), in.Text)
	if err != nil {
		s.writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, MessageResult{DeliveredTo: n})
}

// PointInput points the open tabs at an element.
type PointInput struct {
	Target string `json:"target"`
	Text   string `json:"text,omitempty"`
}

func (s *Server) handlePoint(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 4<<10)
	var in PointInput
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		badRequest(w, "invalid point: "+err.Error(), `{"target":"flow","text":"This step is new."}`)
		return
	}
	n, err := s.Store.Point(pathName(r), in.Target, in.Text)
	if err != nil {
		s.writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, MessageResult{DeliveredTo: n})
}

func (s *Server) handleProtoRedirect(w http.ResponseWriter, r *http.Request) {
	http.Redirect(w, r, r.URL.Path+"/", http.StatusMovedPermanently)
}

// hubPos is where hub.js goes: right after the <head> tag, or if the page
// starts without one, after the leading comments, doctype and <html> tag. A
// script before the doctype would switch the page to quirks mode, and one
// before <html> would run before its data-track attribute exists. Only the
// start of the page is scanned, so a <head> in a comment or script later on
// does not count.
func hubPos(html []byte) int {
	pos, _ := scanStart(html)
	return pos
}

// scanStart returns hubPos and the <html> start tag, if the page has one
// before that position.
func scanStart(html []byte) (pos int, htmlTag []byte) {
	if bytes.HasPrefix(html, []byte("\xef\xbb\xbf")) {
		pos = 3
	}
	for {
		i := pos
		for i < len(html) && strings.IndexByte(" \t\r\n\f", html[i]) >= 0 {
			i++
		}
		rest := html[i:]
		switch {
		case bytes.HasPrefix(rest, []byte("<!--")):
			end := bytes.Index(rest[4:], []byte("-->"))
			if end < 0 {
				return pos, htmlTag
			}
			pos = i + 4 + end + 3
		case hasTagPrefix(rest, "<!doctype"), hasTagPrefix(rest, "<html"), hasTagPrefix(rest, "<head"):
			end := tagEnd(rest)
			if end < 0 {
				return pos, htmlTag
			}
			if hasTagPrefix(rest, "<html") {
				htmlTag = rest[:end]
			}
			pos = i + end
			if hasTagPrefix(rest, "<head") {
				return pos, htmlTag
			}
		default:
			return pos, htmlTag
		}
	}
}

// hasTagPrefix reports whether b starts with the tag opening tag (lower
// case, e.g. "<html"), in any case, followed by the end of its name.
func hasTagPrefix(b []byte, tag string) bool {
	if len(b) <= len(tag) || !bytes.EqualFold(b[:len(tag)], []byte(tag)) {
		return false
	}
	return strings.IndexByte(" \t\r\n\f/>", b[len(tag)]) >= 0
}

// tagEnd returns the index after the '>' that closes the tag at the start of
// b, skipping quoted attribute values, or -1.
func tagEnd(b []byte) int {
	var quote byte
	for i, c := range b {
		switch {
		case quote != 0:
			if c == quote {
				quote = 0
			}
		case c == '"' || c == '\'':
			quote = c
		case c == '>':
			return i + 1
		}
	}
	return -1
}

// hasCommentsAttr reports whether an <html> start tag carries data-comments,
// ignoring attribute values such as title=" data-comments".
func hasCommentsAttr(tag []byte) bool {
	return commentsAttrRE.Match(quotedRE.ReplaceAll(tag, []byte(`""`)))
}

var (
	commentsAttrRE = regexp.MustCompile(`(?i)\sdata-comments(\s*=|[\s/>])`)
	quotedRE       = regexp.MustCompile(`"[^"]*"|'[^']*'`)
)

// handleProto serves the HTML with hub.js injected at hubPos, and
// comments.js too if <html> carries data-comments.
func (s *Server) handleProto(w http.ResponseWriter, r *http.Request) {
	name := pathName(r)
	page, err := s.Store.Page(name)
	if err != nil {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusNotFound)
		fmt.Fprintf(w, `<!doctype html><meta charset="utf-8"><title>Not here</title><body style="font:16px system-ui;padding:40px">
<h1>%s is not here</h1><p>It expired or was deleted. Your agent can publish it again from the repository.</p>
<p><a href="/">All prototypes</a></p>`, template.HTMLEscapeString(name))
		return
	}
	tag := fmt.Sprintf(`<script src="/_hub/hub.js" data-name="%s" data-version="%d" data-token="%s"></script>`,
		template.HTMLEscapeString(name), page.Version, page.EventToken)
	pos, htmlTag := scanStart(page.HTML)
	if hasCommentsAttr(htmlTag) {
		tag += `<script src="/_hub/comments.js" defer></script>`
	}
	var out bytes.Buffer
	out.Write(page.HTML[:pos])
	out.WriteString(tag)
	out.Write(page.HTML[pos:])
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Write(out.Bytes())
}

func (s *Server) handleCommentsJS(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	w.Write(commentsJS)
}

func (s *Server) handleHubJS(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	w.Write(hubJS)
}

type emitBody struct {
	Type    string          `json:"type"`
	Data    json.RawMessage `json:"data"`
	Version int             `json:"version"`
	Auto    bool            `json:"auto"`
}

// handleEmit stores an event. Pages send their event token; requiring JSON
// and the X-Vitrine header means a browser asks for a CORS preflight before
// sending it from another origin, and vitrine never approves one.
func (s *Server) handleEmit(w http.ResponseWriter, r *http.Request) {
	name := pathName(r)
	if mt, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type")); mt != "application/json" {
		writeJSON(w, http.StatusUnsupportedMediaType, apiError{"events must be application/json", ""})
		return
	}
	pageOK := r.Header.Get(auth.Header) != "" && s.Store.CheckEventToken(name, r.Header.Get(EventTokenHeader))
	if !pageOK && !s.isAgent(r) {
		s.unauthorized(w)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, int64(s.Store.Limits().MaxEventBytes)+4<<10)
	var b emitBody
	if err := json.NewDecoder(r.Body).Decode(&b); err != nil {
		badRequest(w, "invalid event: "+err.Error(), `{"type":"choice","data":{...}}`)
		return
	}
	if string(b.Data) == "null" {
		b.Data = nil
	}
	ev, err := s.Store.Emit(name, b.Type, b.Data, b.Version, b.Auto)
	if err != nil {
		s.writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, ev)
}

func (s *Server) handleListEvents(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	since, _ := strconv.ParseInt(q.Get("since"), 10, 64)
	evs, err := s.Store.Events(pathName(r), splitTypes(q.Get("types")), since)
	if err != nil {
		s.writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, evs)
}

func splitTypes(s string) []string {
	var out []string
	for _, t := range strings.Split(s, ",") {
		if t = strings.TrimSpace(t); t != "" {
			out = append(out, t)
		}
	}
	return out
}

// handleNext waits for the next unacknowledged event and then ends. It is made
// for background commands that wake the agent when they exit:
//
//	curl -s -H @<auth file> 'http://127.0.0.1:8720/api/events/next?name=a/b&types=choice&ack=7'
//
// While waiting it writes a space every 25 s so the idle connection is not
// dropped; leading whitespace is valid JSON.
func (s *Server) handleNext(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	name := q.Get("name")
	if _, err := s.Store.Get(name); err != nil {
		s.writeErr(w, err)
		return
	}
	wait := DefaultNextWait
	if v := q.Get("timeout"); v != "" {
		secs, err := strconv.Atoi(v)
		if err != nil || secs < 0 {
			badRequest(w, "timeout must be seconds", "")
			return
		}
		wait = min(time.Duration(secs)*time.Second, MaxNextWait)
	}
	ack, _ := strconv.ParseInt(q.Get("ack"), 10, 64)

	type result struct {
		res store.FetchResult
		err error
	}
	done := make(chan result, 1)
	go func() {
		res, err := s.Store.Fetch(r.Context(), name, splitTypes(q.Get("types")), ack, wait)
		done <- result{res, err}
	}()

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	flusher, _ := w.(http.Flusher)
	tick := time.NewTicker(keepAlive)
	defer tick.Stop()
	for {
		select {
		case <-tick.C:
			w.Write([]byte(" "))
			if flusher != nil {
				flusher.Flush()
			}
		case out := <-done:
			enc := json.NewEncoder(w)
			enc.SetIndent("", "  ")
			if out.err != nil {
				if !errors.Is(out.err, store.ErrUnknown) {
					return // the client went away
				}
				enc.Encode(apiError{"unknown_prototype", "Removed while waiting: it expired or was deleted. Publish it again from the repository."})
				return
			}
			enc.Encode(out.res)
			return
		}
	}
}

// handleStream is a server-sent event stream. hub.js (hub=1, with the event
// token in the query because EventSource cannot set headers) gets messages,
// new versions, removal and the comment threads of its own prototype. Agents,
// e.g. a Claude Code Monitor, get the events, optionally filtered by types,
// and count as listening while connected.
func (s *Server) handleStream(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	name := q.Get("name")
	isHub := q.Get("hub") == "1"
	if isHub && !s.Store.CheckEventToken(name, q.Get("token")) || !isHub && !s.isAgent(r) {
		s.unauthorized(w)
		return
	}
	ch, cancel, err := s.Store.Subscribe(name, isHub)
	if err != nil {
		s.writeErr(w, err)
		return
	}
	defer cancel()
	if !isHub {
		if done := s.Store.Listen(name); done != nil {
			defer done(false)
		}
	}
	types := splitTypes(q.Get("types"))
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(": vitrine\n\n"))
	if isHub {
		// hub.js disconnects while its tab is hidden; on reconnect it learns
		// whether a new version was published meanwhile.
		// The threads and the agent's presence come along, so a reloaded or
		// re-shown tab is up to date (docs/adr/0005-comments-on-elements.md).
		if info, err := s.Store.Get(name); err == nil {
			fmt.Fprintf(w, "event: version\ndata: {\"version\":%d}\n\n", info.Version)
			agent, _ := json.Marshal(info.Agent)
			fmt.Fprintf(w, "event: agent\ndata: %s\n\n", agent)
		}
		if evs, err := s.Store.Threads(name); err == nil {
			data, _ := json.Marshal(evs)
			fmt.Fprintf(w, "event: threads\ndata: %s\n\n", data)
		}
	}
	flusher.Flush()
	tick := time.NewTicker(keepAlive)
	defer tick.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-tick.C:
			w.Write([]byte(": keep-alive\n\n"))
			flusher.Flush()
		case p, open := <-ch:
			if !open {
				return
			}
			if p.Kind == "event" {
				if isHub {
					continue
				}
				if ev, ok := p.Data.(store.Event); ok && len(types) > 0 && !contains(types, ev.Type) {
					continue
				}
			} else if !isHub && p.Kind != "gone" {
				continue
			}
			data, _ := json.Marshal(p.Data)
			fmt.Fprintf(w, "event: %s\ndata: %s\n\n", p.Kind, data)
			flusher.Flush()
			if p.Kind == "gone" {
				return
			}
		}
	}
}

func contains(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}

func (s *Server) handleOverview(w http.ResponseWriter, _ *http.Request) {
	lim := s.Store.Limits()
	data := map[string]any{
		"Protos":     s.Store.List(),
		"PublicURL":  s.PublicURL,
		"DefaultTTL": store.FormatDuration(lim.DefaultTTL),
		"MaxKeepFor": store.FormatDuration(lim.MaxKeepFor),
		"Version":    s.Version,
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	if err := s.overview.Execute(w, data); err != nil {
		s.Log.Error("overview", "err", err)
	}
}
