package cli

import (
	"database/sql"
	"errors"
	"fmt"
	"mime"
	"strings"

	"github.com/allisonhere/tidemail/internal/config"
	"github.com/allisonhere/tidemail/internal/db"
	"github.com/allisonhere/tidemail/internal/mailapp"
)

// applyForward turns a draft into a forward of a cached message, as the composer
// does: a "Fwd:" subject, the original's attachments, no threading (a forward must
// not join the original's conversation), the draft remembering what it forwards,
// and the original quoted as quoteMode says. The recipient stays the caller's.
func applyForward(d db.Draft, cfg config.Config, agent bool, forward, quoteMode string, signature, quoteImages bool) (db.Draft, []mailapp.ComposeOption, *replyError) {
	if quoteMode != "marker" && quoteMode != "text" && quoteMode != "none" {
		return d, nil, &replyError{"invalid_arguments", "--quote must be marker, text or none", 2}
	}
	if agent && !mailapp.Allowed(cfg, "messages.read") {
		return d, nil, &replyError{"permission_denied", "agent capability denied: messages.read (a forward copies the original's body and attachments)", 3}
	}
	id, err := readID(forward)
	if err != nil {
		return d, nil, &replyError{"invalid_arguments", "invalid --forward; use an ID from messages list, such as cache:123", 2}
	}
	database, err := db.Open()
	if err != nil {
		return d, nil, &replyError{"database_error", "cannot open mail cache", 1}
	}
	defer database.Close() //nolint:errcheck
	original, err := database.GetMessage(id)
	if errors.Is(err, sql.ErrNoRows) {
		return d, nil, &replyError{"not_found", "the message to forward is not in the cache", 1}
	}
	if err != nil {
		return d, nil, &replyError{"database_error", "cannot read the message to forward", 1}
	}
	if strings.TrimSpace(original.MessageID) == "" {
		return d, nil, &replyError{"invalid_arguments", "that message has no Message-ID to forward", 2}
	}
	d.ForwardOf = original.MessageID
	if strings.TrimSpace(d.Subject) == "" {
		d.Subject = original.Subject
		if !strings.HasPrefix(strings.ToLower(strings.TrimSpace(d.Subject)), "fwd:") {
			d.Subject = "Fwd: " + d.Subject
		}
	}
	parts, err := database.GetAttachments(original.ID)
	if err != nil {
		return d, nil, &replyError{"database_error", "cannot read the attachments of the message to forward", 1}
	}
	for i, p := range parts {
		name := p.Filename
		if name == "" { // embedded images often have no filename
			name = fmt.Sprintf("attachment-%d", i+1)
			if exts, _ := mime.ExtensionsByType(p.ContentType); len(exts) > 0 {
				name += exts[0]
			}
		}
		d.Attachments = append(d.Attachments, db.DraftAttachment{Filename: name, ContentType: p.ContentType, Data: p.Data, Size: int64(len(p.Data))})
	}
	body := strings.TrimRight(d.BodyText, "\n")
	switch quoteMode {
	case "marker":
		tail := "\n\n"
		if signature {
			tail += mailapp.SignatureMarker + "\n\n"
		}
		d.BodyText = body + tail + mailapp.QuoteMarker + "\n"
		lookup := func(messageID string) (db.Message, bool, error) {
			lookupDB, e := db.Open()
			if e != nil {
				return db.Message{}, false, e
			}
			defer lookupDB.Close() //nolint:errcheck
			return lookupDB.MessageByMessageID(messageID)
		}
		opts := []mailapp.ComposeOption{mailapp.WithForwardQuote(d.ForwardOf, lookup)}
		if quoteImages {
			opts = append(opts, mailapp.WithQuoteImages(quoteImageLoader))
		}
		return d, opts, nil
	case "text":
		d.BodyText = strings.TrimLeft(body+"\n\n"+mailapp.BuildForwardQuote(original).ExpandedMarkdown()+"\n", "\n")
	}
	return d, nil, nil
}

// quoteImageLoader reads a quoted message's cached image parts, opening the
// cache for the call like the lookup does.
func quoteImageLoader(original db.Message) ([]db.Attachment, error) {
	partsDB, err := db.Open()
	if err != nil {
		return nil, err
	}
	defer partsDB.Close() //nolint:errcheck
	return partsDB.GetImageParts(original.ID)
}
