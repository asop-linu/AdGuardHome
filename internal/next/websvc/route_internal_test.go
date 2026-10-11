package websvc

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/AdguardTeam/golibs/logutil/slogutil"
	"github.com/AdguardTeam/golibs/netutil/httputil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestIsValidRoutePattern(t *testing.T) {
	testCases := []struct {
		name    string
		pattern string
		want    bool
	}{{
		name:    "get",
		pattern: "GET /dns-query",
		want:    true,
	}, {
		name:    "post",
		pattern: "POST /dns-query",
		want:    true,
	}, {
		name:    "no_method",
		pattern: "/dns-query",
		want:    true,
	}, {
		name:    "wildcard",
		pattern: "GET /dns-query/{ClientID}",
		want:    true,
	}, {
		name:    "root",
		pattern: "/",
		want:    true,
	}, {
		name:    "empty",
		pattern: "",
		want:    false,
	}, {
		name:    "method_only",
		pattern: http.MethodGet,
		want:    false,
	}, {
		name:    "lower_case_method",
		pattern: "get /dns-query",
		want:    false,
	}, {
		name:    "invalid_method",
		pattern: "BOGUS /dns-query",
		want:    false,
	}, {
		name:    "relative_path",
		pattern: "dns-query",
		want:    false,
	}, {
		name:    "path_with_space",
		pattern: "GET /dns query",
		want:    false,
	}, {
		name:    "double_space",
		pattern: "GET  /dns-query",
		want:    false,
	}}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, isValidRoutePattern(tc.pattern))
		})
	}
}

func TestService_routeDoH(t *testing.T) {
	const wantBody = "doh"

	newMux := func(t *testing.T, doh *DoHConfig) (mux *http.ServeMux) {
		t.Helper()

		svc := &Service{
			logger: slogutil.NewDiscardLogger(),
			doh:    doh,
		}

		mux = http.NewServeMux()
		svc.routeDoH(mux, httputil.NewLogMiddleware(svc.logger, 0))

		return mux
	}

	hdlr := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, err := w.Write([]byte(wantBody))
		assert.NoError(t, err)
	})

	testCases := []struct {
		doh      *DoHConfig
		name     string
		method   string
		url      string
		wantBody string
		wantCode int
	}{{
		name:     "default_routes",
		doh:      &DoHConfig{Handler: hdlr},
		method:   http.MethodPost,
		url:      "/dns-query",
		wantCode: http.StatusOK,
		wantBody: wantBody,
	}, {
		name:     "default_routes_method_mismatch",
		doh:      &DoHConfig{Handler: hdlr},
		method:   http.MethodPut,
		url:      "/dns-query",
		wantCode: http.StatusMethodNotAllowed,
	}, {
		name: "custom_route",
		doh: &DoHConfig{
			Routes:  []string{"GET /custom-dns-query"},
			Handler: hdlr,
		},
		method:   http.MethodGet,
		url:      "/custom-dns-query",
		wantCode: http.StatusOK,
		wantBody: wantBody,
	}, {
		name: "invalid_routes_are_skipped",
		doh: &DoHConfig{
			Routes:  []string{"BOGUS /bad", "GET /good"},
			Handler: hdlr,
		},
		method:   http.MethodGet,
		url:      "/good",
		wantCode: http.StatusOK,
		wantBody: wantBody,
	}, {
		name: "duplicate_routes_are_skipped",
		doh: &DoHConfig{
			Routes:  []string{"GET /dup", "GET /dup"},
			Handler: hdlr,
		},
		method:   http.MethodGet,
		url:      "/dup",
		wantCode: http.StatusOK,
		wantBody: wantBody,
	}, {
		name:     "no_handler",
		doh:      &DoHConfig{Routes: []string{"GET /dns-query"}},
		method:   http.MethodGet,
		url:      "/dns-query",
		wantCode: http.StatusNotFound,
	}, {
		name:     "no_config",
		doh:      nil,
		method:   http.MethodGet,
		url:      "/dns-query",
		wantCode: http.StatusNotFound,
	}}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			mux := newMux(t, tc.doh)

			r := httptest.NewRequest(tc.method, tc.url, http.NoBody)
			w := httptest.NewRecorder()
			mux.ServeHTTP(w, r)

			assert.Equal(t, tc.wantCode, w.Code)

			if tc.wantBody != "" {
				assert.Equal(t, tc.wantBody, w.Body.String())
			}
		})
	}
}

// TestService_routeDoH_noPanic makes sure that a malformed configuration does
// not make [http.ServeMux.Handle] panic.
func TestService_routeDoH_noPanic(t *testing.T) {
	routes := []string{
		"",
		" ",
		http.MethodGet,
		"GET /",
		"GET /dns-query",
		"POST /dns-query",
		"dns-query",
		"BOGUS /x",
		"GET /a b",
		"GET /x",
		"GET /x",
		"/x",
	}

	svc := &Service{
		logger: slogutil.NewDiscardLogger(),
		doh: &DoHConfig{
			Routes: routes,
			Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusOK)
			}),
		},
	}

	require.NotPanics(t, func() {
		svc.routeDoH(http.NewServeMux(), httputil.NewLogMiddleware(svc.logger, 0))
	})
}
