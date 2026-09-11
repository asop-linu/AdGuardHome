// Package rdns processes reverse DNS lookup queries.
package rdns

import (
	"context"
	"log/slog"
	"net/netip"
	"sync"
	"time"

	"github.com/AdguardTeam/golibs/errors"
	"github.com/AdguardTeam/golibs/logutil/slogutil"
	"github.com/bluele/gcache"
)

// Interface processes rDNS queries.
type Interface interface {
	// Process makes rDNS request and returns domain name.  changed indicates
	// that domain name was updated since last request.
	Process(ctx context.Context, ip netip.Addr) (host string, changed bool)
}

// Empty is an empty [Interface] implementation which does nothing.
type Empty struct{}

// type check
var _ Interface = (*Empty)(nil)

// Process implements the [Interface] interface for Empty.
func (Empty) Process(_ context.Context, _ netip.Addr) (host string, changed bool) {
	return "", false
}

// Exchanger is a resolver for clients' addresses.
type Exchanger interface {
	// Exchange tries to resolve the ip in a suitable way, i.e. either as local
	// or as external.
	Exchange(ctx context.Context, ip netip.Addr) (host string, ttl time.Duration, err error)
}

// Config is the configuration structure for Default.
type Config struct {
	// Logger is used for logging the operation of the reverse DNS lookup
	// queries.  It must not be nil.
	Logger *slog.Logger

	// Exchanger resolves IP addresses to domain names.
	Exchanger Exchanger

	// CacheSize is the maximum size of the cache.  It must be greater than
	// zero.
	CacheSize int

	// CacheTTL is the Time to Live duration for cached IP addresses.
	CacheTTL time.Duration
}

// Default is the default rDNS query processor.
type Default struct {
	// logger is used for logging the operation of the reverse DNS lookup
	// queries.  It must not be nil.
	logger *slog.Logger

	// cache is the cache containing IP addresses of clients.  An active IP
	// address is resolved once again after it expires.  If IP address couldn't
	// be resolved, it stays here for some time to prevent further attempts to
	// resolve the same IP.
	cache gcache.Cache

	// exchanger resolves IP addresses to domain names.
	exchanger Exchanger

	// cacheTTL is the Time to Live duration for cached IP addresses.
	cacheTTL time.Duration

	// inflight guards inFlight, the map of unresolved in-progress resolutions
	// keyed by IP address.  It is used to avoid duplicate concurrent upstream
	// queries for the same address.
	inflight *sync.Mutex
	inFlight map[netip.Addr]*inFlightQuery
}

// inFlightQuery tracks a single in-progress rDNS resolution so that concurrent
// callers for the same address can share its result.
type inFlightQuery struct {
	done chan struct{}
	host string
}

// New returns a new default rDNS query processor.  conf must not be nil.
func New(conf *Config) (r *Default) {
	return &Default{
		logger:    conf.Logger,
		cache:     gcache.New(conf.CacheSize).LRU().Build(),
		exchanger: conf.Exchanger,
		cacheTTL:  conf.CacheTTL,
		inflight:  &sync.Mutex{},
		inFlight:  map[netip.Addr]*inFlightQuery{},
	}
}

// type check
var _ Interface = (*Default)(nil)

// Process implements the [Interface] interface for Default.
func (r *Default) Process(ctx context.Context, ip netip.Addr) (host string, changed bool) {
	fromCache, expired := r.findInCache(ctx, ip)
	if !expired {
		return fromCache, false
	}

	// Check whether another goroutine is already resolving this address and
	// share its result if so, to avoid duplicate upstream queries during bursts
	// of requests for the same IP.
	r.inflight.Lock()
	if iq, ok := r.inFlight[ip]; ok && !channelClosed(iq.done) {
		r.inflight.Unlock()

		<-iq.done

		return iq.host, iq.host != fromCache
	}

	iq := &inFlightQuery{done: make(chan struct{})}
	r.inFlight[ip] = iq
	r.inflight.Unlock()

	var err error
	host, ttl, err := r.exchanger.Exchange(ctx, ip)
	if err != nil {
		r.logger.DebugContext(ctx, "resolving", "ip", ip, slogutil.KeyError, err)
	}

	ttl = max(ttl, r.cacheTTL)

	item := &cacheItem{
		expiry: time.Now().Add(ttl),
		host:   host,
	}

	err = r.cache.Set(ip, item)
	if err != nil {
		r.logger.DebugContext(ctx, "adding item to cache", "key", ip, slogutil.KeyError, err)
	}

	iq.host = host
	close(iq.done)

	// The result is now cached, so an in-flight entry is no longer needed.
	// Keep the map bounded.
	r.inflight.Lock()
	delete(r.inFlight, ip)
	r.inflight.Unlock()

	return host, fromCache == "" || host != fromCache
}

// channelClosed reports whether c has already been closed.
func channelClosed(c chan struct{}) (ok bool) {
	select {
	case <-c:
		return true
	default:
		return false
	}
}

// findInCache finds domain name in the cache.  expired is true if host is not
// valid anymore.
func (r *Default) findInCache(ctx context.Context, ip netip.Addr) (host string, expired bool) {
	val, err := r.cache.Get(ip)
	if err != nil {
		if !errors.Is(err, gcache.KeyNotFoundError) {
			r.logger.DebugContext(
				ctx,
				"retrieving item from cache",
				"key", ip,
				slogutil.KeyError, err,
			)
		}

		return "", true
	}

	item := val.(*cacheItem)

	return item.host, time.Now().After(item.expiry)
}

// cacheItem represents an item that we will store in the cache.
type cacheItem struct {
	// expiry is the time when cacheItem will expire.
	expiry time.Time

	// host is the domain name of a runtime client.
	host string
}
