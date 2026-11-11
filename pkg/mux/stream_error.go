package mux

import (
	"errors"
	"io"

	"github.com/hashicorp/yamux"
)

// IsClosedStream identifies rejection/closure of one stream, which does not
// prove that a tunnel or its logical client session has failed. In particular,
// admission limits close individual new streams while existing streams live on.
func IsClosedStream(err error) bool {
	return errors.Is(err, yamux.ErrStreamClosed) || errors.Is(err, yamux.ErrConnectionReset) || errors.Is(err, io.EOF) || errors.Is(err, io.ErrClosedPipe)
}
