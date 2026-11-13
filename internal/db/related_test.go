package db

import (
	"fmt"
	"reflect"
	"sort"
	"testing"
	"time"
)

// relatedFixture holds two conversations, an unrelated message with the same
// subject, and another account's mail that reuses a Message-ID.
type relatedFixture struct {
	*DB
	ids map[string]int64 // by label
}

func newRelatedFixture(t *testing.T) *relatedFixture {
	t.Helper()
	d := newTestDB(t)
	work, _ := d.AddAccount("", "Work", "")
	home, _ := d.AddAccount("", "Home", "")
	mk := func(acc int64, name string) int64 {
		id, err := d.UpsertMailbox(Mailbox{AccountID: acc, Name: name, Delimiter: "/"})
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	inbox, sent, archive, homeInbox := mk(work, "INBOX"), mk(work, "Sent"), mk(work, "Archive"), mk(home, "INBOX")
	f := &relatedFixture{DB: d, ids: map[string]int64{}}
	uid := uint32(0)
	add := func(label string, mailbox int64, subject, msgID, inReplyTo, refs string) {
		uid++
		date := time.Unix(1_800_000_000, 0).Add(time.Duration(uid) * time.Hour)
		if err := d.UpsertMessage(Message{MailboxID: mailbox, UID: uid, Subject: subject, MessageID: msgID, InReplyTo: inReplyTo, References: refs, Date: date}); err != nil {
			t.Fatal(err)
		}
		id, err := d.MessageIDByUID(mailbox, uid)
		if err != nil {
			t.Fatal(err)
		}
		f.ids[label] = id
	}
	// Conversation A: root in Archive, reply in INBOX, my answer in Sent, a late
	// reply that only names its parent (no References header).
	add("a-root", archive, "Budget", "<A1@x>", "", "")
	add("a-reply", inbox, "Re: Budget", "<A2@x>", "<A1@x>", "<A1@x>")
	add("a-mine", sent, "Re: Budget", "<A3@x>", "<a2@X>", "<A1@x> <A2@x>") // case differs
	add("a-late", inbox, "Re: Budget", "<A4@x>", "<A3@x>", "")
	// Conversation B has the same subject but no header link to A.
	add("b-root", inbox, "Budget", "<B1@x>", "", "")
	add("b-reply", inbox, "Re: Budget", "<B2@x>", "<B1@x>", "<B1@x>")
	// A message with no Message-ID that replies into B.
	add("b-anon", inbox, "Re: Budget", "", "<B2@x>", "<B1@x> <B2@x>")
	// A message with no Message-ID and no references: alone.
	add("lonely", inbox, "Budget", "", "", "")
	// A reference cycle must not hang or split.
	add("c-1", inbox, "Loop", "<C1@x>", "<C2@x>", "<C2@x>")
	add("c-2", inbox, "Loop", "<C2@x>", "<C1@x>", "<C1@x>")
	// Another account reusing A's Message-ID is never mixed in.
	add("home-a", homeInbox, "Budget", "<A1@x>", "", "")
	return f
}

func (f *relatedFixture) labels(t *testing.T, ids []int64) []string {
	t.Helper()
	back := map[int64]string{}
	for l, id := range f.ids {
		back[id] = l
	}
	var out []string
	for _, id := range ids {
		out = append(out, back[id])
	}
	sort.Strings(out)
	return out
}

func TestRelatedMessagesFollowReplyHeadersAcrossFolders(t *testing.T) {
	f := newRelatedFixture(t)
	wantA := []string{"a-late", "a-mine", "a-reply", "a-root"}
	// The same conversation is found from any member, in any folder.
	for _, from := range wantA {
		got, err := f.RelatedMessageIDs(f.ids[from])
		if err != nil {
			t.Fatal(err)
		}
		if l := f.labels(t, got); !reflect.DeepEqual(l, wantA) {
			t.Errorf("from %s: %v, want %v", from, l, wantA)
		}
	}
	for from, want := range map[string][]string{
		"b-root": {"b-anon", "b-reply", "b-root"},
		"b-anon": {"b-anon", "b-reply", "b-root"}, // no Message-ID of its own
		"lonely": {"lonely"},                      // a shared subject is not a link
		"c-1":    {"c-1", "c-2"},
		"home-a": {"home-a"}, // another account is never mixed in
	} {
		got, err := f.RelatedMessageIDs(f.ids[from])
		if err != nil {
			t.Fatal(err)
		}
		if l := f.labels(t, got); !reflect.DeepEqual(l, want) {
			t.Errorf("from %s: %v, want %v", from, l, want)
		}
	}
	if got, err := f.RelatedMessageIDs(999999); err != nil || len(got) != 0 {
		t.Errorf("unknown message: %v, %v", got, err)
	}
}

func TestRelatedMessageIDTokensIncludeIDsWhoseMailIsNotCached(t *testing.T) {
	f := newRelatedFixture(t)
	// a-late names <A3@x> as its parent; a reply to a message we never cached
	// still tells us that message's ID.
	d := f.DB
	box, _ := d.UpsertMailbox(Mailbox{AccountID: 1, Name: "INBOX", Delimiter: "/"})
	if err := d.UpsertMessage(Message{MailboxID: box, UID: 900, Subject: "Re: gone", MessageID: "<R1@x>", InReplyTo: "<missing@x>", References: "<missing@x>"}); err != nil {
		t.Fatal(err)
	}
	id, _ := d.MessageIDByUID(box, 900)
	got, err := d.RelatedMessageIDTokens(id)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, []string{"<missing@x>", "<r1@x>"}) {
		t.Errorf("tokens = %v", got)
	}
}

func TestSearchRelatedTerm(t *testing.T) {
	f := newRelatedFixture(t)
	q := func(s string) []string {
		found, err := f.SearchAllMessages(s, false)
		if err != nil {
			t.Fatal(err)
		}
		var ids []int64
		for _, m := range found {
			ids = append(ids, m.ID)
		}
		return f.labels(t, ids)
	}
	a := fmt.Sprintf("related:%d", f.ids["a-reply"])
	if got := q(a); !reflect.DeepEqual(got, []string{"a-late", "a-mine", "a-reply", "a-root"}) {
		t.Errorf("%s = %v", a, got)
	}
	if got := q(fmt.Sprintf("related:cache:%d", f.ids["a-reply"])); len(got) != 4 {
		t.Errorf("cache: prefix = %v", got)
	}
	// It combines with the rest of the language.
	if got := q(a + " subject:budget -subject:re"); !reflect.DeepEqual(got, []string{"a-root"}) {
		t.Errorf("combined = %v", got)
	}
	if got := q("-" + a + " subject:budget"); !reflect.DeepEqual(got, []string{"b-anon", "b-reply", "b-root", "home-a", "lonely"}) {
		t.Errorf("negated = %v", got)
	}
	// A message that does not exist selects nothing, and does not error.
	if got := q("related:99999"); len(got) != 0 {
		t.Errorf("unknown = %v", got)
	}
	// Newest first, with nothing else ranking the results.
	found, _ := f.SearchAllMessages(a, false)
	if len(found) != 4 || found[0].ID != f.ids["a-late"] || found[3].ID != f.ids["a-root"] {
		t.Errorf("order = %v", found)
	}
}
