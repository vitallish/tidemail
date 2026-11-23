package db

import "testing"

// A forward has to remember which message it carries (#20): In-Reply-To would
// thread it into the original's conversation, so it has its own column.
func TestDraftRemembersWhatItForwards(t *testing.T) {
	path := t.TempDir() + "/mail.db"
	d, err := OpenPath(path)
	if err != nil {
		t.Fatal(err)
	}
	id, err := d.SaveDraft(Draft{AccountConfigID: "a", Subject: "Fwd: Budget", BodyText: "x", ForwardOf: "<orig@x>"})
	if err != nil {
		t.Fatal(err)
	}
	got, err := d.GetDraft(id)
	if err != nil || got.ForwardOf != "<orig@x>" || got.InReplyTo != "" {
		t.Fatalf("round trip: %+v %v", got, err)
	}
	// Autosave rebuilds the draft from the screen and sends the key again.
	got.BodyText = "edited"
	if _, err := d.SaveDraft(got); err != nil {
		t.Fatal(err)
	}
	if again, _ := d.GetDraft(id); again.ForwardOf != "<orig@x>" || again.BodyText != "edited" {
		t.Fatalf("after autosave: %+v", again)
	}
	listed, err := d.ListDrafts("a", "", "")
	if err != nil || len(listed) != 1 || listed[0].ForwardOf != "<orig@x>" {
		t.Fatalf("list: %+v %v", listed, err)
	}
	// Reopening the file (migrations run again) keeps it.
	d.Close()
	d, err = OpenPath(path)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	if again, err := d.GetDraft(id); err != nil || again.ForwardOf != "<orig@x>" {
		t.Fatalf("after reopen: %+v %v", again, err)
	}
}
