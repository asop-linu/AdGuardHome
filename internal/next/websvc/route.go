package websvc

import (
	"log/slog"
	"net/http"
	"slices"
	"strings"

	"github.com/AdguardTeam/golibs/netutil/httputil"
)

// Path pattern constants.
const (
	PathPatternDoHQuery       = "/dns-query"
	PathPatternFrontend       = "/"
	PathPatternHealthCheck    = "/health-check"
	PathPatternV1SettingsAll  = "/api/v1/settings/all"
	PathPatternV1SettingsDNS  = "/api/v1/settings/dns"
	PathPatternV1SettingsHTTP = "/api/v1/settings/http"
	PathPatternV1SystemInfo   = "/api/v1/system/info"
)

// Route pattern constants.
const (
	routePatternFrontend            = http.MethodGet + " " + PathPatternFrontend
	routePatternGetV1SettingsAll    = http.MethodGet + " " + PathPatternV1SettingsAll
	routePatternGetV1SystemInfo     = http.MethodGet + " " + PathPatternV1SystemInfo
	routePatternHealthCheck         = http.MethodGet + " " + PathPatternHealthCheck
	routePatternPatchV1SettingsDNS  = http.MethodPatch + " " + PathPatternV1SettingsDNS
	routePatternPatchV1SettingsHTTP = http.MethodPatch + " " + PathPatternV1SettingsHTTP
)

// route registers all necessary handlers in mux.
func (svc *Service) route(mux *http.ServeMux) {
	routes := []struct {
		handler http.Handler
		pattern string
		isJSON  bool
	}{{
		handler: httputil.HealthCheckHandler,
		pattern: routePatternHealthCheck,
		isJSON:  false,
	}, {
		handler: http.FileServer(http.FS(svc.frontend)),
		pattern: routePatternFrontend,
		isJSON:  false,
	}, {
		handler: http.HandlerFunc(svc.handleGetSettingsAll),
		pattern: routePatternGetV1SettingsAll,
		isJSON:  true,
	}, {
		handler: http.HandlerFunc(svc.handlePatchSettingsDNS),
		pattern: routePatternPatchV1SettingsDNS,
		isJSON:  true,
	}, {
		handler: http.HandlerFunc(svc.handlePatchSettingsHTTP),
		pattern: routePatternPatchV1SettingsHTTP,
		isJSON:  true,
	}, {
		handler: http.HandlerFunc(svc.handleGetV1SystemInfo),
		pattern: routePatternGetV1SystemInfo,
		isJSON:  true,
	}}

	logMw := httputil.NewLogMiddleware(svc.logger, slog.LevelDebug)
	for _, r := range routes {
		var hdlr http.Handler
		if r.isJSON {
			hdlr = jsonMw(r.handler)
		} else {
			hdlr = r.handler
		}

		mux.Handle(r.pattern, logMw.Wrap(hdlr))
	}

	svc.routeDoH(mux, logMw)
}

// routeDoH registers the DNS-over-HTTPS handlers in mux.  A nil *Service, as
// well as a configuration without DoH, is valid and registers nothing.
func (svc *Service) routeDoH(mux *http.ServeMux, logMw *httputil.LogMiddleware) {
	if svc == nil || svc.doh == nil || svc.doh.Handler == nil {
		return
	}

	routes := svc.doh.Routes
	if len(routes) == 0 {
		routes = DefaultDoHRoutes
	}

	// Prevent panics from [http.ServeMux.Handle] on malformed or duplicated
	// user-provided routes.
	seen := make(map[string]struct{}, len(routes))
	for _, r := range routes {
		svc.registerDoHRoute(mux, logMw, seen, r)
	}
}

// registerDoHRoute validates the route pattern r and registers it in mux,
// skipping invalid or duplicated patterns with an error log entry.  It
// recovers from panics of [http.ServeMux.Handle] on conflicting patterns.
func (svc *Service) registerDoHRoute(
	mux *http.ServeMux,
	logMw *httputil.LogMiddleware,
	seen map[string]struct{},
	r string,
) {
	if !isValidRoutePattern(r) {
		svc.logger.Error("invalid doh route", "route", r)

		return
	}

	if _, ok := seen[r]; ok {
		svc.logger.Error("duplicate doh route", "route", r)

		return
	}

	seen[r] = struct{}{}

	func() {
		defer func() {
			if p := recover(); p != nil {
				svc.logger.Error("failed to register doh route", "route", r, "err", p)
			}
		}()

		mux.Handle(r, logMw.Wrap(svc.doh.Handler))
	}()
}

// httpMethods is the set of methods that [http.ServeMux] accepts within a route
// pattern.
var httpMethods = []string{
	http.MethodConnect,
	http.MethodDelete,
	http.MethodGet,
	http.MethodHead,
	http.MethodOptions,
	http.MethodPatch,
	http.MethodPost,
	http.MethodPut,
	http.MethodTrace,
}

// isValidRoutePattern returns true if p is a valid [http.ServeMux] pattern in
// the "[METHOD ]/path" form that is used for the DoH routes.  It is used to
// validate the user-provided configuration before registering it, since
// [http.ServeMux.Handle] panics on invalid patterns.
//
// TODO(a.garipov):  Support host and wildcard segments once the configuration
// allows them.
func isValidRoutePattern(p string) (ok bool) {
	method, path, hasMethod := strings.Cut(p, " ")
	if !hasMethod {
		// A pattern without a method matches all methods.
		return isValidRoutePath(p)
	}

	if path == "" || !slices.Contains(httpMethods, method) {
		return false
	}

	return isValidRoutePath(path)
}

// isValidRoutePath returns true if p is a valid [http.ServeMux] path pattern,
// i.e. an absolute path without any whitespace in it.
func isValidRoutePath(p string) (ok bool) {
	if !strings.HasPrefix(p, "/") {
		return false
	}

	return !strings.ContainsAny(p, " \t\r\n")
}
