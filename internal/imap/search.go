package imap

import (
	"context"
	"fmt"
	"sort"

	"github.com/allisonhere/tidemail/internal/db"
	"github.com/allisonhere/tidemail/internal/searchquery"
	"github.com/emersion/go-imap/v2"
)

// SearchCriteria compiles a parsed search into IMAP SEARCH criteria.
//
// The result must be a SUPERSET of what the local search would show: the
// server only decides which messages to download, and the normal local search
// then filters them. So a clause the server cannot express in full is left out
// rather than approximated, and skipped lists those terms. Left out are #tags
// (local only) and has:attachment (IMAP has no such criterion), which is why
// "has:attachment from:ann" works against any server: the server narrows by
// sender and the cached flag does the rest.
//
// ok is false when nothing at all is left for the server to search, such as a
// bare "has:attachment"; the caller should not fetch a whole folder for that.
func SearchCriteria(q searchquery.Query) (criteria *imap.SearchCriteria, skipped []searchquery.Term, ok bool) {
	crit := &imap.SearchCriteria{}
	pushed := 0
	for _, clause := range q.Clauses {
		parts := make([]imap.SearchCriteria, 0, len(clause))
		complete := true
		for _, term := range clause {
			c, can := termCriteria(term)
			if !can {
				complete = false
				break
			}
			parts = append(parts, c)
		}
		if !complete {
			skipped = append(skipped, clause...)
			continue
		}
		crit.And(orCriteria(parts))
		pushed++
	}
	if pushed == 0 {
		return nil, skipped, false
	}
	return crit, skipped, true
}

// orCriteria joins criteria with OR; IMAP's OR takes exactly two keys, so three
// or more nest: a OR (b OR c).
func orCriteria(parts []imap.SearchCriteria) *imap.SearchCriteria {
	if len(parts) == 1 {
		return &parts[0]
	}
	tail := orCriteria(parts[1:])
	return &imap.SearchCriteria{Or: [][2]imap.SearchCriteria{{parts[0], *tail}}}
}

// termCriteria is the criteria for one term; ok is false for terms the server
// cannot search.
func termCriteria(t searchquery.Term) (imap.SearchCriteria, bool) {
	var c imap.SearchCriteria
	switch t.Field {
	case searchquery.Any:
		c.Text = []string{t.Value} // headers and body
	case searchquery.Body:
		c.Body = []string{t.Value}
	case searchquery.Subject:
		c.Header = []imap.SearchCriteriaHeaderField{{Key: "Subject", Value: t.Value}}
	case searchquery.From:
		c.Header = []imap.SearchCriteriaHeaderField{{Key: "From", Value: t.Value}}
	case searchquery.To:
		c.Header = []imap.SearchCriteriaHeaderField{{Key: "To", Value: t.Value}}
	case searchquery.CC:
		c.Header = []imap.SearchCriteriaHeaderField{{Key: "Cc", Value: t.Value}}
	case searchquery.Date:
		// The Date: header, as the local search uses; IMAP compares whole days.
		c.SentSince, c.SentBefore = t.Start, t.End
	case searchquery.Unread:
		c.NotFlag = []imap.Flag{imap.FlagSeen}
	case searchquery.Read:
		c.Flag = []imap.Flag{imap.FlagSeen}
	case searchquery.Starred:
		c.Flag = []imap.Flag{imap.FlagFlagged}
	default:
		return c, false
	}
	if t.Negate {
		return imap.SearchCriteria{Not: []imap.SearchCriteria{c}}, true
	}
	return c, true
}

// SearchUIDs returns the UIDs in mailboxName matching criteria, ascending.
func (c *Client) SearchUIDs(ctx context.Context, mailboxName string, criteria *imap.SearchCriteria) ([]uint32, error) {
	if c.conn == nil {
		return nil, fmt.Errorf("not connected")
	}
	defer c.applyDeadline(ctx)()
	if _, err := c.conn.Select(mailboxName, &imap.SelectOptions{ReadOnly: true}).Wait(); err != nil {
		return nil, fmt.Errorf("select %s: %w", mailboxName, err)
	}
	found, err := c.conn.UIDSearch(criteria, nil).Wait()
	if err != nil {
		return nil, fmt.Errorf("search %s: %w", mailboxName, err)
	}
	all := found.AllUIDs()
	uids := make([]uint32, 0, len(all))
	for _, uid := range all {
		uids = append(uids, uint32(uid))
	}
	sort.Slice(uids, func(i, j int) bool { return uids[i] < uids[j] })
	return uids, nil
}

// FetchUIDs fetches the given messages from mailboxName, in one request.
// Callers keep batches small: a fetch pulls whole bodies.
func (c *Client) FetchUIDs(ctx context.Context, mailboxName string, uids []uint32) ([]db.Message, error) {
	if c.conn == nil {
		return nil, fmt.Errorf("not connected")
	}
	if len(uids) == 0 {
		return nil, nil
	}
	defer c.applyDeadline(ctx)()
	if _, err := c.conn.Select(mailboxName, &imap.SelectOptions{ReadOnly: true}).Wait(); err != nil {
		return nil, fmt.Errorf("select %s: %w", mailboxName, err)
	}
	set := make([]imap.UID, len(uids))
	for i, u := range uids {
		set[i] = imap.UID(u)
	}
	return c.fetchNumSet(imap.UIDSetNum(set...))
}
