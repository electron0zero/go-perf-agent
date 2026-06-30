// Package helper holds the small cross-cutting helpers shared across the engine - running external
// commands, a couple of filesystem checks, and defaulting a nil progress logger.
package helper

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Run executes name in dir (empty = cwd) and returns stdout, stderr, error.
func Run(dir, name string, args ...string) (stdout, stderr string, err error) {
	return RunCtx(context.Background(), dir, name, args...)
}

// RunCtx is Run bounded by ctx, so a stuck process is killed when ctx times out or is cancelled.
func RunCtx(ctx context.Context, dir, name string, args ...string) (stdout, stderr string, err error) {
	cmd := exec.CommandContext(ctx, name, args...)
	if dir != "" {
		cmd.Dir = dir
	}
	var out, errb strings.Builder
	cmd.Stdout = &out
	cmd.Stderr = &errb
	err = cmd.Run()
	return out.String(), errb.String(), err
}

// Exists reports whether path p exists.
func Exists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

// MustAbs returns the absolute form of p, or p unchanged if it cannot be resolved.
func MustAbs(p string) string {
	if a, err := filepath.Abs(p); err == nil {
		return a
	}
	return p
}

// OrNoop returns logf, or a no-op logger when it is nil, so callers can accept an optional progress
// logger without nil-checking at every use.
func OrNoop(logf func(string, ...any)) func(string, ...any) {
	if logf == nil {
		return func(string, ...any) {}
	}
	return logf
}
