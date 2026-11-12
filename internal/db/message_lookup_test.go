package db

import "testing"

func TestMessageByMessageID(t *testing.T) {
	d := newTestDB(t)
	acc, _ := d.AddAccount("", "A", "")
	box, _ := d.UpsertMailbox(Mailbox{AccountID: acc, Name: "INBOX"})
	other, _ := d.UpsertMailbox(Mailbox{AccountID: acc, Name: "Archive"})
	// The same message in two folders; only one copy has its body cached.
	if err := d.UpsertMessage(Message{MailboxID: box, UID: 1, MessageID: "<Abc@X>", Subject: "no body"}); err != nil {
		t.Fatal(err)
	}
	if err := d.UpsertMessage(Message{MailboxID: other, UID: 7, MessageID: "<abc@x>", Subject: "with body", BodyText: "hello"}); err != nil {
		t.Fatal(err)
	}
	got, ok, err := d.MessageByMessageID("<ABC@x>")
	if err != nil || !ok || got.Subject != "with body" || got.BodyText != "hello" {
		t.Fatalf("got %+v ok=%v err=%v, want the copy with a body, case-insensitively", got, ok, err)
	}
	if _, ok, err := d.MessageByMessageID("<missing@x>"); ok || err != nil {
		t.Errorf("missing: ok=%v err=%v", ok, err)
	}
	if _, ok, _ := d.MessageByMessageID(""); ok {
		t.Error("an empty Message-ID must not match messages that have none")
	}
}
