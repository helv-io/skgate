package httputil

import (
	"errors"
	"net"
	"net/http"
	"time"
)

// HeaderReadTimeout is how long a client has to send its request headers (http.Server.ReadHeaderTimeout).
const HeaderReadTimeout = 15 * time.Second

// SmallBodyTimeout is how long a client has to send the body of a small request (a registration, a token request,
// an admin form): four times the header time. A variable so a test can shorten it.
var SmallBodyTimeout = 4 * HeaderReadTimeout

// ReadDeadline gives the body of a request a deadline of SmallBodyTimeout, for endpoints that take a few
// kilobytes. It sets the deadline on the connection's read side only, so a response that takes longer is not
// affected, and it is not used on anything that uploads or streams (/v1, /mcp, /sse, /messages). Without it a
// client can hold a connection open by sending a body one byte at a time.
func ReadDeadline(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		LimitBodyTime(w)
		next.ServeHTTP(w, r)
	})
}

// LimitBodyTime sets the SmallBodyTimeout read deadline for the current request. A writer that cannot
// (a test recorder, an HTTP/2 stream without support) is left as it is.
func LimitBodyTime(w http.ResponseWriter) {
	_ = http.NewResponseController(w).SetReadDeadline(time.Now().Add(SmallBodyTimeout))
}

// IsTimeout reports whether err is a network timeout, such as the read deadline passing.
func IsTimeout(err error) bool {
	var ne net.Error
	return errors.As(err, &ne) && ne.Timeout()
}
