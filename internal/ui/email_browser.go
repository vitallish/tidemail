package ui

import (
	"fmt"
	htmlpkg "html"
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
		blocked, err := openEmailHTML(raw, configured, allowImages)
		return emailBrowserOpenedMsg{err: err, msgID: id, blocked: blocked, images: allowImages}
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
	src := imapClient.ExtractHTML(raw)
	if src == "" {
		return 0, fmt.Errorf("this message has no HTML body")
	}
	if !utf8.ValidString(src) {
		return 0, fmt.Errorf("the message HTML could not be decoded as UTF-8")
	}
	html, blocked, err := sanitizeEmailHTML(strings.TrimPrefix(src, "\ufeff"), allowImages)
	if err != nil {
		return 0, fmt.Errorf("prepare HTML: %w", err)
	}
	// MIME decoding produces UTF-8. A leading BOM makes browsers use UTF-8
	// even when the original HTML still declares a different charset, without
	// rewriting its markup. Preserve an existing BOM rather than adding two.
	html = "\ufeff" + strings.TrimPrefix(html, "\ufeff")
	if err := writeAndOpenHTML(html, configured); err != nil {
		return 0, err
	}
	return blocked, nil
}

// writeAndOpenHTML writes html to the private export directory and opens it in
// the configured browser (or the system default when configured is empty).
func writeAndOpenHTML(html, configured string) error {
	dir, err := emailExportDir()
	if err != nil {
		return err
	}
	pruneEmailExports(dir, time.Now(), emailExportMaxAge)
	file, err := os.CreateTemp(dir, "tidemail-email-*.html")
	if err != nil {
		return fmt.Errorf("create temporary HTML: %w", err)
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
		return fmt.Errorf("write temporary HTML: %w", writeErr)
	}
	if closeErr != nil {
		return fmt.Errorf("close temporary HTML: %w", closeErr)
	}
	if err := openEmailBrowser(configured, path); err != nil {
		return fmt.Errorf("could not launch browser: %w", err)
	}
	keep = true
	return nil
}

// draftPreviewCSP lets the preview load nothing: no network, no scripts, no
// frames. Inline styles (the table styling) and data: images are allowed.
const draftPreviewCSP = "default-src 'none'; style-src 'unsafe-inline'; img-src data:; base-uri 'none'; form-action 'none'"

// previewDocument wraps the HTML part a draft would be sent with in a minimal
// page. The part is included verbatim; the wrapper adds only the doctype,
// charset, a title naming the subject, and a policy that blocks all loading.
func previewDocument(subject, htmlPart string) string {
	title := "Preview (not sent)"
	if s := strings.TrimSpace(subject); s != "" {
		title += ": " + s
	}
	return "\ufeff<!doctype html>\n<html><head><meta charset=\"utf-8\">\n" +
		"<meta http-equiv=\"Content-Security-Policy\" content=\"" + draftPreviewCSP + "\">\n" +
		"<title>" + htmlpkg.EscapeString(title) + "</title></head>\n<body>\n" +
		htmlPart + "</body></html>\n"
}

// openDraftPreview opens the draft's HTML part in the browser.
func openDraftPreview(subject, htmlPart, configured string) error {
	return writeAndOpenHTML(previewDocument(subject, htmlPart), configured)
}
