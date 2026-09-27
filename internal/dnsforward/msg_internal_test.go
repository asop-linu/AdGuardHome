package dnsforward

import (
	"testing"
	"time"

	"github.com/miekg/dns"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newTestCacheEntry returns a cache entry holding a single A record for host.
func newTestCacheEntry(host string, ttl uint32) (e blockedHostCacheEntry) {
	return blockedHostCacheEntry{
		expiry: time.Now().Add(time.Duration(ttl) * time.Second),
		answers: []dns.RR{&dns.A{
			Hdr: dns.RR_Header{
				Name:   dns.Fqdn(host),
				Rrtype: dns.TypeA,
				Class:  dns.ClassINET,
				Ttl:    ttl,
			},
			A: []byte{192, 0, 2, 1},
		}},
	}
}

// TestBlockedHostIPCache_copyOnGet ensures that mutating the records returned
// by the cache doesn't corrupt the cached entries, which would otherwise make
// previously sent responses change retroactively.
func TestBlockedHostIPCache_copyOnGet(t *testing.T) {
	const (
		cacheHost = "replacement.example.net"
		otherHost = "other.example.org"
	)

	c := newBlockedHostIPCache()
	c.m[cacheHost] = newTestCacheEntry(cacheHost, 300)

	answers, ok := c.get(cacheHost)
	require.True(t, ok)
	require.Len(t, answers, 1)

	// Mutate the returned records the way genBlockedHost does.
	answers[0].Header().Name = dns.Fqdn(otherHost)

	// The cached entry must be unaffected.
	cached := c.m[cacheHost].answers
	require.Len(t, cached, 1)
	assert.Equal(t, dns.Fqdn(cacheHost), cached[0].Header().Name)
}

// TestBlockedHostIPCache_copyOnSet ensures that the cache stores its own copy
// of the records, so that later mutations of the caller's slice don't leak into
// the cache.
func TestBlockedHostIPCache_copyOnSet(t *testing.T) {
	const host = "replacement.example.net"

	c := newBlockedHostIPCache()

	answers := []dns.RR{&dns.A{
		Hdr: dns.RR_Header{
			Name:   dns.Fqdn(host),
			Rrtype: dns.TypeA,
			Class:  dns.ClassINET,
			Ttl:    300,
		},
		A: []byte{192, 0, 2, 1},
	}}

	c.set(host, answers, 5*time.Minute)

	// Mutate the caller's records after storing them.
	answers[0].Header().Name = dns.Fqdn("mutated.example.org")

	cached, ok := c.get(host)
	require.True(t, ok)
	require.Len(t, cached, 1)
	assert.Equal(t, dns.Fqdn(host), cached[0].Header().Name)
}

// TestBlockedHostIPCache_ttlClamp ensures that the entry TTL is clamped to
// [blockedHostIPCacheTTL] and that non-positive TTLs fall back to it as well.
func TestBlockedHostIPCache_ttlClamp(t *testing.T) {
	const host = "replacement.example.net"

	answers := []dns.RR{&dns.A{
		Hdr: dns.RR_Header{
			Name:   dns.Fqdn(host),
			Rrtype: dns.TypeA,
			Class:  dns.ClassINET,
			Ttl:    1,
		},
		A: []byte{192, 0, 2, 1},
	}}

	t.Run("too_long", func(t *testing.T) {
		c := newBlockedHostIPCache()
		c.set(host, answers, 10*time.Hour)

		got := time.Until(c.m[host].expiry)
		assert.LessOrEqual(t, got, blockedHostIPCacheTTL)
		assert.Positive(t, got)
	})

	t.Run("non_positive", func(t *testing.T) {
		c := newBlockedHostIPCache()
		c.set(host, answers, 0)

		got := time.Until(c.m[host].expiry)
		assert.LessOrEqual(t, got, blockedHostIPCacheTTL)
		assert.Positive(t, got)
	})
}

// TestBlockedHostIPCache_expiry ensures that stale entries are reported as
// misses.
func TestBlockedHostIPCache_expiry(t *testing.T) {
	const host = "replacement.example.net"

	c := newBlockedHostIPCache()
	c.m[host] = blockedHostCacheEntry{
		expiry:  time.Now().Add(-time.Second),
		answers: newTestCacheEntry(host, 300).answers,
	}

	_, ok := c.get(host)
	assert.False(t, ok)
}

// TestBlockedHostIPCache_maxEviction ensures that the cache stays within its
// size limit by evicting all entries once the maximum is reached.
func TestBlockedHostIPCache_maxEviction(t *testing.T) {
	c := newBlockedHostIPCache()

	answer := &dns.A{
		Hdr: dns.RR_Header{
			Name:   dns.Fqdn("replacement.example.net"),
			Rrtype: dns.TypeA,
			Class:  dns.ClassINET,
			Ttl:    300,
		},
		A: []byte{192, 0, 2, 1},
	}

	for i := range blockedHostIPCacheMax {
		host := string(rune('a'+i%26)) + ".example.net"
		c.set(host, []dns.RR{dns.Copy(answer)}, time.Minute)

		assert.LessOrEqual(t, len(c.m), blockedHostIPCacheMax)
	}
}
