// Package clipimage reads an image from the system clipboard. A terminal paste
// carries text only, so TideMail asks the clipboard itself, through the
// platform's command line tool: wl-paste on Wayland, xclip on X11. Other
// platforms report that image paste is not supported yet. The clipboard is read
// only when asked, never polled.
package clipimage

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	"image/png"
	"os/exec"
	"runtime"
	"strings"

	_ "golang.org/x/image/bmp" // registers decoders: BMP, TIFF and WebP clipboard images are converted to PNG
	_ "golang.org/x/image/tiff"
	_ "golang.org/x/image/webp"
)

// MaxBytes is the largest clipboard image accepted.
const MaxBytes = 20 << 20

// ErrNoImage means the clipboard holds no image.
var ErrNoImage = errors.New("the clipboard has no image")

// Image is a clipboard image ready to attach.
type Image struct {
	Data []byte
	Ext  string // "png", "jpg" or "gif"
}

// Kind says what the clipboard holds.
type Kind struct {
	Image bool // at least one image type is offered
	Text  bool // some text type is offered (plain, HTML, ...)
	Types []string
}

// ImageOnly reports an image and no text: the case where a paste means the image.
func (k Kind) ImageOnly() bool { return k.Image && !k.Text }

// Env is the outside world the package needs, so tests can supply their own.
type Env struct {
	GOOS     string
	Getenv   func(string) string
	LookPath func(string) (string, error)
	// Run runs a command and returns its standard output. Output beyond MaxBytes
	// is an error.
	Run func(ctx context.Context, name string, args ...string) ([]byte, error)
}

// DefaultEnv is the real environment.
func DefaultEnv(getenv func(string) string) Env {
	return Env{GOOS: runtime.GOOS, Getenv: getenv, LookPath: exec.LookPath, Run: runCommand}
}

// preference is the order image types are chosen in when several are offered.
var preference = []string{"image/png", "image/jpeg", "image/gif", "image/webp", "image/bmp", "image/tiff"}

type backend struct {
	list []string
	read func(mime string) []string
	name string
}

// pick chooses the clipboard tool: Wayland first (X11 is always visible under
// XWayland, but then the Wayland clipboard is the right one), then X11.
func (e Env) pick() (string, backend, error) {
	if e.GOOS != "linux" {
		return "", backend{}, fmt.Errorf("image paste is not supported on %s yet", e.GOOS)
	}
	wayland, x11 := e.Getenv("WAYLAND_DISPLAY") != "", e.Getenv("DISPLAY") != ""
	if wayland {
		if _, err := e.LookPath("wl-paste"); err == nil {
			return "wl-paste", backend{
				list: []string{"--list-types"},
				read: func(m string) []string { return []string{"--no-newline", "--type", m} },
				name: "wl-paste",
			}, nil
		}
	}
	if x11 {
		if _, err := e.LookPath("xclip"); err == nil {
			return "xclip", backend{
				list: []string{"-selection", "clipboard", "-t", "TARGETS", "-o"},
				read: func(m string) []string { return []string{"-selection", "clipboard", "-t", m, "-o"} },
				name: "xclip",
			}, nil
		}
	}
	switch {
	case wayland:
		return "", backend{}, errors.New("cannot read the clipboard: install wl-clipboard (wl-paste)")
	case x11:
		return "", backend{}, errors.New("cannot read the clipboard: install xclip")
	}
	return "", backend{}, errors.New("cannot read the clipboard: no Wayland or X11 session is visible to TideMail (under tmux, add WAYLAND_DISPLAY to update-environment)")
}

// Probe reports what the clipboard holds, without reading any of it.
func (e Env) Probe(ctx context.Context) (Kind, error) {
	tool, b, err := e.pick()
	if err != nil {
		return Kind{}, err
	}
	out, err := e.Run(ctx, tool, b.list...)
	if err != nil {
		// An empty clipboard makes wl-paste exit non-zero; that is "nothing", not a failure.
		return Kind{}, nil
	}
	return classify(out), nil
}

func classify(listing []byte) Kind {
	var k Kind
	for _, line := range strings.Split(string(listing), "\n") {
		t := strings.TrimSpace(line)
		if t == "" {
			continue
		}
		k.Types = append(k.Types, t)
		lower := strings.ToLower(t)
		switch {
		case strings.HasPrefix(lower, "image/"):
			k.Image = true
		case strings.HasPrefix(lower, "text/plain"), strings.HasPrefix(lower, "text/html"),
			t == "UTF8_STRING", t == "STRING", t == "TEXT", t == "COMPOUND_TEXT":
			k.Text = true
		}
	}
	return k
}

// chooseType picks the image type to read from those offered.
func chooseType(types []string) (string, error) {
	have := map[string]string{}
	other := ""
	for _, t := range types {
		lower := strings.ToLower(t)
		if strings.HasPrefix(lower, "image/") {
			have[lower] = t
			if other == "" {
				other = t
			}
		}
	}
	for _, want := range preference {
		if t, ok := have[want]; ok {
			return t, nil
		}
	}
	if other != "" {
		return "", fmt.Errorf("the clipboard image is %s, which is not supported", other)
	}
	return "", ErrNoImage
}

// Read returns the clipboard image, converting types other than PNG, JPEG and
// GIF to PNG. It returns ErrNoImage when there is none.
func (e Env) Read(ctx context.Context) (Image, error) {
	tool, b, err := e.pick()
	if err != nil {
		return Image{}, err
	}
	listing, err := e.Run(ctx, tool, b.list...)
	if err != nil {
		return Image{}, ErrNoImage
	}
	mime, err := chooseType(classify(listing).Types)
	if err != nil {
		return Image{}, err
	}
	data, err := e.Run(ctx, tool, b.read(mime)...)
	if err != nil {
		return Image{}, fmt.Errorf("cannot read the clipboard image: %w", err)
	}
	if len(data) == 0 {
		return Image{}, errors.New("the clipboard image is empty")
	}
	if len(data) > MaxBytes {
		return Image{}, fmt.Errorf("the clipboard image is %d MB; the limit is %d MB", len(data)>>20, MaxBytes>>20)
	}
	return normalize(data, strings.ToLower(mime))
}

// normalize checks that data decodes and returns it, as PNG when its type is not
// one the message can carry untouched.
func normalize(data []byte, mime string) (Image, error) {
	switch mime {
	case "image/png", "image/jpeg", "image/gif":
		cfg, format, err := image.DecodeConfig(bytes.NewReader(data))
		if err != nil || cfg.Width <= 0 || cfg.Height <= 0 {
			return Image{}, errors.New("the clipboard data is not a valid image")
		}
		ext := map[string]string{"png": "png", "jpeg": "jpg", "gif": "gif"}[format]
		if ext == "" {
			return Image{}, fmt.Errorf("the clipboard image is %s, which is not supported", format)
		}
		return Image{Data: data, Ext: ext}, nil
	}
	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return Image{}, fmt.Errorf("cannot decode the %s clipboard image: %v", mime, err)
	}
	var out bytes.Buffer
	if err := png.Encode(&out, img); err != nil {
		return Image{}, err
	}
	if out.Len() > MaxBytes {
		return Image{}, fmt.Errorf("the clipboard image is %d MB as PNG; the limit is %d MB", out.Len()>>20, MaxBytes>>20)
	}
	return Image{Data: out.Bytes(), Ext: "png"}, nil
}

// runCommand runs a command, returning its standard output (bounded by MaxBytes)
// or an error that includes what it said on standard error.
func runCommand(ctx context.Context, name string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &limitedWriter{buf: &stdout, left: MaxBytes + 1}
	cmd.Stderr = &limitedWriter{buf: &stderr, left: 4096}
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return nil, fmt.Errorf("%s timed out", name)
		}
		if msg := strings.TrimSpace(stderr.String()); msg != "" {
			return nil, fmt.Errorf("%s: %s", name, msg)
		}
		return nil, fmt.Errorf("%s: %w", name, err)
	}
	return stdout.Bytes(), nil
}

// limitedWriter keeps the first left bytes and quietly drops the rest, so a huge
// clipboard cannot fill memory; the caller sees the length and refuses it.
type limitedWriter struct {
	buf  *bytes.Buffer
	left int
}

func (w *limitedWriter) Write(p []byte) (int, error) {
	if w.left > 0 {
		n := min(len(p), w.left)
		w.buf.Write(p[:n])
		w.left -= n
	}
	return len(p), nil
}
