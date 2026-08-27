package httpserver

import "errors"

var ErrTimeout = errors.New("request processing timed out")
