package ui

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/allisonhere/tidemail/internal/browser"
	"github.com/allisonhere/tidemail/internal/db"
	imapClient "github.com/allisonhere/tidemail/internal/imap"
	tea "github.com/charmbracelet/bubbletea"
)

// emailExportMaxAge is how long an exported message stays on disk. A browser
// may read the file after its launcher exits, so exports are pruned by age
// rather than deleted immediately.
const emailExportMaxAge = time.Hour

type emailBrowserOpenedMsg struct {
	err   error
	msgID int64
	// blocked counts remote images left out of the export.
	blocked int
	// images reports that remote images were deliberately loaded.
	images bool
	// missing counts inline (cid:) images the export could not show.
	missing int
}

var fetchEmailSource = Model.messageSourceFetcher
var openEmailBrowser = browser.OpenLocalFile

// openEmailInBrowser exports msg and opens it. With allowImages the export
// keeps remote images, which tells the sender the message was opened.
func (m Model) openEmailInBrowser(msg db.Message, allowImages bool) (tea.Model, tea.Cmd) {
	mailbox := m.mailboxByID(msg.MailboxID)
	acfg, err := m.accountCfgForMailbox(msg.MailboxID)
	if mailbox == nil || err != nil || acfg.IMAPHost == "" || msg.UID == 0 {
		m.setStatus("This message can't be fetched from a server", true)
		return m, m.clearStatusCmd()
	}
	fetch := fetchEmailSource(m, msg)
	configured := m.cfg.Display.Browser
	m.setStatus("Opening message in browser…", false)
	id := msg.ID
	return m, func() tea.Msg {
		raw, err := fetch()
		if err != nil {
			return emailBrowserOpenedMsg{err: err, msgID: id}
		}
		blocked, missing, err := openEmailHTMLCounting(raw, configured, allowImages)
		return emailBrowserOpenedMsg{err: err, msgID: id, blocked: blocked, images: allowImages, missing: missing}
	}
}

// emailExportDir returns the private directory holding exported messages. It is
// per-user and must stay owner-only, so other local users cannot read exports
// or plant files for the browser to open.
func emailExportDir() (string, error) {
	dir := filepath.Join(os.TempDir(), fmt.Sprintf("tidemail-browser-%d", os.Getuid()))
	if err := os.Mkdir(dir, 0o700); err != nil && !os.IsExist(err) {
		return "", fmt.Errorf("create export directory: %w", err)
	}
	info, err := os.Lstat(dir)
	if err != nil {
		return "", fmt.Errorf("check export directory: %w", err)
	}
	if !info.IsDir() || (info.Mode().Perm()&0o077 != 0 && os.Getuid() >= 0) {
		return "", fmt.Errorf("export directory %s is not private", dir)
	}
	return dir, nil
}

// PruneEmailExports removes exported messages older than emailExportMaxAge.
// It is called at startup and before each export.
func PruneEmailExports() {
	dir, err := emailExportDir()
	if err != nil {
		return
	}
	pruneEmailExports(dir, time.Now(), emailExportMaxAge)
}

func pruneEmailExports(dir string, now time.Time, maxAge time.Duration) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		name := e.Name()
		if !e.Type().IsRegular() || !strings.HasPrefix(name, "tidemail-email-") || !strings.HasSuffix(name, ".html") {
			continue
		}
		if info, err := e.Info(); err == nil && now.Sub(info.ModTime()) > maxAge {
			_ = os.Remove(filepath.Join(dir, name))
		}
	}
}

// openEmailHTML exports the message's HTML with remote content blocked and
// opens it, in the configured browser when one is set. It reports how many
// remote images were left out; with allowImages remote images are kept.
func openEmailHTML(raw []byte, configured string, allowImages bool) (blocked int, err error) {
	blocked, _, err = openEmailHTMLCounting(raw, configured, allowImages)
	return blocked, err
}

// openEmailHTMLCounting is openEmailHTML that also reports how many inline
// (cid:) images could not be embedded. The message's own images are embedded as
// data: URIs: they make no network request, so they need no opt-in.
func openEmailHTMLCounting(raw []byte, configured string, allowImages bool) (blocked, missing int, err error) {
	src, images := imapClient.ExtractHTMLWithImages(raw)
	if src == "" {
		return 0, 0, fmt.Errorf("this message has no HTML body")
	}
	if !utf8.ValidString(src) {
		return 0, 0, fmt.Errorf("the message HTML could not be decoded as UTF-8")
	}
	cids := newCIDEmbedding(images)
	html, blocked, err := sanitizeEmailHTMLWith(strings.TrimPrefix(src, "\ufeff"), allowImages, cids.embed)
	if err != nil {
		return 0, 0, fmt.Errorf("prepare HTML: %w", err)
	}
	missing = cids.missing()
	// MIME decoding produces UTF-8. A leading BOM makes browsers use UTF-8
	// even when the original HTML still declares a different charset, without
	// rewriting its markup. Preserve an existing BOM rather than adding two.
	html = "\ufeff" + strings.TrimPrefix(html, "\ufeff")
	dir, err := emailExportDir()
	if err != nil {
		return 0, 0, err
	}
	pruneEmailExports(dir, time.Now(), emailExportMaxAge)
	file, err := os.CreateTemp(dir, "tidemail-email-*.html")
	if err != nil {
		return 0, 0, fmt.Errorf("create temporary HTML: %w", err)
	}
	path := file.Name()
	// Remove failed exports immediately; successful ones stay available for
	// reloads until they age out.
	keep := false
	defer func() {
		if !keep {
			_ = os.Remove(path)
		}
	}()
	_, writeErr := file.WriteString(html)
	closeErr := file.Close()
	if writeErr != nil {
		return 0, 0, fmt.Errorf("write temporary HTML: %w", writeErr)
	}
	if closeErr != nil {
		return 0, 0, fmt.Errorf("close temporary HTML: %w", closeErr)
	}
	if err := openEmailBrowser(configured, path); err != nil {
		return 0, 0, fmt.Errorf("could not launch browser: %w", err)
	}
	keep = true
	return blocked, missing, nil
}

// missingInlineNote is the status suffix for inline images that could not be shown.
func missingInlineNote(n int) string {
	switch {
	case n == 1:
		return " · 1 inline image could not be shown"
	case n > 1:
		return fmt.Sprintf(" · %d inline images could not be shown", n)
	}
	return ""
}
