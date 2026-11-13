package imap

import (
	"context"
	"fmt"
	"net"
	"os"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/allisonhere/tidemail/internal/config"
	"github.com/allisonhere/tidemail/internal/searchquery"
	"github.com/emersion/go-imap/v2"
	imapclient "github.com/emersion/go-imap/v2/imapclient"
)

type searchMsg struct {
	from, to, subject, body, date string
	seen, flagged                 bool
}

// searchCorpus is deliberately small; each message differs in what the
// operators distinguish. UIDs are assigned 1..n in this order.
var searchCorpus = []searchMsg{
	{"Ann Lee <ann@example.com>", "me@work.test", "Invoice March", "please pay the attached invoice", "Tue, 10 Mar 2026 09:00:00 +0000", true, false},
	{"Bob Ray <bob@example.com>", "me@work.test", "Lunch", "noon at the usual place", "Wed, 11 Mar 2026 12:00:00 +0000", false, true},
	{"me@work.test", "ann@example.com", "Re: Invoice March", "paid, thanks", "Thu, 12 Mar 2026 08:00:00 +0000", true, false},
	{"News <news@example.org>", "me@work.test", "Newsletter", "invoice season is here", "Wed, 01 Apr 2026 07:30:00 +0000", false, false},
	{"Cy <cy@example.net>", "me@work.test", "Photos", "holiday pictures", "Thu, 02 Apr 2026 23:59:00 +0000", true, false},
}

func appendCorpus(t *testing.T, port int, mailbox string) {
	t.Helper()
	appendCorpusTo(t, fmt.Sprintf("127.0.0.1:%d", port), "testuser", "testpass", mailbox)
}

func appendCorpusTo(t *testing.T, addr, user, pass, mailbox string) {
	t.Helper()
	conn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	client := imapclient.New(conn, nil)
	defer client.Close()
	if err := client.Login(user, pass).Wait(); err != nil {
		t.Fatal(err)
	}
	for i, m := range searchCorpus {
		raw := fmt.Sprintf("From: %s\r\nTo: %s\r\nSubject: %s\r\nDate: %s\r\nMessage-ID: <m%d@test>\r\n\r\n%s\r\n",
			m.from, m.to, m.subject, m.date, i+1, m.body)
		var flags []imap.Flag
		if m.seen {
			flags = append(flags, imap.FlagSeen)
		}
		if m.flagged {
			flags = append(flags, imap.FlagFlagged)
		}
		when, _ := time.Parse(time.RFC1123Z, m.date)
		cmd := client.Append(mailbox, int64(len(raw)), &imap.AppendOptions{Flags: flags, Time: when})
		_, _ = cmd.Write([]byte(raw))
		_ = cmd.Close()
		if _, err := cmd.Wait(); err != nil {
			t.Fatalf("append %d: %v", i+1, err)
		}
	}
}

func connectTestClient(t *testing.T, port int) *Client {
	t.Helper()
	c := New(config.AccountConfig{IMAPHost: "127.0.0.1", IMAPPort: port, User: "testuser", Password: "testpass"})
	if err := c.Connect(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close() })
	return c
}

var searchNow = time.Date(2026, 11, 12, 12, 0, 0, 0, time.Local)

func TestSearchCriteriaSelectsOnTheServer(t *testing.T) {
	port, cleanup := startTestServer(t)
	defer cleanup()
	appendCorpus(t, port, "INBOX")
	c := connectTestClient(t, port)

	checkSearchCases(t, c, "INBOX")
}

// checkSearchCases runs every query against the corpus in mailbox.
func checkSearchCases(t *testing.T, c *Client, mailbox string) {
	t.Helper()
	cases := []struct {
		query string
		want  []uint32
	}{
		{"invoice", []uint32{1, 3, 4}}, // TEXT: subject, headers and body
		{"subject:invoice", []uint32{1, 3}},
		{"body:invoice", []uint32{1, 4}},
		{"from:ann", []uint32{1}},
		{"to:ann", []uint32{3}},
		{"from:ann OR from:bob", []uint32{1, 2}},
		{"from:ann OR from:bob OR from:cy", []uint32{1, 2, 5}},
		{"from:ann OR from:bob subject:lunch", []uint32{2}},
		{"invoice -subject:re", []uint32{1, 4}},
		{"is:unread", []uint32{2, 4}},
		{"is:read", []uint32{1, 3, 5}},
		{"is:starred", []uint32{2}},
		{"-is:starred", []uint32{1, 3, 4, 5}},
		{"after:2026-03-11 before:2026-04-01", []uint32{2, 3}},
		{"on:2026-04-02", []uint32{5}},
		{"on:2026-04-01 OR on:2026-03-10", []uint32{1, 4}},
		// Terms the server cannot search are left out; the rest still narrows.
		{"has:attachment from:ann", []uint32{1}},
		{"#github subject:lunch", []uint32{2}},
		{"from:ann OR #github subject:lunch", []uint32{2}}, // the OR clause mixes in a tag, so it is left to the local filter
	}
	for _, tc := range cases {
		q := searchquery.Parse(tc.query, searchNow)
		crit, _, ok := SearchCriteria(q)
		if !ok {
			t.Errorf("%q: nothing to search on the server", tc.query)
			continue
		}
		got, err := c.SearchUIDs(context.Background(), mailbox, crit)
		if err != nil {
			t.Errorf("%q: %v", tc.query, err)
			continue
		}
		if !reflect.DeepEqual(got, tc.want) {
			t.Errorf("%q = %v, want %v", tc.query, got, tc.want)
		}
	}
}

func TestSearchCriteriaRefusesWhatTheServerCannotSearch(t *testing.T) {
	for _, query := range []string{"has:attachment", "-has:attachment", "#github", "has:attachment OR from:ann", "#a OR #b", ""} {
		if _, _, ok := SearchCriteria(searchquery.Parse(query, searchNow)); ok {
			t.Errorf("%q: expected nothing for the server to search", query)
		}
	}
}

func TestSearchCriteriaReportsWhatItLeftOut(t *testing.T) {
	_, skipped, ok := SearchCriteria(searchquery.Parse("from:ann has:attachment #x", searchNow))
	if !ok || len(skipped) != 2 {
		t.Fatalf("ok=%v skipped=%v, want from:ann searched and two terms left out", ok, skipped)
	}
	if skipped[0].Field != searchquery.HasAtt || skipped[1].Field != searchquery.Tag {
		t.Errorf("skipped = %v", skipped)
	}
}

func TestFetchUIDsReturnsOnlyTheRequestedMessages(t *testing.T) {
	port, cleanup := startTestServer(t)
	defer cleanup()
	appendCorpus(t, port, "INBOX")
	c := connectTestClient(t, port)

	msgs, err := c.FetchUIDs(context.Background(), "INBOX", []uint32{2, 4})
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, m := range msgs {
		got = append(got, m.Subject)
	}
	if !reflect.DeepEqual(got, []string{"Lunch", "Newsletter"}) {
		t.Errorf("fetched %v", got)
	}
	if none, err := c.FetchUIDs(context.Background(), "INBOX", nil); err != nil || none != nil {
		t.Errorf("empty request = %v, %v", none, err)
	}
	if _, err := c.SearchUIDs(context.Background(), "NoSuchFolder", &imap.SearchCriteria{}); err == nil {
		t.Error("searching a missing folder must fail")
	}
}

// TestSearchAgainstARealServer runs the same cases against a real IMAP server
// (the in-memory server above is lenient and incomplete). It is off unless
// TIDEMAIL_TEST_IMAP is set to "host:port user password" of a throwaway server
// such as the Dovecot container in the vault's harness/dovecot, because it
// creates and deletes a folder there:
//
//	TIDEMAIL_TEST_IMAP="127.0.0.1:31143 work1@test.local work1pass" go test ./internal/imap -run RealServer -v
func TestSearchAgainstARealServer(t *testing.T) {
	spec := os.Getenv("TIDEMAIL_TEST_IMAP")
	if spec == "" {
		t.Skip("set TIDEMAIL_TEST_IMAP=\"host:port user password\" to run against a real server")
	}
	fields := strings.Fields(spec)
	if len(fields) != 3 {
		t.Fatalf("TIDEMAIL_TEST_IMAP must be \"host:port user password\", got %d fields", len(fields))
	}
	host, portText, err := net.SplitHostPort(fields[0])
	if err != nil {
		t.Fatal(err)
	}
	port, err := strconv.Atoi(portText)
	if err != nil {
		t.Fatal(err)
	}
	c := New(config.AccountConfig{IMAPHost: host, IMAPPort: port, User: fields[1], Password: fields[2]})
	if err := c.Connect(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close() })

	mailbox := fmt.Sprintf("search-test-%d", time.Now().UnixNano())
	if err := c.CreateMailbox(context.Background(), mailbox); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.DeleteMailbox(context.Background(), mailbox) })
	appendCorpusTo(t, fields[0], fields[1], fields[2], mailbox)

	checkSearchCases(t, c, mailbox)
}
