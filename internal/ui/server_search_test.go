package ui

import (
	"context"
	"fmt"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/allisonhere/tidemail/internal/config"
	"github.com/allisonhere/tidemail/internal/db"
	imapClient "github.com/allisonhere/tidemail/internal/imap"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/emersion/go-imap/v2/imapserver"
	"github.com/emersion/go-imap/v2/imapserver/imapmemserver"
)

// serverSearchFixture is a model whose account has one message cached locally
// and several more only on an in-memory IMAP server.
type serverSearchFixture struct {
	model    Model
	database *db.DB
	inbox    int64
}

func newServerSearchFixture(t *testing.T) *serverSearchFixture {
	t.Helper()
	backend := imapmemserver.New()
	user := imapmemserver.NewUser("test", "password")
	backend.AddUser(user)
	if err := user.Create("INBOX", nil); err != nil {
		t.Fatal(err)
	}
	server := imapserver.New(&imapserver.Options{InsecureAuth: true, NewSession: func(*imapserver.Conn) (imapserver.Session, *imapserver.GreetingData, error) {
		return backend.NewSession(), nil, nil
	}})
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(func() { server.Close(); listener.Close() })

	acfg := config.AccountConfig{ID: "srv", Name: "Work", IMAPHost: "127.0.0.1", IMAPPort: listener.Addr().(*net.TCPAddr).Port, User: "test", Password: "password"}
	client := imapClient.New(acfg)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := client.Connect(ctx); err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	raws := []string{
		"From: Ann <ann@example.com>\r\nSubject: Old invoice\r\nDate: Mon, 02 Mar 2026 09:00:00 +0000\r\nMessage-ID: <1@t>\r\n\r\nnothing\r\n",
		"From: Ann <ann@example.com>\r\nSubject: Older invoice\r\nDate: Tue, 03 Mar 2026 09:00:00 +0000\r\nMessage-ID: <2@t>\r\n\r\nnothing\r\n",
		"From: Bob <bob@example.com>\r\nSubject: Lunch\r\nDate: Wed, 04 Mar 2026 09:00:00 +0000\r\nMessage-ID: <3@t>\r\n\r\nnoon\r\n",
	}
	for _, raw := range raws {
		if err := client.AppendWithFlags(ctx, "INBOX", []byte(raw), time.Now()); err != nil {
			t.Fatal(err)
		}
	}

	database, err := db.OpenPath(t.TempDir() + "/mail.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { database.Close() })
	accountID, err := database.AddAccount("srv", "Work", "")
	if err != nil {
		t.Fatal(err)
	}
	inbox, err := database.UpsertMailbox(db.Mailbox{AccountID: accountID, Name: "INBOX"})
	if err != nil {
		t.Fatal(err)
	}
	// UID 1 is already cached; 2 and 3 exist only on the server.
	if err := database.UpsertMessage(db.Message{MailboxID: inbox, UID: 1, Subject: "Old invoice", From: "Ann <ann@example.com>", Date: time.Date(2026, 3, 2, 9, 0, 0, 0, time.UTC)}); err != nil {
		t.Fatal(err)
	}
	cfg := config.DefaultConfig()
	cfg.Accounts = []config.AccountConfig{acfg}
	m := NewModel(database, cfg, "test", false)
	t.Cleanup(m.sessions.Close)
	m.accounts = []db.Account{{ID: accountID, Name: "Work"}}
	m.mailboxes = []db.Mailbox{{ID: inbox, AccountID: accountID, Name: "INBOX"}}
	m.searchMode = true
	return &serverSearchFixture{model: m, database: database, inbox: inbox}
}

func (f *serverSearchFixture) search(t *testing.T, query string) (Model, ServerSearchDoneMsg) {
	t.Helper()
	m := f.model
	m.searchQuery = query
	next, cmd := m.startServerSearch()
	m = next.(Model)
	if cmd == nil {
		t.Fatalf("no search started: %q", m.statusMsg)
	}
	done, ok := cmd().(ServerSearchDoneMsg)
	if !ok {
		t.Fatal("search did not finish with ServerSearchDoneMsg")
	}
	return m, done
}

func TestServerSearchDownloadsOnlyWhatIsMissingThenSearchesLocally(t *testing.T) {
	f := newServerSearchFixture(t)
	m, done := f.search(t, "from:ann")
	if done.Err != nil || len(done.Errs) != 0 {
		t.Fatalf("search failed: %v %v", done.Err, done.Errs)
	}
	if done.Found != 2 || done.Fetched != 1 || done.Folders != 1 {
		t.Fatalf("found=%d fetched=%d folders=%d, want 2 matches, 1 downloaded, 1 folder", done.Found, done.Fetched, done.Folders)
	}
	// Lunch does not match and must not have been downloaded.
	var subjects []string
	msgs, err := f.database.ListMessages(f.inbox)
	if err != nil {
		t.Fatal(err)
	}
	for _, msg := range msgs {
		subjects = append(subjects, msg.Subject)
	}
	if got := strings.Join(subjects, ","); !strings.Contains(got, "Older invoice") || strings.Contains(got, "Lunch") || len(subjects) != 2 {
		t.Fatalf("cache holds %q, want Old invoice and Older invoice only", got)
	}
	// The done message re-runs the local search, which now finds both.
	next, cmd := m.handleServerSearchDone(done)
	m = next.(Model)
	if cmd == nil || !strings.Contains(m.statusMsg, "2 matches") || !strings.Contains(m.statusMsg, "1 new downloaded") {
		t.Fatalf("status %q", m.statusMsg)
	}
	local, err := f.database.SearchAllMessages("from:ann", false)
	if err != nil || len(local) != 2 {
		t.Fatalf("local search after download = %d, %v", len(local), err)
	}
}

func TestServerSearchTwiceDownloadsNothingNew(t *testing.T) {
	f := newServerSearchFixture(t)
	if _, done := f.search(t, "from:ann"); done.Fetched != 1 {
		t.Fatalf("first search fetched %d", done.Fetched)
	}
	if _, done := f.search(t, "from:ann"); done.Fetched != 0 || done.Found != 2 {
		t.Fatalf("second search found=%d fetched=%d, want 2 and 0", done.Found, done.Fetched)
	}
}

func TestServerSearchStopsAtThePerFolderCap(t *testing.T) {
	f := newServerSearchFixture(t)
	acfg := f.model.cfg.Accounts[0]
	client := imapClient.New(acfg)
	ctx := context.Background()
	if err := client.Connect(ctx); err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	for i := 0; i < serverSearchPerFolder+5; i++ {
		raw := fmt.Sprintf("From: Zed <zed@example.com>\r\nSubject: bulk %d\r\nMessage-ID: <b%d@t>\r\n\r\nx\r\n", i, i)
		if err := client.AppendWithFlags(ctx, "INBOX", []byte(raw), time.Now()); err != nil {
			t.Fatal(err)
		}
	}
	_, done := f.search(t, "from:zed")
	if done.Found != serverSearchPerFolder+5 || done.Fetched != serverSearchPerFolder || done.Capped != 5 {
		t.Fatalf("found=%d fetched=%d capped=%d", done.Found, done.Fetched, done.Capped)
	}
	m := f.model
	m.searchQuery = "from:zed"
	next, _ := m.handleServerSearchDone(done)
	if got := next.(Model).statusMsg; !strings.Contains(got, "5 more not downloaded") {
		t.Fatalf("status %q does not say the cap was hit", got)
	}
}

func TestServerSearchRefusesWhatOnlyTheCacheCanAnswer(t *testing.T) {
	f := newServerSearchFixture(t)
	for _, query := range []string{"has:attachment", "#github"} {
		m := f.model
		m.searchQuery = query
		next, cmd := m.startServerSearch()
		m = next.(Model)
		if cmd == nil {
			// clearStatusCmd is fine; a search command is not.
			t.Fatalf("%q: expected the status-clearing command", query)
		}
		if !m.statusErr || !strings.Contains(m.statusMsg, "cannot search that on its own") {
			t.Errorf("%q: status %q", query, m.statusMsg)
		}
	}
	// Attachments combined with something the server can search are fine, and
	// the status says the attachment test is done locally.
	_, done := f.search(t, "from:ann has:attachment")
	if done.Err != nil || done.Found != 2 {
		t.Fatalf("combined search: found=%d err=%v", done.Found, done.Err)
	}
	m := f.model
	m.searchQuery = "from:ann has:attachment"
	next, _ := m.handleServerSearchDone(done)
	if got := next.(Model).statusMsg; !strings.Contains(got, "has:attachment checked locally") {
		t.Errorf("status %q", got)
	}
}

func TestServerSearchNeedsASearchFirst(t *testing.T) {
	f := newServerSearchFixture(t)
	m := f.model
	m.searchMode, m.searchQuery = false, "from:ann"
	next, _ := m.startServerSearch()
	if got := next.(Model).statusMsg; !strings.Contains(got, "type a search first") {
		t.Errorf("outside search mode: %q", got)
	}
	m = f.model
	m.searchQuery = "  "
	next, _ = m.startServerSearch()
	if got := next.(Model).statusMsg; !strings.Contains(got, "type a search first") {
		t.Errorf("empty search: %q", got)
	}
}

func TestServerSearchIgnoresAnAnswerToAnOlderQuery(t *testing.T) {
	f := newServerSearchFixture(t)
	_, done := f.search(t, "from:ann")
	m := f.model
	m.searchQuery = "something else now"
	next, cmd := m.handleServerSearchDone(done)
	if cmd != nil || next.(Model).statusMsg != "" {
		t.Errorf("a stale answer changed the screen: cmd=%v status=%q", cmd != nil, next.(Model).statusMsg)
	}
	m.searchMode, m.searchQuery = false, "from:ann"
	if _, cmd := m.handleServerSearchDone(done); cmd != nil {
		t.Error("an answer after leaving search must be ignored")
	}
}

func TestServerSearchKeyWorksWhileTypingAndAfterEnter(t *testing.T) {
	f := newServerSearchFixture(t)
	for _, editing := range []bool{true, false} {
		m := f.model
		m.searchQuery, m.searchEditing = "from:ann", editing
		next, cmd := m.handleMainKey(tea.KeyMsg{Type: tea.KeyCtrlG})
		if cmd == nil || !strings.Contains(next.(Model).statusMsg, "searching the server") {
			t.Errorf("editing=%v: ctrl+g did nothing (status %q)", editing, next.(Model).statusMsg)
		}
	}
}

func TestSearchableMailboxesSkipsFoldersThatHoldNoServerMail(t *testing.T) {
	boxes := []db.Mailbox{
		{Name: "INBOX"}, {Name: "Drafts", Flags: []string{`\Drafts`}},
		{Name: "[Gmail]", Flags: []string{`\Noselect`, `\HasChildren`}}, {Name: "Sent", Flags: []string{`\Sent`}},
	}
	var got []string
	for _, mb := range searchableMailboxes(boxes) {
		got = append(got, mb.Name)
	}
	if strings.Join(got, ",") != "INBOX,Sent" {
		t.Errorf("searchable = %v, want INBOX and Sent", got)
	}
}

func TestServerSearchSummaryNamesFailuresAndCaps(t *testing.T) {
	text, isErr := serverSearchSummary(ServerSearchDoneMsg{Found: 1, Folders: 3, Errs: []string{"Work/Archive: boom"}}, "")
	if !isErr || !strings.Contains(text, "1 match in 3 folders") || !strings.Contains(text, "1 folder failed: Work/Archive: boom") {
		t.Errorf("summary %q err=%v", text, isErr)
	}
	if text, isErr := serverSearchSummary(ServerSearchDoneMsg{Err: fmt.Errorf("no route")}, ""); !isErr || !strings.Contains(text, "no route") {
		t.Errorf("hard failure %q", text)
	}
}
