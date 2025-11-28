// (c) Siemens AG 2025
//
// SPDX-License-Identifier: MIT

package testslog

import (
	"io"
	"log/slog"

	"github.com/thediveo/morbyd/safe"
)

// SetDefault creates and sets a new default slog.Logger, returning a
// concurrent-safe buffer which receives the structured log textual output. The
// logger is configured with the passed level. If an optional, non-nil io.Writer
// is passed, then the logger will duplicate its logging output additionally to
// this io.Writer.
func SetDefault(level slog.Leveler, optw io.Writer) *safe.Buffer {
	b := &safe.Buffer{}
	w := io.Writer(b)
	if optw != nil {
		w = io.MultiWriter(b, optw)
	}
	slog.SetDefault(slog.New(slog.NewTextHandler(w, &slog.HandlerOptions{
		Level: level,
	})))
	return b
}
