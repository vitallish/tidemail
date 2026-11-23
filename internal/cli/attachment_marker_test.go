package cli

import (
	"bytes"
	"encoding/json"
	"testing"
	"time"

	"github.com/allisonhere/tidemail/internal/db"
)

// The command line gives the same answer as the list marker (#31): has_attachment
// stays the broad stored flag, attachment_marker is the one the TUI shows.
func TestMessagesListReportsAttachmentMarker(t *testing.T) {
	mailReadFixture(t)
	database, err := db.Open()
	if err != nil {
		t.Fatal(err)
	}
	boxes, _ := database.ListMailboxes(1)
	box := boxes[0].ID
	logo := db.Attachment{Filename: "logo.png", ContentType: "image/png", ContentID: "logo@x", Inline: true, Data: []byte("p")}
	pdf := db.Attachment{Filename: "a.pdf", ContentType: "application/pdf", Data: []byte("%PDF")}
	for i, c := range []struct {
		subject string
		part    db.Attachment
	}{{"logo mail", logo}, {"pdf mail", pdf}} {
		if err := database.UpsertMessage(db.Message{MailboxID: box, UID: uint32(100 + i), Subject: c.subject, From: "s@example.com", BodyText: "b", Date: time.Unix(200+int64(i), 0), HasAttachment: true, AttachmentData: []db.Attachment{c.part}}); err != nil {
			t.Fatal(err)
		}
	}
	database.Close()

	var out, diagnostics bytes.Buffer
	if code := RunMailRead("messages", []string{"list", "--json"}, &out, &diagnostics); code != 0 {
		t.Fatalf("%d: %s", code, diagnostics.String())
	}
	var envelope struct {
		Data struct {
			Items []struct {
				Subject          string `json:"subject"`
				HasAttachment    bool   `json:"has_attachment"`
				AttachmentMarker bool   `json:"attachment_marker"`
			} `json:"items"`
		} `json:"data"`
	}
	if err := json.Unmarshal(out.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	want := map[string][2]bool{"logo mail": {true, false}, "pdf mail": {true, true}, "Invoice": {false, false}}
	for _, it := range envelope.Data.Items {
		w, ok := want[it.Subject]
		if !ok {
			continue
		}
		delete(want, it.Subject)
		if it.HasAttachment != w[0] || it.AttachmentMarker != w[1] {
			t.Errorf("%s: has_attachment=%v attachment_marker=%v, want %v", it.Subject, it.HasAttachment, it.AttachmentMarker, w)
		}
	}
	if len(want) != 0 {
		t.Errorf("missing from the list: %v\n%s", want, out.String())
	}
}
