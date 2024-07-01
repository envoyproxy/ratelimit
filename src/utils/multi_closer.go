package utils

import (
	"errors"
	"io"
)

type MultiCloser struct {
	Closers []io.Closer
}

func (m *MultiCloser) Close() error {
	var e error
	for _, closer := range m.Closers {
		e = errors.Join(closer.Close())
	}
	return e
}
