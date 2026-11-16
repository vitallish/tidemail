package clipimage

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/color"
	"image/gif"
	"image/jpeg"
	"image/png"
	"os/exec"
	"strings"
	"testing"

	"golang.org/x/image/bmp"
	"golang.org/x/image/tiff"
)

// clip is a fake clipboard behind a fake tool.
type clip struct {
	types []string
	data  map[string][]byte // mime -> bytes
	fail  map[string]error  // mime -> read error ("" key: the listing)
	calls [][]string
}

func (c *clip) run(_ context.Context, name string, args ...string) ([]byte, error) {
	c.calls = append(c.calls, append([]string{name}, args...))
	joined := strings.Join(args, " ")
	switch {
	case joined == "--list-types" || strings.Contains(joined, "TARGETS"):
		if err := c.fail[""]; err != nil {
			return nil, err
		}
		return []byte(strings.Join(c.types, "\n") + "\n"), nil
	}
	var mime string
	if name == "wl-paste" {
		mime = args[2] // --no-newline --type <mime>
	} else { // xclip -selection clipboard -t <mime> -o
		mime = args[3]
	}
	if err := c.fail[mime]; err != nil {
		return nil, err
	}
	return c.data[mime], nil
}

func env(c *clip, goos string, vars map[string]string, tools ...string) Env {
	have := map[string]bool{}
	for _, t := range tools {
		have[t] = true
	}
	return Env{
		GOOS:   goos,
		Getenv: func(k string) string { return vars[k] },
		LookPath: func(n string) (string, error) {
			if have[n] {
				return "/usr/bin/" + n, nil
			}
			return "", exec.ErrNotFound
		},
		Run: c.run,
	}
}

func pngData(t *testing.T) []byte {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, 30, 20))
	img.Set(5, 5, color.NRGBA{255, 0, 0, 255})
	var b bytes.Buffer
	if err := png.Encode(&b, img); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func encode(t *testing.T, kind string) []byte {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, 30, 20))
	var b bytes.Buffer
	var err error
	switch kind {
	case "jpeg":
		err = jpeg.Encode(&b, img, nil)
	case "gif":
		err = gif.Encode(&b, img, nil)
	case "bmp":
		err = bmp.Encode(&b, img)
	case "tiff":
		err = tiff.Encode(&b, img, nil)
	}
	if err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

var wayland = map[string]string{"WAYLAND_DISPLAY": "wayland-1", "DISPLAY": ":0"}

func TestProbeSeparatesImageOnlyFromImageWithText(t *testing.T) {
	for name, tc := range map[string]struct {
		types     []string
		img, text bool
	}{
		"screenshot":        {[]string{"image/png"}, true, false},
		"web page image":    {[]string{"text/html", "text/plain", "image/png"}, true, true},
		"text only":         {[]string{"text/plain;charset=utf-8", "UTF8_STRING"}, false, true},
		"html only":         {[]string{"text/html"}, false, true},
		"x11 text targets":  {[]string{"TARGETS", "UTF8_STRING", "STRING", "TEXT"}, false, true},
		"x11 image targets": {[]string{"TARGETS", "image/png"}, true, false},
		"file manager copy": {[]string{"text/uri-list", "x-special/gnome-copied-files"}, false, false},
		"empty":             {nil, false, false},
	} {
		c := &clip{types: tc.types}
		k, err := env(c, "linux", wayland, "wl-paste").Probe(context.Background())
		if err != nil || k.Image != tc.img || k.Text != tc.text {
			t.Errorf("%s: %+v err=%v, want image=%v text=%v", name, k, err, tc.img, tc.text)
		}
		if k.ImageOnly() != (tc.img && !tc.text) {
			t.Errorf("%s: ImageOnly = %v", name, k.ImageOnly())
		}
	}
}

func TestReadWaylandUsesWlPasteAndKeepsPNGUntouched(t *testing.T) {
	p := pngData(t)
	c := &clip{types: []string{"image/png"}, data: map[string][]byte{"image/png": p}}
	img, err := env(c, "linux", wayland, "wl-paste", "xclip").Read(context.Background())
	if err != nil || img.Ext != "png" || !bytes.Equal(img.Data, p) {
		t.Fatalf("%+v %v", img, err)
	}
	last := c.calls[len(c.calls)-1]
	if strings.Join(last, " ") != "wl-paste --no-newline --type image/png" {
		t.Fatalf("command: %v", last)
	}
}

func TestReadX11UsesXclipWhenThereIsNoWayland(t *testing.T) {
	p := encode(t, "jpeg")
	c := &clip{types: []string{"TARGETS", "image/jpeg"}, data: map[string][]byte{"image/jpeg": p}}
	img, err := env(c, "linux", map[string]string{"DISPLAY": ":0"}, "xclip").Read(context.Background())
	if err != nil || img.Ext != "jpg" || !bytes.Equal(img.Data, p) {
		t.Fatalf("%+v %v", img, err)
	}
	if got := strings.Join(c.calls[len(c.calls)-1], " "); got != "xclip -selection clipboard -t image/jpeg -o" {
		t.Fatalf("command: %q", got)
	}
}

func TestWaylandIsPreferredOverXWayland(t *testing.T) {
	c := &clip{types: []string{"image/png"}, data: map[string][]byte{"image/png": pngData(t)}}
	if _, err := env(c, "linux", wayland, "wl-paste", "xclip").Read(context.Background()); err != nil {
		t.Fatal(err)
	}
	for _, call := range c.calls {
		if call[0] != "wl-paste" {
			t.Fatalf("used %s although Wayland was available", call[0])
		}
	}
}

func TestPreferredTypeIsChosenAmongSeveral(t *testing.T) {
	c := &clip{
		types: []string{"image/bmp", "image/jpeg", "image/png", "image/tiff"},
		data:  map[string][]byte{"image/png": pngData(t), "image/jpeg": encode(t, "jpeg"), "image/bmp": encode(t, "bmp")},
	}
	img, err := env(c, "linux", wayland, "wl-paste").Read(context.Background())
	if err != nil || img.Ext != "png" {
		t.Fatalf("%+v %v", img, err)
	}
	if got := strings.Join(c.calls[len(c.calls)-1], " "); !strings.HasSuffix(got, "image/png") {
		t.Fatalf("read %q, want the PNG", got)
	}
}

func TestBMPAndTIFFAreConvertedToPNG(t *testing.T) {
	for _, kind := range []string{"bmp", "tiff"} {
		mime := "image/" + kind
		c := &clip{types: []string{mime}, data: map[string][]byte{mime: encode(t, kind)}}
		img, err := env(c, "linux", wayland, "wl-paste").Read(context.Background())
		if err != nil || img.Ext != "png" {
			t.Fatalf("%s: %+v %v", kind, img, err)
		}
		cfg, format, err := image.DecodeConfig(bytes.NewReader(img.Data))
		if err != nil || format != "png" || cfg.Width != 30 || cfg.Height != 20 {
			t.Fatalf("%s: converted to %s %v %v", kind, format, cfg, err)
		}
	}
}

func TestGIFKeepsItsTypeAndBytes(t *testing.T) {
	g := encode(t, "gif")
	c := &clip{types: []string{"image/gif"}, data: map[string][]byte{"image/gif": g}}
	img, err := env(c, "linux", wayland, "wl-paste").Read(context.Background())
	if err != nil || img.Ext != "gif" || !bytes.Equal(img.Data, g) {
		t.Fatalf("%+v %v", img, err)
	}
}

func TestReadFailuresSayWhy(t *testing.T) {
	good := pngData(t)
	cases := map[string]struct {
		c     *clip
		e     func(*clip) Env
		want  string
		isNil error
	}{
		"no image":         {&clip{types: []string{"text/plain"}}, func(c *clip) Env { return env(c, "linux", wayland, "wl-paste") }, "no image", ErrNoImage},
		"empty clipboard":  {&clip{fail: map[string]error{"": errors.New("Nothing is copied")}}, func(c *clip) Env { return env(c, "linux", wayland, "wl-paste") }, "no image", ErrNoImage},
		"unsupported type": {&clip{types: []string{"image/x-icon"}}, func(c *clip) Env { return env(c, "linux", wayland, "wl-paste") }, "image/x-icon", nil},
		"not an image":     {&clip{types: []string{"image/png"}, data: map[string][]byte{"image/png": []byte("just text")}}, func(c *clip) Env { return env(c, "linux", wayland, "wl-paste") }, "not a valid image", nil},
		"empty data":       {&clip{types: []string{"image/png"}, data: map[string][]byte{"image/png": {}}}, func(c *clip) Env { return env(c, "linux", wayland, "wl-paste") }, "empty", nil},
		"read error":       {&clip{types: []string{"image/png"}, fail: map[string]error{"image/png": errors.New("wl-paste: boom")}}, func(c *clip) Env { return env(c, "linux", wayland, "wl-paste") }, "boom", nil},
		"too large":        {&clip{types: []string{"image/png"}, data: map[string][]byte{"image/png": append(append([]byte{}, good...), make([]byte, MaxBytes)...)}}, func(c *clip) Env { return env(c, "linux", wayland, "wl-paste") }, "limit", nil},
		"no wl-paste":      {&clip{}, func(c *clip) Env { return env(c, "linux", wayland) }, "wl-clipboard", nil},
		"no xclip":         {&clip{}, func(c *clip) Env { return env(c, "linux", map[string]string{"DISPLAY": ":0"}) }, "xclip", nil},
		"no session":       {&clip{}, func(c *clip) Env { return env(c, "linux", nil, "wl-paste", "xclip") }, "update-environment", nil},
		"macos":            {&clip{}, func(c *clip) Env { return env(c, "darwin", wayland, "wl-paste") }, "not supported on darwin", nil},
		"windows":          {&clip{}, func(c *clip) Env { return env(c, "windows", nil) }, "not supported on windows", nil},
	}
	for name, tc := range cases {
		_, err := tc.e(tc.c).Read(context.Background())
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: err = %v, want it to contain %q", name, err, tc.want)
		}
		if tc.isNil != nil && !errors.Is(err, tc.isNil) {
			t.Errorf("%s: err = %v, want %v", name, err, tc.isNil)
		}
	}
}

func TestProbeWithoutATool(t *testing.T) {
	if _, err := env(&clip{}, "linux", nil).Probe(context.Background()); err == nil {
		t.Fatal("a missing clipboard tool must be reported, so the caller can fall back to text paste")
	}
}

func TestRealRunnerReportsStderrAndTimeouts(t *testing.T) {
	if _, err := runCommand(context.Background(), "sh", "-c", "echo oops >&2; exit 3"); err == nil || !strings.Contains(err.Error(), "oops") {
		t.Fatalf("err = %v", err)
	}
	out, err := runCommand(context.Background(), "sh", "-c", "printf abc")
	if err != nil || string(out) != "abc" {
		t.Fatalf("%q %v", out, err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 50_000_000)
	defer cancel()
	if _, err := runCommand(ctx, "sh", "-c", "sleep 5"); err == nil || !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("a hung tool must time out: %v", err)
	}
}

func TestLimitedWriterBoundsMemory(t *testing.T) {
	var b bytes.Buffer
	w := &limitedWriter{buf: &b, left: 5}
	if n, err := w.Write([]byte("0123456789")); n != 10 || err != nil || b.String() != "01234" {
		t.Fatalf("n=%d err=%v kept=%q", n, err, b.String())
	}
}
