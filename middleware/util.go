package middleware

import (
	"errors"
	"net/http"

	"github.com/goforj/web"
)

type nativeRequester interface {
	Request() *http.Request
}

func nativeRequest(r web.Context) (*http.Request, bool) {
	native := r.Native()
	if native == nil {
		return nil, false
	}
	requester, ok := native.(nativeRequester)
	if !ok || requester.Request() == nil {
		return nil, false
	}
	return requester.Request(), true
}

func invalidConfigError(message string) error {
	return errors.New("web: " + message)
}
