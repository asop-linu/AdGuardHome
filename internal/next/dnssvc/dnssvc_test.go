package dnssvc_test

import (
	"bytes"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"testing"
	"time"

	"github.com/AdguardTeam/golibs/httphdr"

	"github.com/AdguardTeam/AdGuardHome/internal/next/dnssvc"
	"github.com/AdguardTeam/golibs/logutil/slogutil"
	"github.com/AdguardTeam/golibs/testutil"
	"github.com/asop-linu/dnsproxy/proxy"
	"github.com/miekg/dns"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// testTimeout is the common timeout for tests.
const testTimeout = 1 * time.Second

func TestService(t *testing.T) {
	const (
		listenAddr    = "127.0.0.1:0"
		bootstrapAddr = "127.0.0.1:0"
		upstreamAddr  = "upstream.example"
	)

	pt := testutil.NewPanicT(t)
	upstreamErrCh := make(chan error, 1)
	upstreamStartedCh := make(chan struct{})
	upstreamSrv := &dns.Server{
		Addr: bootstrapAddr,
		Net:  "udp",
		Handler: dns.HandlerFunc(func(w dns.ResponseWriter, req *dns.Msg) {
			resp := (&dns.Msg{}).SetReply(req)
			resp.Answer = append(resp.Answer, &dns.A{
				Hdr: dns.RR_Header{},
				A:   netip.MustParseAddrPort(bootstrapAddr).Addr().AsSlice(),
			})

			writeErr := w.WriteMsg(resp)
			require.NoError(pt, writeErr)
		}),
		NotifyStartedFunc: func() { close(upstreamStartedCh) },
	}

	go func() {
		listenErr := upstreamSrv.ListenAndServe()
		if listenErr != nil {
			// Log these immediately to see what happens.
			t.Logf("upstream listen error: %s", listenErr)
		}

		upstreamErrCh <- listenErr
	}()

	_, _ = testutil.RequireReceive(t, upstreamStartedCh, testTimeout)

	c := &dnssvc.Config{
		Logger:           slogutil.NewDiscardLogger(),
		UpstreamMode:     proxy.UpstreamModeParallel,
		Addresses:        []netip.AddrPort{netip.MustParseAddrPort(listenAddr)},
		BootstrapServers: []string{upstreamSrv.PacketConn.LocalAddr().String()},
		UpstreamServers:  []string{upstreamAddr},
		UpstreamTimeout:  testTimeout,
	}

	svc, err := dnssvc.New(c)
	require.NoError(t, err)

	err = svc.Start(testutil.ContextWithTimeout(t, testTimeout))
	require.NoError(t, err)

	gotConf := svc.Config()
	require.NotNil(t, gotConf)
	require.Len(t, gotConf.Addresses, 1)

	addr := gotConf.Addresses[0]

	t.Run("dns", func(t *testing.T) {
		req := &dns.Msg{
			MsgHdr: dns.MsgHdr{
				Id:               dns.Id(),
				RecursionDesired: true,
			},
			Question: []dns.Question{{
				Name:   "example.com.",
				Qtype:  dns.TypeA,
				Qclass: dns.ClassINET,
			}},
		}

		cli := &dns.Client{}
		ctx := testutil.ContextWithTimeout(t, testTimeout)

		var resp *dns.Msg
		require.Eventually(t, func() (ok bool) {
			var excErr error
			resp, _, excErr = cli.ExchangeContext(ctx, req, addr.String())

			return excErr == nil
		}, testTimeout, testTimeout/10)

		assert.NotNil(t, resp)
	})

	err = svc.Shutdown(testutil.ContextWithTimeout(t, testTimeout))

	require.NoError(t, err)

	err = upstreamSrv.Shutdown()
	require.NoError(t, err)

	err, ok := testutil.RequireReceive(t, upstreamErrCh, testTimeout)
	require.True(t, ok)
	require.NoError(t, err)
}

// TestServiceConfigRoundTrip makes sure that [dnssvc.Service.Config] returns
// every field that the HTTP API can change.  The configuration manager
// persists that configuration back to disk, so a field that is missing from the
// returned value is silently reset to its zero value in the configuration file
// as soon as any setting is updated.
func TestServiceConfigRoundTrip(t *testing.T) {
	const (
		listenAddr    = "127.0.0.1:0"
		bootstrapAddr = "127.0.0.1:0"
		upstreamAddr  = "upstream.example"

		ratelimit  = 100
		cacheSize  = 4096
		upstreamTO = testTimeout
	)

	pt := testutil.NewPanicT(t)
	upstreamErrCh := make(chan error, 1)
	upstreamStartedCh := make(chan struct{})
	upstreamSrv := &dns.Server{
		Addr: bootstrapAddr,
		Net:  "udp",
		Handler: dns.HandlerFunc(func(w dns.ResponseWriter, req *dns.Msg) {
			resp := (&dns.Msg{}).SetReply(req)

			writeErr := w.WriteMsg(resp)
			require.NoError(pt, writeErr)
		}),
		NotifyStartedFunc: func() { close(upstreamStartedCh) },
	}

	go func() {
		listenErr := upstreamSrv.ListenAndServe()
		if listenErr != nil {
			t.Logf("upstream listen error: %s", listenErr)
		}

		upstreamErrCh <- listenErr
	}()

	_, _ = testutil.RequireReceive(t, upstreamStartedCh, testTimeout)

	c := &dnssvc.Config{
		Logger:              slogutil.NewDiscardLogger(),
		UpstreamMode:        proxy.UpstreamModeParallel,
		Addresses:           []netip.AddrPort{netip.MustParseAddrPort(listenAddr)},
		BootstrapServers:    []string{upstreamSrv.PacketConn.LocalAddr().String()},
		UpstreamServers:     []string{upstreamAddr},
		DNS64Prefixes:       []netip.Prefix{netip.MustParsePrefix("64:ff9b::/96")},
		UpstreamTimeout:     upstreamTO,
		Ratelimit:           ratelimit,
		CacheSize:           cacheSize,
		CacheEnabled:        true,
		BootstrapPreferIPv6: true,
		DNSSECEnabled:       true,
		RefuseAny:           true,
		UseDNS64:            true,
	}

	svc, err := dnssvc.New(c)
	require.NoError(t, err)

	t.Run("config", func(t *testing.T) {
		got := svc.Config()
		require.NotNil(t, got)

		assert.Equal(t, c.UpstreamMode, got.UpstreamMode)
		assert.Equal(t, c.BootstrapServers, got.BootstrapServers)
		assert.Equal(t, c.UpstreamServers, got.UpstreamServers)
		assert.Equal(t, c.DNS64Prefixes, got.DNS64Prefixes)
		assert.Equal(t, c.UpstreamTimeout, got.UpstreamTimeout)
		assert.Equal(t, c.Ratelimit, got.Ratelimit)
		assert.Equal(t, c.CacheSize, got.CacheSize)
		assert.Equal(t, c.BootstrapPreferIPv6, got.BootstrapPreferIPv6)
		assert.Equal(t, c.CacheEnabled, got.CacheEnabled)
		assert.Equal(t, c.DNSSECEnabled, got.DNSSECEnabled)
		assert.Equal(t, c.RefuseAny, got.RefuseAny)
		assert.Equal(t, c.UseDNS64, got.UseDNS64)
	})

	err = svc.Shutdown(testutil.ContextWithTimeout(t, testTimeout))
	require.NoError(t, err)

	err = upstreamSrv.Shutdown()
	require.NoError(t, err)

	err, ok := testutil.RequireReceive(t, upstreamErrCh, testTimeout)
	require.True(t, ok)
	require.NoError(t, err)
}

func TestServiceDoHHandler(t *testing.T) {
	const (
		listenAddr    = "127.0.0.1:0"
		bootstrapAddr = "127.0.0.1:0"
		upstreamAddr  = "upstream.example"

		wantIP = "1.2.3.4"
	)

	pt := testutil.NewPanicT(t)
	upstreamErrCh := make(chan error, 1)
	upstreamStartedCh := make(chan struct{})
	upstreamSrv := &dns.Server{
		Addr: bootstrapAddr,
		Net:  "udp",
		Handler: dns.HandlerFunc(func(w dns.ResponseWriter, req *dns.Msg) {
			resp := (&dns.Msg{}).SetReply(req)
			resp.Answer = append(resp.Answer, &dns.A{
				Hdr: dns.RR_Header{Name: req.Question[0].Name, Rrtype: dns.TypeA},
				A:   net.ParseIP(wantIP),
			})

			writeErr := w.WriteMsg(resp)
			require.NoError(pt, writeErr)
		}),
		NotifyStartedFunc: func() { close(upstreamStartedCh) },
	}

	go func() {
		listenErr := upstreamSrv.ListenAndServe()
		if listenErr != nil {
			t.Logf("upstream listen error: %s", listenErr)
		}

		upstreamErrCh <- listenErr
	}()

	_, _ = testutil.RequireReceive(t, upstreamStartedCh, testTimeout)

	newSvc := func(t *testing.T, insecure bool) (svc *dnssvc.Service) {
		t.Helper()

		svc, err := dnssvc.New(&dnssvc.Config{
			Logger:             slogutil.NewDiscardLogger(),
			UpstreamMode:       proxy.UpstreamModeParallel,
			Addresses:          []netip.AddrPort{netip.MustParseAddrPort(listenAddr)},
			UpstreamServers:    []string{upstreamSrv.PacketConn.LocalAddr().String()},
			UpstreamTimeout:    testTimeout,
			DoHRoutes:          []string{"GET /dns-query", "POST /dns-query"},
			DoHInsecureEnabled: insecure,
		})
		require.NoError(t, err)

		// The proxy only processes the requests after it has been started.
		err = svc.Start(testutil.ContextWithTimeout(t, testTimeout))
		require.NoError(t, err)

		t.Cleanup(func() {
			shutdownErr := svc.Shutdown(testutil.ContextWithTimeout(t, testTimeout))
			assert.NoError(testutil.NewPanicT(t), shutdownErr)
		})

		return svc
	}

	// queryDoH sends a DoH query over plain HTTP.
	queryDoH := func(t *testing.T, h http.Handler) (w *httptest.ResponseRecorder) {
		t.Helper()

		require.NotNil(t, h)

		q := (&dns.Msg{}).SetQuestion(dns.Fqdn("example.org."), dns.TypeA)
		q.RecursionDesired = true

		b, packErr := q.Pack()
		require.NoError(t, packErr)

		req := httptest.NewRequest(http.MethodPost, "http://example.com/dns-query", bytes.NewReader(b))
		req.Header.Set(httphdr.ContentType, "application/dns-message")

		w = httptest.NewRecorder()
		h.ServeHTTP(w, req)

		return w
	}

	t.Run("insecure_enabled", func(t *testing.T) {
		svc := newSvc(t, true)

		w := queryDoH(t, svc.DoHHandler())
		require.Equal(t, http.StatusOK, w.Code)

		resp := parseDoHBody(t, w)
		require.Len(t, resp.Answer, 1)

		a, ok := resp.Answer[0].(*dns.A)
		require.True(t, ok)
		assert.Equal(t, wantIP, a.A.String())
	})

	t.Run("insecure_disabled", func(t *testing.T) {
		svc := newSvc(t, false)

		// The proxy must reject the unencrypted request itself.
		w := queryDoH(t, svc.DoHHandler())
		assert.NotEqual(t, http.StatusOK, w.Code)
	})

	t.Run("nil_service", func(t *testing.T) {
		var svc *dnssvc.Service
		assert.Nil(t, svc.DoHHandler())
	})

	err := upstreamSrv.Shutdown()
	require.NoError(t, err)

	err, ok := testutil.RequireReceive(t, upstreamErrCh, testTimeout)
	require.True(t, ok)
	require.NoError(t, err)
}

// parseDoHBody parses a DNS message from a DoH response body.
func parseDoHBody(t *testing.T, w *httptest.ResponseRecorder) (resp *dns.Msg) {
	t.Helper()

	resp = &dns.Msg{}
	err := resp.Unpack(w.Body.Bytes())
	require.NoError(t, err)

	return resp
}
