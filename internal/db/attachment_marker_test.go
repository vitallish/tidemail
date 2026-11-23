package db

import "testing"

// The list marker must say "attachment" for things a person would call one, not
// for every part the parser keeps (#31). The stored has_attachment flag is left
// alone: the reader needs it to find cid images.
func TestAttachmentMarkerSeparatesRealAttachmentsFromLogosAndSignatures(t *testing.T) {
	d := newTestDB(t)
	acct, _ := d.AddAccount("", "A", "")
	box, err := d.UpsertMailbox(Mailbox{AccountID: acct, Name: "INBOX"})
	if err != nil {
		t.Fatal(err)
	}
	pdf := Attachment{Filename: "report.pdf", ContentType: "application/pdf", Data: []byte("%PDF")}
	logo := Attachment{Filename: "logo.png", ContentType: "image/png", ContentID: "logo@x", Inline: true, Data: []byte("png")}
	sig := Attachment{Filename: "signature.asc", ContentType: "application/pgp-signature", Data: []byte("sig")}
	photo := Attachment{Filename: "photo.jpg", ContentType: "image/jpeg", Inline: true, Data: []byte("jpg")} // inline, but no Content-ID: a pasted photo
	cases := []struct {
		subject string
		has     bool
		parts   []Attachment
		want    bool
	}{
		{"pdf", true, []Attachment{pdf}, true},
		{"logo only", true, []Attachment{logo}, false},
		{"signature only", true, []Attachment{sig}, false},
		{"logo and pdf", true, []Attachment{logo, pdf}, true},
		{"signature and pdf", true, []Attachment{sig, pdf}, true},
		{"inline photo without content id", true, []Attachment{photo}, true},
		{"flag set but no parts cached", true, nil, true},
		{"no attachment", false, nil, false},
	}
	for i, c := range cases {
		m := Message{MailboxID: box, UID: uint32(i + 1), Subject: c.subject, BodyText: "b", HasAttachment: c.has, AttachmentData: c.parts}
		if err := d.UpsertMessage(m); err != nil {
			t.Fatal(err)
		}
	}
	for _, list := range []struct {
		name string
		get  func() ([]Message, error)
	}{
		{"mailbox", func() ([]Message, error) { return d.ListMessages(box) }},
		{"unread first", func() ([]Message, error) { return d.ListMessagesUnreadFirst(box) }},
		{"unread", func() ([]Message, error) { return d.ListUnreadMessages(box) }},
	} {
		msgs, err := list.get()
		if err != nil {
			t.Fatalf("%s: %v", list.name, err)
		}
		got := map[string]Message{}
		for _, m := range msgs {
			got[m.Subject] = m
		}
		for _, c := range cases {
			m, ok := got[c.subject]
			if !ok {
				t.Fatalf("%s: %q missing", list.name, c.subject)
			}
			if m.AttachmentMarker != c.want {
				t.Errorf("%s: %q marker = %v, want %v", list.name, c.subject, m.AttachmentMarker, c.want)
			}
			if m.HasAttachment != c.has {
				t.Errorf("%s: %q has_attachment changed to %v", list.name, c.subject, m.HasAttachment)
			}
		}
	}
	found, err := d.SearchAllMessages("signature", false)
	if err != nil || len(found) == 0 {
		t.Fatalf("search: %v %v", found, err)
	}
	for _, m := range found {
		if m.Subject == "signature only" && m.AttachmentMarker {
			t.Error("search results must use the same rule")
		}
	}
}
