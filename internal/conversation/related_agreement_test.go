package conversation

import (
	"fmt"
	"math/rand"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/allisonhere/tidemail/internal/db"
)

// db.RelatedMessageIDs repeats the grouping rule of BuildThreads (the db package
// cannot import this one). This test builds many random header graphs and checks
// that, for every message, the two return the same set. If either changes, it fails.
func TestRelatedMessageIDsAgreeWithBuildThreads(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	for round := 0; round < 25; round++ {
		database, err := db.OpenPath(t.TempDir() + "/mail.db")
		if err != nil {
			t.Fatal(err)
		}
		acc, _ := database.AddAccount("", "A", "")
		box1, _ := database.UpsertMailbox(db.Mailbox{AccountID: acc, Name: "INBOX"})
		box2, _ := database.UpsertMailbox(db.Mailbox{AccountID: acc, Name: "Sent"})
		pool := 12
		id := func(n int) string {
			s := fmt.Sprintf("<m%d@x>", n)
			if rng.Intn(3) == 0 {
				s = strings.ToUpper(s) // case must not matter
			}
			return s
		}
		for i := 0; i < 30; i++ {
			m := db.Message{MailboxID: box1, UID: uint32(i + 1), Subject: "same subject", Date: time.Unix(int64(1000+i), 0)}
			if i%2 == 1 {
				m.MailboxID = box2
			}
			if rng.Intn(6) != 0 { // some messages have no Message-ID
				m.MessageID = id(rng.Intn(pool))
			}
			if rng.Intn(2) == 0 {
				m.InReplyTo = id(rng.Intn(pool))
			}
			for k := rng.Intn(3); k > 0; k-- {
				m.References += id(rng.Intn(pool)) + " "
			}
			if err := database.UpsertMessage(m); err != nil {
				t.Fatal(err)
			}
		}
		var all []db.Message
		for _, box := range []int64{box1, box2} {
			msgs, err := database.ListMessages(box)
			if err != nil {
				t.Fatal(err)
			}
			all = append(all, msgs...)
		}
		want := map[int64][]int64{} // message -> its thread, by BuildThreads
		for _, th := range BuildThreads(all) {
			var ids []int64
			for _, m := range th.Messages {
				ids = append(ids, m.ID)
			}
			sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
			for _, m := range th.Messages {
				want[m.ID] = ids
			}
		}
		for _, m := range all {
			got, err := database.RelatedMessageIDs(m.ID)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, want[m.ID]) {
				t.Fatalf("round %d message %d: db says %v, BuildThreads says %v", round, m.ID, got, want[m.ID])
			}
		}
		database.Close()
	}
}
