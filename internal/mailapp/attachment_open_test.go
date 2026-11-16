package mailapp

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/allisonhere/tidemail/internal/db"
)

func TestAttachmentFileName(t *testing.T) {
	for _, tc := range []struct {
		in   db.Attachment
		want string
	}{
		{db.Attachment{Filename: "Report Q3.pdf"}, "Report Q3.pdf"},
		{db.Attachment{Filename: "../../etc/passwd"}, ".._.._etc_passwd"},
		{db.Attachment{Filename: `a\b/c.txt`}, "a_b_c.txt"},
		{db.Attachment{Filename: ""}, "attachment"},
		{db.Attachment{Filename: "..", ContentType: ""}, "attachment"},
		{db.Attachment{Filename: "scan", ContentType: "application/pdf"}, "scan.pdf"},
		{db.Attachment{Filename: "tab\x00name.txt"}, "tab_name.txt"},
	} {
		if got := AttachmentFileName(tc.in); got != tc.want {
			t.Errorf("AttachmentFileName(%+v) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestOpenableAttachmentsSkipsEmbeddedParts(t *testing.T) {
	got := OpenableAttachments([]db.Attachment{{Filename: "a.pdf"}, {Filename: "logo.png", Inline: true}, {Filename: "b.csv"}})
	if len(got) != 2 || got[0].Filename != "a.pdf" || got[1].Filename != "b.csv" {
		t.Fatalf("got %+v", got)
	}
}

func TestOpenAttachmentUsesDefaultOpenerOnPrivateCopy(t *testing.T) {
	dir := t.TempDir()
	var opened string
	path, err := OpenAttachment(nil, db.Attachment{Filename: "Plan.pdf", Data: []byte("%PDF")}, OpenAttachmentOptions{
		Dir:         dir,
		DefaultOpen: func(p string) error { opened = p; return nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	if opened != path || filepath.Base(path) != "Plan.pdf" || filepath.Dir(filepath.Dir(path)) != dir {
		t.Fatalf("opened %q, returned %q", opened, path)
	}
	if data, _ := os.ReadFile(path); string(data) != "%PDF" {
		t.Fatalf("copy holds %q", data)
	}
	if info, _ := os.Stat(path); info.Mode().Perm() != 0o600 {
		t.Fatalf("copy mode %v, want 0600", info.Mode().Perm())
	}
	if info, _ := os.Stat(filepath.Dir(path)); info.Mode().Perm() != 0o700 {
		t.Fatalf("folder mode %v, want 0700", info.Mode().Perm())
	}
}

func TestOpenAttachmentCommandQuotesTheFile(t *testing.T) {
	var gotName string
	var gotArgs []string
	run := func(name string, args ...string) error { gotName, gotArgs = name, args; return nil }
	a := db.Attachment{Filename: "it's a $(touch x) file.txt", Data: []byte("x")}

	path, err := OpenAttachment(nil, a, OpenAttachmentOptions{Dir: t.TempDir(), Command: "zathura {file}", Run: run})
	if err != nil {
		t.Fatal(err)
	}
	if gotName != "sh" || len(gotArgs) != 2 || gotArgs[0] != "-c" {
		t.Fatalf("ran %q %q", gotName, gotArgs)
	}
	want := "zathura " + shellQuote(path)
	if gotArgs[1] != want {
		t.Fatalf("command = %q, want %q", gotArgs[1], want)
	}
	if !strings.Contains(gotArgs[1], `'\''`) {
		t.Fatalf("single quote in the file name was not escaped: %q", gotArgs[1])
	}

	// Without {file} the path is appended.
	_, err = OpenAttachment(nil, db.Attachment{Filename: "a.txt", Data: []byte("x")}, OpenAttachmentOptions{Dir: t.TempDir(), Command: "less", Run: run})
	if err != nil || !strings.HasPrefix(gotArgs[1], "less '") {
		t.Fatalf("command = %q (%v)", gotArgs[1], err)
	}
}

func TestOpenAttachmentRemovesTheCopyWhenTheOpenerFails(t *testing.T) {
	dir := t.TempDir()
	_, err := OpenAttachment(nil, db.Attachment{Filename: "a.txt", Data: []byte("x")}, OpenAttachmentOptions{
		Dir: dir, DefaultOpen: func(string) error { return os.ErrNotExist },
	})
	if err == nil {
		t.Fatal("expected the opener error")
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Fatalf("failed open left %d entries behind", len(entries))
	}
}

func TestOpenAttachmentRefusesEmptyContent(t *testing.T) {
	_, err := OpenAttachment(nil, db.Attachment{Filename: "a.txt"}, OpenAttachmentOptions{Dir: t.TempDir(), DefaultOpen: func(string) error { return nil }})
	if err == nil {
		t.Fatal("an attachment with no data must not be opened")
	}
}

func TestOpenAttachmentLoadsDataFromTheDatabase(t *testing.T) {
	database, err := db.OpenPath(filepath.Join(t.TempDir(), "mail.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close() //nolint:errcheck
	accountID, err := database.AddAccount("", "Work", "")
	if err != nil {
		t.Fatal(err)
	}
	mailboxID, err := database.UpsertMailbox(db.Mailbox{AccountID: accountID, Name: "INBOX"})
	if err != nil {
		t.Fatal(err)
	}
	if err := database.UpsertMessage(db.Message{MailboxID: mailboxID, UID: 1, Subject: "Hi", Date: time.Unix(1, 0)}); err != nil {
		t.Fatal(err)
	}
	id, err := database.SaveAttachment(1, db.Attachment{Filename: "data.csv", ContentType: "text/csv", Data: []byte("a,b\n1,2\n")})
	if err != nil {
		t.Fatal(err)
	}
	// The reading pane keeps metadata only; contents must be fetched at open time.
	metas, err := database.GetAttachmentsMeta(1)
	if err != nil || len(metas) != 1 || metas[0].ID != id || len(metas[0].Data) != 0 {
		t.Fatalf("metadata = %+v (%v)", metas, err)
	}
	path, err := OpenAttachment(database, metas[0], OpenAttachmentOptions{Dir: t.TempDir(), DefaultOpen: func(string) error { return nil }})
	if err != nil {
		t.Fatal(err)
	}
	if data, _ := os.ReadFile(path); string(data) != "a,b\n1,2\n" {
		t.Fatalf("copy holds %q", data)
	}
}

func TestPruneOpenedRemovesOnlyOldOpenFolders(t *testing.T) {
	dir := t.TempDir()
	old := filepath.Join(dir, "open-old")
	fresh := filepath.Join(dir, "open-fresh")
	other := filepath.Join(dir, "keep-me")
	for _, d := range []string{old, fresh, other} {
		if err := os.Mkdir(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	now := time.Now()
	for d, age := range map[string]time.Duration{old: 48 * time.Hour, fresh: time.Hour, other: 48 * time.Hour} {
		if err := os.Chtimes(d, now.Add(-age), now.Add(-age)); err != nil {
			t.Fatal(err)
		}
	}
	pruneOpened(dir, now, OpenedAttachmentMaxAge)
	for d, want := range map[string]bool{old: false, fresh: true, other: true} {
		if _, err := os.Stat(d); (err == nil) != want {
			t.Errorf("%s exists = %v, want %v", filepath.Base(d), err == nil, want)
		}
	}
}
