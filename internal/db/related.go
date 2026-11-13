package db

import (
	"database/sql"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// Related messages: everything in an account whose reply headers connect it,
// directly or through other messages, to a chosen message.
//
// Only Message-ID, In-Reply-To and References decide; the subject never does.
// This is the same connected component conversation.BuildThreads makes for the
// message list. That package imports db, so the rule is repeated here, and
// conversation's tests assert the two agree.

var relatedIDToken = regexp.MustCompile(`<[^<>\s]+>`)

// normalizeRelatedID matches conversation.NormalizeMessageID.
func normalizeRelatedID(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	s = strings.TrimSpace(strings.Trim(s, "<>"))
	if s == "" || strings.ContainsAny(s, " \t\r\n") {
		return ""
	}
	return strings.ToLower("<" + s + ">")
}

func relatedIDList(s string) []string {
	var ids []string
	for _, m := range relatedIDToken.FindAllString(s, -1) {
		if id := normalizeRelatedID(m); id != "" {
			ids = append(ids, id)
		}
	}
	return ids
}

// relatedComponent finds the component of message id and returns its row IDs
// (ascending) and the Message-IDs it knows about (own and referenced).
func (db *DB) relatedComponent(id int64) (rows []int64, messageIDs []string, err error) {
	var accountID int64
	err = db.QueryRow(`SELECT mailboxes.account_id FROM messages
		JOIN mailboxes ON mailboxes.id = messages.mailbox_id WHERE messages.id = ?`, id).Scan(&accountID)
	if err == sql.ErrNoRows {
		return nil, nil, nil
	}
	if err != nil {
		return nil, nil, err
	}
	result, err := db.Query(`SELECT messages.id, messages.message_id, messages.in_reply_to, messages.references_text
		FROM messages JOIN mailboxes ON mailboxes.id = messages.mailbox_id
		WHERE mailboxes.account_id = ?`, accountID)
	if err != nil {
		return nil, nil, err
	}
	defer result.Close()

	parent := map[string]string{}
	var find func(string) string
	find = func(k string) string {
		p, ok := parent[k]
		if !ok {
			parent[k] = k
			return k
		}
		if p == k {
			return k
		}
		root := find(p)
		parent[k] = root
		return root
	}
	union := func(a, b string) {
		if ra, rb := find(a), find(b); ra != rb {
			parent[rb] = ra
		}
	}
	type row struct {
		id  int64
		key string
	}
	var all []row
	for result.Next() {
		var rowID int64
		var mid, inReplyTo, refs string
		if err := result.Scan(&rowID, &mid, &inReplyTo, &refs); err != nil {
			return nil, nil, err
		}
		key := normalizeRelatedID(mid)
		if key == "" {
			key = "row:" + strconv.FormatInt(rowID, 10) // no Message-ID: its own node
		}
		find(key)
		for _, ref := range relatedIDList(refs) {
			union(key, ref)
		}
		for _, ref := range relatedIDList(inReplyTo) {
			union(key, ref)
		}
		all = append(all, row{rowID, key})
	}
	if err := result.Err(); err != nil {
		return nil, nil, err
	}
	var start string
	for _, r := range all {
		if r.id == id {
			start = find(r.key)
		}
	}
	if start == "" {
		return nil, nil, nil
	}
	for _, r := range all {
		if find(r.key) == start {
			rows = append(rows, r.id)
		}
	}
	for k := range parent {
		if !strings.HasPrefix(k, "row:") && find(k) == start {
			messageIDs = append(messageIDs, k)
		}
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i] < rows[j] })
	sort.Strings(messageIDs)
	return rows, messageIDs, nil
}

// RelatedMessageIDs returns the row IDs of every message in the same account
// that is connected to message id by reply headers, id included. A message that
// is not cached yields none.
func (db *DB) RelatedMessageIDs(id int64) ([]int64, error) {
	rows, _, err := db.relatedComponent(id)
	return rows, err
}

// RelatedMessageIDTokens returns the normalized Message-IDs (with angle
// brackets) of message id's conversation: its own and every one it references
// or is referenced by, including ones whose messages are not cached. Server
// search uses them to look for the rest.
func (db *DB) RelatedMessageIDTokens(id int64) ([]string, error) {
	_, ids, err := db.relatedComponent(id)
	return ids, err
}
