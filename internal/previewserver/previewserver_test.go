package previewserver

import (
	"bufio"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"
)

func start(t *testing.T, ttl time.Duration) *Server {
	t.Helper()
	s, err := Start(ttl)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	return s
}

func get(t *testing.T, url, host string) (*http.Response, string) {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	if host != "" {
		req.Host = host
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp, string(b)
}

func TestListensOnLoopbackOnly(t *testing.T) {
	s := start(t, time.Minute)
	host, _, err := net.SplitHostPort(s.addr)
	if err != nil || host != "127.0.0.1" {
		t.Fatalf("addr = %q, want 127.0.0.1:port", s.addr)
	}
	if !strings.HasPrefix(s.URL(), "http://127.0.0.1:") || !strings.HasSuffix(s.URL(), "/") {
		t.Fatalf("URL = %q", s.URL())
	}
}

func TestTokenIsRequiredAndRandom(t *testing.T) {
	a, b := start(t, time.Minute), start(t, time.Minute)
	if a.token == b.token || len(a.token) < 20 {
		t.Fatalf("tokens must be long and distinct: %q %q", a.token, b.token)
	}
	base := "http://" + a.addr
	for _, path := range []string{"/", "/doc", "/events", "/wrong/", "/wrong/doc", "/" + a.token, "/" + a.token + "x/", "/" + b.token + "/"} {
		if resp, _ := get(t, base+path, ""); resp.StatusCode != http.StatusNotFound {
			t.Errorf("GET %s = %d, want 404", path, resp.StatusCode)
		}
	}
	if resp, _ := get(t, a.URL(), ""); resp.StatusCode != http.StatusOK {
		t.Fatalf("the real URL = %d", resp.StatusCode)
	}
	if resp, _ := get(t, a.URL()+"nothere", ""); resp.StatusCode != http.StatusNotFound {
		t.Fatalf("unknown path under the token = %d", resp.StatusCode)
	}
}

func TestHostHeaderMustBeTheLoopbackAddress(t *testing.T) {
	s := start(t, time.Minute)
	for _, host := range []string{"evil.example", "evil.example:" + strings.Split(s.addr, ":")[1], "localhost:" + strings.Split(s.addr, ":")[1], "127.0.0.1", "127.0.0.1:1"} {
		resp, body := get(t, s.URL(), host)
		if resp.StatusCode != http.StatusForbidden || strings.Contains(body, "tidemail-preview") {
			t.Errorf("Host %q = %d, want 403 with no content", host, resp.StatusCode)
		}
	}
	if resp, _ := get(t, s.URL(), s.addr); resp.StatusCode != http.StatusOK {
		t.Fatalf("the exact Host must work, got %d", resp.StatusCode)
	}
}

func TestOnlyGetAndHeadAreServed(t *testing.T) {
	s := start(t, time.Minute)
	for _, m := range []string{http.MethodPost, http.MethodPut, http.MethodDelete, http.MethodOptions} {
		req, _ := http.NewRequest(m, s.URL(), strings.NewReader("x"))
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusMethodNotAllowed {
			t.Errorf("%s = %d, want 405", m, resp.StatusCode)
		}
		if resp.Header.Get("Access-Control-Allow-Origin") != "" {
			t.Errorf("%s: no CORS headers may be sent", m)
		}
	}
}

func TestPageHasContentHeadersAndTheStrictPolicy(t *testing.T) {
	s := start(t, time.Minute)
	s.Set("Q3 <results> & more", "<p>Hi <b>there</b></p>\n<table></table>\n")
	resp, body := get(t, s.URL(), "")
	if !strings.Contains(body, "<p>Hi <b>there</b></p>\n<table></table>\n") {
		t.Fatalf("the first page must carry the HTML part verbatim:\n%s", body)
	}
	if !strings.Contains(body, "<title>Q3 &lt;results&gt; &amp; more</title>") {
		t.Fatalf("title not escaped:\n%s", body)
	}
	sum := sha256.Sum256([]byte(pageScript))
	policy := resp.Header.Get("Content-Security-Policy")
	for _, want := range []string{"default-src 'none'", "connect-src 'self'", "script-src 'sha256-" + base64.StdEncoding.EncodeToString(sum[:]) + "'", "frame-ancestors 'none'"} {
		if !strings.Contains(policy, want) {
			t.Errorf("policy lacks %q: %s", want, policy)
		}
	}
	if strings.Contains(policy, "'unsafe-eval'") || strings.Contains(policy, "script-src 'unsafe-inline'") {
		t.Errorf("only the hashed script may run: %s", policy)
	}
	for k, want := range map[string]string{"Cache-Control": "no-store", "Referrer-Policy": "no-referrer", "X-Content-Type-Options": "nosniff"} {
		if got := resp.Header.Get(k); got != want {
			t.Errorf("%s = %q, want %q", k, got, want)
		}
	}
}

func TestDocReturnsTheLatestContent(t *testing.T) {
	s := start(t, time.Minute)
	s.Set("one", "<p>1</p>")
	s.Set("two", "<p>2</p>")
	_, body := get(t, s.URL()+"doc", "")
	var d struct{ Title, HTML string }
	if err := json.Unmarshal([]byte(body), &d); err != nil || d.Title != "two" || d.HTML != "<p>2</p>" {
		t.Fatalf("doc = %q (%v)", body, err)
	}
}

func TestEventStreamDeliversAnUpdateAndStopsOnClose(t *testing.T) {
	s := start(t, time.Minute)
	s.Set("t", "<p>1</p>")
	resp, err := http.Get(s.URL() + "events")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if ct := resp.Header.Get("Content-Type"); ct != "text/event-stream" {
		t.Fatalf("Content-Type = %q", ct)
	}
	lines := make(chan string, 8)
	go func() {
		sc := bufio.NewScanner(resp.Body)
		for sc.Scan() {
			lines <- sc.Text()
		}
		close(lines)
	}()
	wait := func(want string) {
		t.Helper()
		deadline := time.After(2 * time.Second)
		for {
			select {
			case l, ok := <-lines:
				if !ok {
					t.Fatalf("stream ended before %q", want)
				}
				if strings.Contains(l, want) {
					return
				}
			case <-deadline:
				t.Fatalf("no %q within 2s", want)
			}
		}
	}
	wait("connected")
	s.Set("t", "<p>2</p>")
	wait("data: update")
	s.Close()
	deadline := time.After(2 * time.Second)
	for {
		select {
		case _, ok := <-lines:
			if !ok {
				return // the stream ended with the server
			}
		case <-deadline:
			t.Fatal("the event stream must end when the server closes")
		}
	}
}

func TestSettingTheSameContentSendsNoEvent(t *testing.T) {
	s := start(t, time.Minute)
	s.Set("t", "<p>1</p>")
	ch := make(chan struct{}, 1)
	s.mu.Lock()
	s.clients[ch] = struct{}{}
	s.mu.Unlock()
	s.Set("t", "<p>1</p>")
	select {
	case <-ch:
		t.Fatal("an unchanged document must not notify pages")
	default:
	}
	s.Set("t", "<p>2</p>")
	select {
	case <-ch:
	default:
		t.Fatal("a changed document must notify pages")
	}
}

func TestIdleServerShutsItselfDownAndATouchPostponesIt(t *testing.T) {
	s := start(t, 300*time.Millisecond)
	url := s.URL()
	for i := 0; i < 4; i++ { // 600 ms of touches: past the ttl, still alive
		time.Sleep(150 * time.Millisecond)
		s.Touch()
		if !s.Alive() {
			t.Fatal("a touched server must stay up")
		}
	}
	deadline := time.Now().Add(3 * time.Second)
	for s.Alive() && time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
	}
	if s.Alive() {
		t.Fatal("an untouched server must shut itself down")
	}
	if resp, err := http.Get(url); err == nil {
		resp.Body.Close()
		t.Fatal("the port must be closed after shutdown")
	}
}

func TestCloseIsIdempotent(t *testing.T) {
	s := start(t, time.Minute)
	s.Close()
	s.Close()
	if s.Alive() {
		t.Fatal("closed server reports alive")
	}
}
