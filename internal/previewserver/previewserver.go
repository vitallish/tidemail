// Package previewserver serves one draft preview to a browser on the loopback
// interface and pushes a change event when the draft changes, so an open page
// updates in place without reloading.
//
// It exists only while a composer is open. Access control, in order: it
// listens on 127.0.0.1 only; every URL carries a random token and anything
// else is a 404; the Host header must be the exact loopback address and port
// (this stops DNS rebinding from a web page); only GET and HEAD are served; no
// CORS headers are sent. The content is held in memory.
package previewserver

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"html"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

// pageScript opens the event stream and, on every event, fetches the current
// document and swaps it in. Relative URLs resolve under the token path, so the
// page URL must end in a slash (Server.URL does). If the stream breaks the page
// stops updating rather than retrying forever.
const pageScript = `(function(){var box=document.getElementById('tidemail-preview');function load(){fetch('doc',{cache:'no-store'}).then(function(r){return r.json()}).then(function(d){box.innerHTML=d.html;document.title=d.title}).catch(function(){})}var es=new EventSource('events');es.onmessage=load;es.onerror=function(){es.close()}})();`

// keepAlive is how often an idle event stream is pinged, so a closed browser
// is noticed and the connection is not dropped by an intermediary.
const keepAlive = 15 * time.Second

// Server is a running preview server. Create it with Start.
type Server struct {
	addr  string // "127.0.0.1:port", the only acceptable Host
	token string
	ttl   time.Duration
	srv   *http.Server

	mu        sync.Mutex
	title     string
	html      string
	version   int
	clients   map[chan struct{}]struct{}
	lastTouch time.Time
	closed    bool
	done      chan struct{}
}

// Start listens on a random loopback port. The server shuts itself down when it
// has not been touched (Touch or Set) for ttl, which is how it ends after the
// composer that owns it closes.
func Start(ttl time.Duration) (*Server, error) {
	ln, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("start preview server: %w", err)
	}
	raw := make([]byte, 16)
	if _, err := rand.Read(raw); err != nil {
		_ = ln.Close()
		return nil, fmt.Errorf("start preview server: %w", err)
	}
	s := &Server{
		addr:      ln.Addr().String(),
		token:     base64.RawURLEncoding.EncodeToString(raw),
		ttl:       ttl,
		clients:   map[chan struct{}]struct{}{},
		lastTouch: time.Now(),
		done:      make(chan struct{}),
	}
	s.srv = &http.Server{Handler: s, ReadHeaderTimeout: 5 * time.Second}
	go func() { _ = s.srv.Serve(ln) }()
	go s.watch()
	return s, nil
}

// URL is the address to open in the browser. It ends in a slash.
func (s *Server) URL() string { return "http://" + s.addr + "/" + s.token + "/" }

// Alive reports whether the server is still accepting requests.
func (s *Server) Alive() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return !s.closed
}

// Touch postpones the idle shutdown.
func (s *Server) Touch() {
	s.mu.Lock()
	s.lastTouch = time.Now()
	s.mu.Unlock()
}

// Set replaces the previewed document and tells connected pages. An unchanged
// document only counts as a Touch.
func (s *Server) Set(title, htmlPart string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lastTouch = time.Now()
	if s.version > 0 && title == s.title && htmlPart == s.html {
		return
	}
	s.title, s.html = title, htmlPart
	s.version++
	for ch := range s.clients {
		select {
		case ch <- struct{}{}:
		default: // one pending event is enough; the page fetches the latest
		}
	}
}

// Close stops the server and ends every event stream. It is safe to call twice.
func (s *Server) Close() {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	s.closed = true
	close(s.done)
	s.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := s.srv.Shutdown(ctx); err != nil {
		_ = s.srv.Close()
	}
}

// watch closes the server once it has gone untouched for the ttl.
func (s *Server) watch() {
	tick := max(s.ttl/4, 10*time.Millisecond)
	t := time.NewTicker(tick)
	defer t.Stop()
	for {
		select {
		case <-s.done:
			return
		case <-t.C:
			s.mu.Lock()
			idle := time.Since(s.lastTouch) > s.ttl
			s.mu.Unlock()
			if idle {
				s.Close()
				return
			}
		}
	}
}

// csp is the page policy: nothing loads except data: images, inline styles,
// this origin's event stream and document, and the one page script by hash.
func csp() string {
	sum := sha256.Sum256([]byte(pageScript))
	return "default-src 'none'; style-src 'unsafe-inline'; img-src data:; connect-src 'self'; " +
		"script-src 'sha256-" + base64.StdEncoding.EncodeToString(sum[:]) + "'; " +
		"base-uri 'none'; form-action 'none'; frame-ancestors 'none'"
}

// ServeHTTP implements http.Handler.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Host != s.addr {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	// The token must be followed by a slash: the page's relative URLs depend on it.
	seg, rest, found := strings.Cut(strings.TrimPrefix(r.URL.Path, "/"), "/")
	if !found || subtle.ConstantTimeCompare([]byte(seg), []byte(s.token)) != 1 {
		http.NotFound(w, r)
		return
	}
	h := w.Header()
	h.Set("Cache-Control", "no-store")
	h.Set("Referrer-Policy", "no-referrer")
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Cross-Origin-Resource-Policy", "same-origin")
	switch rest {
	case "":
		s.servePage(w)
	case "doc":
		s.serveDoc(w)
	case "events":
		s.serveEvents(w, r)
	default:
		http.NotFound(w, r)
	}
}

func (s *Server) snapshot() (title, htmlPart string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.title, s.html
}

// servePage serves the shell with the current HTML already in place, so the
// first paint needs no round trip.
func (s *Server) servePage(w http.ResponseWriter) {
	title, htmlPart := s.snapshot()
	h := w.Header()
	h.Set("Content-Type", "text/html; charset=utf-8")
	h.Set("Content-Security-Policy", csp())
	_, _ = fmt.Fprintf(w, "<!doctype html>\n<html><head><meta charset=\"utf-8\"><title>%s</title></head>\n<body>\n<div id=\"tidemail-preview\">%s</div>\n<script>%s</script>\n</body></html>\n",
		html.EscapeString(title), htmlPart, pageScript)
}

func (s *Server) serveDoc(w http.ResponseWriter) {
	title, htmlPart := s.snapshot()
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(struct {
		Title string `json:"title"`
		HTML  string `json:"html"`
	}{title, htmlPart})
}

func (s *Server) serveEvents(w http.ResponseWriter, r *http.Request) {
	fl, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	ch := make(chan struct{}, 1)
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		http.Error(w, "gone", http.StatusGone)
		return
	}
	s.clients[ch] = struct{}{}
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		delete(s.clients, ch)
		s.mu.Unlock()
	}()
	w.Header().Set("Content-Type", "text/event-stream")
	if _, err := fmt.Fprint(w, ": connected\n\n"); err != nil {
		return
	}
	fl.Flush()
	ping := time.NewTicker(keepAlive)
	defer ping.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-s.done:
			return
		case <-ch:
			if _, err := fmt.Fprint(w, "data: update\n\n"); err != nil {
				return // the browser went away
			}
			fl.Flush()
		case <-ping.C:
			if _, err := fmt.Fprint(w, ": keepalive\n\n"); err != nil {
				return
			}
			fl.Flush()
		}
	}
}
