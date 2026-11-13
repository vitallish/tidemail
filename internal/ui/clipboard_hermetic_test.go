package ui

import (
	"context"
	"errors"
	"os/exec"

	"github.com/allisonhere/tidemail/internal/clipimage"
)

// No test may read the real system clipboard: it would make results depend on
// whatever the developer last copied. By default the clipboard cannot be
// inspected; tests that need one install a fake with useFakeClipboard.
func init() {
	clipEnv = func() clipimage.Env {
		return clipimage.Env{
			GOOS:     "linux",
			Getenv:   func(string) string { return "" },
			LookPath: func(string) (string, error) { return "", exec.ErrNotFound },
			Run: func(context.Context, string, ...string) ([]byte, error) {
				return nil, errors.New("tests have no clipboard")
			},
		}
	}
}
