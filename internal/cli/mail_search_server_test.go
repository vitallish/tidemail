package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/allisonhere/tidemail/internal/config"
	"github.com/allisonhere/tidemail/internal/db"
	imapClient "github.com/allisonhere/tidemail/internal/imap"
	"github.com/emersion/go-imap/v2/imapserver"
	"github.com/emersion/go-imap/v2/imapserver/imapmemserver"
)

// startCLIServer runs an in-memory IMAP server holding raws in INBOX and writes
// a config and empty cache with one account ("Work", id "acct-1") that reaches it.
// It returns the account's database ID and INBOX mailbox ID.
func startCLIServer(t *testing.T, raws []string) (accountID, inboxID int64) {
	t.Helper()
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_DATA_HOME", t.TempDir())

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

	acfg := config.AccountConfig{ID: "acct-1", Name: "Work", IMAPHost: "127.0.0.1", IMAPPort: listener.Addr().(*net.TCPAddr).Port, User: "test", Password: "password"}
	client := imapClient.New(acfg)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := client.Connect(ctx); err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	for _, raw := range raws {
		if err := client.AppendWithFlags(ctx, "INBOX", []byte(raw), time.Now()); err != nil {
			t.Fatal(err)
		}
	}

	cfg := config.DefaultConfig()
	cfg.Accounts = []config.AccountConfig{acfg}
	if err := config.Save(cfg); err != nil {
		t.Fatal(err)
	}
	database, err := db.Open()
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	accountID, err = database.AddAccount("acct-1", "Work", "")
	if err != nil {
		t.Fatal(err)
	}
	inboxID, err = database.UpsertMailbox(db.Mailbox{AccountID: accountID, Name: "INBOX"})
	if err != nil {
		t.Fatal(err)
	}
	return accountID, inboxID
}

// serverSearchCLIFixture: three messages, none in the local cache.
func serverSearchCLIFixture(t *testing.T) {
	t.Helper()
	startCLIServer(t, []string{
		"From: Ann <ann@example.com>\r\nSubject: Invoice one\r\nDate: Mon, 02 Mar 2026 09:00:00 +0000\r\nMessage-ID: <1@t>\r\n\r\nx\r\n",
		"From: Ann <ann@example.com>\r\nSubject: Invoice two\r\nDate: Tue, 03 Mar 2026 09:00:00 +0000\r\nMessage-ID: <2@t>\r\n\r\nx\r\n",
		"From: Bob <bob@example.com>\r\nSubject: Lunch\r\nDate: Wed, 04 Mar 2026 09:00:00 +0000\r\nMessage-ID: <3@t>\r\n\r\nx\r\n",
	})
}

type searchEnvelope struct {
	OK   bool `json:"ok"`
	Data struct {
		Total int `json:"total"`
		Items []struct {
			Subject string `json:"subject"`
		} `json:"items"`
		Server *struct {
			Folders       int      `json:"folders"`
			Found         int      `json:"found"`
			Fetched       int      `json:"fetched"`
			Errors        []string `json:"errors"`
			CheckedLocal  bool     `json:"checked_locally"`
			NotDownloaded int      `json:"not_downloaded"`
		} `json:"server"`
	} `json:"data"`
}

func runSearch(t *testing.T, args ...string) (int, searchEnvelope, string) {
	t.Helper()
	var out, diag bytes.Buffer
	code := RunMailRead("messages", append([]string{"search"}, args...), &out, &diag)
	var env searchEnvelope
	if strings.HasPrefix(out.String(), "{") {
		if err := json.Unmarshal(out.Bytes(), &env); err != nil {
			t.Fatalf("bad JSON %q: %v", out.String(), err)
		}
	}
	return code, env, out.String() + diag.String()
}

func TestCachedSearchDoesNotTouchTheServer(t *testing.T) {
	serverSearchCLIFixture(t)
	code, env, raw := runSearch(t, "from:ann", "--json")
	if code != 0 || env.Data.Total != 0 || env.Data.Server != nil {
		t.Fatalf("code=%d total=%d server=%v %s", code, env.Data.Total, env.Data.Server, raw)
	}
}

func TestSearchServerDownloadsMatchesThenSearchesTheCache(t *testing.T) {
	serverSearchCLIFixture(t)
	code, env, raw := runSearch(t, "from:ann", "--server", "--json")
	if code != 0 || !env.OK {
		t.Fatalf("code=%d %s", code, raw)
	}
	if env.Data.Total != 2 || len(env.Data.Items) != 2 {
		t.Fatalf("results %d, want the two invoices: %s", env.Data.Total, raw)
	}
	s := env.Data.Server
	if s == nil || s.Found != 2 || s.Fetched != 2 || s.Folders != 1 || len(s.Errors) != 0 {
		t.Fatalf("server report %+v", s)
	}
	// Run again: nothing new to download, same answer.
	code, env, raw = runSearch(t, "from:ann", "--server", "--json")
	if code != 0 || env.Data.Server.Fetched != 0 || env.Data.Total != 2 {
		t.Fatalf("second run: code=%d %s", code, raw)
	}
	// Lunch was never downloaded: a cached search for it stays empty.
	if _, env, _ = runSearch(t, "lunch", "--json"); env.Data.Total != 0 {
		t.Fatal("a message that did not match was downloaded")
	}
}

func TestSearchServerTextModeReportsOnStderr(t *testing.T) {
	serverSearchCLIFixture(t)
	var out, diag bytes.Buffer
	if code := RunMailRead("messages", []string{"search", "from:ann", "--server"}, &out, &diag); code != 0 {
		t.Fatalf("code %d: %s", code, diag.String())
	}
	if !strings.Contains(diag.String(), "server search: 2 matches in 1 folder, 2 new downloaded") || !strings.Contains(out.String(), "Invoice one") {
		t.Fatalf("stdout %q stderr %q", out.String(), diag.String())
	}
}

func TestSearchServerRefusesWhatOnlyTheCacheCanAnswer(t *testing.T) {
	serverSearchCLIFixture(t)
	code, _, raw := runSearch(t, "has:attachment", "--server", "--json")
	if code != 2 || !strings.Contains(raw, "cannot search that on its own") {
		t.Fatalf("code=%d %s", code, raw)
	}
	code, env, raw := runSearch(t, "from:ann has:attachment", "--server", "--json")
	if code != 0 || env.Data.Server == nil || !env.Data.Server.CheckedLocal {
		t.Fatalf("combined: code=%d %s", code, raw)
	}
}

func TestSearchServerAccountSelection(t *testing.T) {
	serverSearchCLIFixture(t)
	if code, _, raw := runSearch(t, "from:ann", "--server", "--account", "Nope", "--json"); code != 1 || !strings.Contains(raw, "not_found") {
		t.Fatalf("unknown account: code=%d %s", code, raw)
	}
	if code, env, raw := runSearch(t, "from:ann", "--server", "--account", "Work", "--json"); code != 0 || env.Data.Total != 2 {
		t.Fatalf("by name: code=%d %s", code, raw)
	}
	if code, _, _ := runSearch(t, "from:ann", "--server", "--account", "acct-1", "--json"); code != 0 {
		t.Fatal("an account can also be named by its ID")
	}
	if code, _, raw := runSearch(t, "from:ann", "--account", "Work", "--json"); code != 2 {
		t.Fatalf("--account without --server: code=%d %s", code, raw)
	}
	var out, diag bytes.Buffer
	if code := RunMailRead("messages", []string{"list", "--server", "--json"}, &out, &diag); code != 2 {
		t.Fatalf("--server on list: code=%d %s", code, out.String())
	}
}

func TestSearchServerNeedsItsOwnAgentCapability(t *testing.T) {
	serverSearchCLIFixture(t)
	// Default agent permissions allow cached search but not contacting the server.
	if code, _, raw := runSearch(t, "from:ann", "--agent", "--json"); code != 0 {
		t.Fatalf("cached agent search: code=%d %s", code, raw)
	}
	code, _, raw := runSearch(t, "from:ann", "--server", "--agent", "--json")
	if code != 3 || !strings.Contains(raw, "messages.search_server") {
		t.Fatalf("agent server search: code=%d %s", code, raw)
	}
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	cfg.AgentPermissions = []string{"messages.search", "messages.search_server"}
	if err := config.Save(cfg); err != nil {
		t.Fatal(err)
	}
	if code, env, raw := runSearch(t, "from:ann", "--server", "--agent", "--json"); code != 0 || env.Data.Total != 2 {
		t.Fatalf("with the capability: code=%d %s", code, raw)
	}
}

func TestRelatedSearchFindsTheConversationFromTheServer(t *testing.T) {
	// The server holds a conversation (root, reply, my answer) and an unrelated
	// message with the same subject. Only the reply is cached locally.
	_, inbox := startCLIServer(t, []string{
		"From: Ann <ann@example.com>\r\nSubject: Budget\r\nDate: Mon, 02 Mar 2026 09:00:00 +0000\r\nMessage-ID: <root@t>\r\n\r\nx\r\n",
		"From: Bob <bob@example.com>\r\nSubject: Re: Budget\r\nDate: Tue, 03 Mar 2026 09:00:00 +0000\r\nMessage-ID: <reply@t>\r\nIn-Reply-To: <root@t>\r\nReferences: <root@t>\r\n\r\nx\r\n",
		"From: Ann <ann@example.com>\r\nSubject: Re: Budget\r\nDate: Wed, 04 Mar 2026 09:00:00 +0000\r\nMessage-ID: <mine@t>\r\nIn-Reply-To: <reply@t>\r\nReferences: <root@t> <reply@t>\r\n\r\nx\r\n",
		"From: Cy <cy@example.com>\r\nSubject: Budget\r\nDate: Thu, 05 Mar 2026 09:00:00 +0000\r\nMessage-ID: <other@t>\r\n\r\nx\r\n",
	})
	database, err := db.Open()
	if err != nil {
		t.Fatal(err)
	}
	if err := database.UpsertMessage(db.Message{MailboxID: inbox, UID: 2, Subject: "Re: Budget", MessageID: "<reply@t>", InReplyTo: "<root@t>", References: "<root@t>", Date: time.Date(2026, 3, 3, 9, 0, 0, 0, time.UTC)}); err != nil {
		t.Fatal(err)
	}
	replyID, err := database.MessageIDByUID(inbox, 2)
	if err != nil {
		t.Fatal(err)
	}
	database.Close()

	query := fmt.Sprintf("related:cache:%d", replyID)
	// Cached only: just the message we hold.
	if code, env, raw := runSearch(t, query, "--json"); code != 0 || env.Data.Total != 1 {
		t.Fatalf("cached: code=%d total=%d %s", code, env.Data.Total, raw)
	}
	// With the server: the root and my answer arrive; the same-subject stranger does not.
	code, env, raw := runSearch(t, query, "--server", "--json")
	if code != 0 || env.Data.Total != 3 || env.Data.Server == nil || env.Data.Server.Fetched != 2 {
		t.Fatalf("server: code=%d total=%d server=%+v %s", code, env.Data.Total, env.Data.Server, raw)
	}
	var subjects []string
	for _, it := range env.Data.Items {
		subjects = append(subjects, it.Subject)
	}
	if got := strings.Join(subjects, "|"); strings.Contains(got, "Budget") && strings.Count(got, "Budget") != 3 {
		t.Fatalf("subjects %q", got)
	}
	if code, env, _ := runSearch(t, "cy", "--json"); code != 0 || env.Data.Total != 0 {
		t.Fatal("the unrelated message with the same subject was downloaded")
	}
	// A message that is not cached cannot be followed.
	if code, _, raw := runSearch(t, "related:cache:9999", "--server", "--json"); code != 1 || !strings.Contains(raw, "not in the cache") {
		t.Fatalf("unknown message: code=%d %s", code, raw)
	}
}
