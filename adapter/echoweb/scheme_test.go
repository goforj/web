package echoweb

import (
	"net"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/goforj/web"
	"github.com/goforj/web/webmiddleware"
	"github.com/labstack/echo/v5"
)

// TestSchemeProxyTrust verifies that the adapter preserves Echo's trusted-peer boundary.
func TestSchemeProxyTrust(t *testing.T) {
	_, proxyRange, err := net.ParseCIDR("198.51.100.0/24")
	if err != nil {
		t.Fatal(err)
	}
	for _, tt := range []struct {
		name, peer, target, forwarded, want string
		explicit                            bool
	}{
		{name: "untrusted public peer", peer: "198.51.100.10:1234", target: "http://example.com/", forwarded: "https", want: "http"},
		{name: "trusted private peer", peer: "10.0.0.1:1234", target: "http://example.com/", forwarded: "https", want: "https"},
		{name: "direct TLS", peer: "198.51.100.10:1234", target: "https://example.com/", forwarded: "http", want: "https"},
		{name: "explicit public proxy", peer: "198.51.100.10:1234", target: "http://example.com/", forwarded: "https", want: "https", explicit: true},
		{name: "outside explicit range", peer: "203.0.113.10:1234", target: "http://example.com/", forwarded: "https", want: "http", explicit: true},
		{name: "nearest proxy value", peer: "10.0.0.1:1234", target: "http://example.com/", forwarded: "http, HTTPS", want: "https"},
		{name: "invalid forwarded scheme", peer: "10.0.0.1:1234", target: "http://example.com/", forwarded: "invalid", want: "http"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			adapter := New()
			if tt.explicit {
				adapter.Echo().SchemeExtractor = echo.ExtractSchemeFromHeaders(echo.TrustIPRange(proxyRange))
			}
			adapter.Router().GET("/", func(c web.Context) error { return c.Text(http.StatusOK, c.Scheme()) })
			request := httptest.NewRequest(http.MethodGet, tt.target, nil)
			request.RemoteAddr = tt.peer
			request.Header.Set(echo.HeaderXForwardedProto, tt.forwarded)
			request.Header.Set(echo.HeaderXForwardedSsl, "on")
			response := httptest.NewRecorder()
			adapter.ServeHTTP(response, request)
			if response.Code != http.StatusOK || response.Body.String() != tt.want {
				t.Fatalf("response = (%d, %q), want (%d, %q)", response.Code, response.Body.String(), http.StatusOK, tt.want)
			}
		})
	}
}

// TestHTTPSRedirectRejectsSpoofedScheme verifies that a public client cannot bypass HTTPS enforcement.
func TestHTTPSRedirectRejectsSpoofedScheme(t *testing.T) {
	adapter := New()
	adapter.Router().Pre(webmiddleware.HTTPSRedirect())
	adapter.Router().GET("/", func(c web.Context) error { return c.NoContent(http.StatusNoContent) })
	request := httptest.NewRequest(http.MethodGet, "http://example.com/", nil)
	request.RemoteAddr = "198.51.100.10:1234"
	request.Header.Set(echo.HeaderXForwardedProto, "https")
	response := httptest.NewRecorder()
	adapter.ServeHTTP(response, request)
	if response.Code != http.StatusMovedPermanently || response.Header().Get("Location") != "https://example.com/" {
		t.Fatalf("response = (%d, %q), want HTTPS redirect", response.Code, response.Header().Get("Location"))
	}
}
