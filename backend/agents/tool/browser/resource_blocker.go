package browser

import (
	"strings"
)

// BlockableResourceType enumerates resource types that can be blocked.
// Source: Scrapling constants.py EXTRA_RESOURCES
var BlockableResourceTypes = map[string]bool{
	"font":       true,
	"image":      true,
	"media":      true,
	"beacon":     true,
	"object":     true,
	"imageset":   true,
	"texttrack":  true,
	"websocket":  true,
	"csp_report": true,
	"stylesheet": true,
}

// adDomains is a compact set of popular ad/tracker domains.
// Source: Scrapling toolbelt/ad_domains.py (representative subset)
var adDomains = map[string]bool{
	"doubleclick.net":            true,
	"googlesyndication.com":      true,
	"googleadservices.com":       true,
	"google-analytics.com":       true,
	"googletagmanager.com":       true,
	"googletagservices.com":      true,
	"adservice.google.com":       true,
	"pagead2.googlesyndication.com": true,
	"facebook.net":               true,
	"facebook.com":               true,
	"fbcdn.net":                  true,
	"connect.facebook.net":       true,
	"analytics.twitter.com":      true,
	"ads-twitter.com":            true,
	"static.ads-twitter.com":     true,
	"amazon-adsystem.com":        true,
	"advertising.amazon.com":     true,
	"ads.yahoo.com":              true,
	"analytics.yahoo.com":        true,
	"adnxs.com":                  true,
	"adsrvr.org":                 true,
	"bidswitch.net":              true,
	"casalemedia.com":            true,
	"criteo.com":                 true,
	"criteo.net":                 true,
	"demdex.net":                 true,
	"exelator.com":               true,
	"eyeota.net":                 true,
	"media.net":                  true,
	"moatads.com":                true,
	"openx.net":                  true,
	"outbrain.com":               true,
	"pubmatic.com":               true,
	"rubiconproject.com":         true,
	"scorecardresearch.com":      true,
	"sharethrough.com":           true,
	"smartadserver.com":          true,
	"taboola.com":                true,
	"tapad.com":                  true,
	"tradedoubler.com":           true,
	"turn.com":                   true,
	"quantserve.com":             true,
	"serving-sys.com":            true,
	"smaato.net":                 true,
	"contextweb.com":             true,
	"undertone.com":              true,
	"yieldmanager.com":           true,
	"33across.com":               true,
	"mathtag.com":                true,
	"simpli.fi":                  true,
	"advertising.com":            true,
	"nexac.com":                  true,
	"bluekai.com":                true,
	"ml314.com":                  true,
	"hotjar.com":                 true,
	"hotjar.io":                  true,
	"mixpanel.com":               true,
	"amplitude.com":              true,
	"segment.com":                true,
	"segment.io":                 true,
	"newrelic.com":               true,
	"nr-data.net":                true,
	"sentry.io":                  true,
	"bugsnag.com":                true,
	"fullstory.com":              true,
	"mouseflow.com":              true,
	"clarity.ms":                 true,
	"crazyegg.com":               true,
	"optimizely.com":             true,
	"adobedtm.com":               true,
	"omtrdc.net":                 true,
}

// ResourceBlocker decides whether to block a request based on resource type and domain.
type ResourceBlocker struct {
	blockResources bool
	blockedDomains map[string]bool // exact + parent domain matching
	blockAds       bool
}

// NewResourceBlocker creates a ResourceBlocker.
// blockResources: block common resource types (fonts, images, media, etc.)
// blockedDomains: additional custom domains to block
// blockAds: block known ad/tracker domains
func NewResourceBlocker(blockResources bool, blockedDomains []string, blockAds bool) *ResourceBlocker {
	dm := make(map[string]bool, len(blockedDomains))
	for _, d := range blockedDomains {
		dm[strings.ToLower(strings.TrimSpace(d))] = true
	}
	return &ResourceBlocker{
		blockResources: blockResources,
		blockedDomains: dm,
		blockAds:       blockAds,
	}
}

// ShouldBlock returns true if the request with the given resource type and URL should be blocked.
// Source: Scrapling toolbelt/navigation.py:_is_domain_blocked + create_intercept_handler
func (rb *ResourceBlocker) ShouldBlock(resourceType, requestURL string) bool {
	if rb == nil {
		return false
	}

	// Block by resource type
	if rb.blockResources && BlockableResourceTypes[strings.ToLower(resourceType)] {
		return true
	}

	// Extract domain from URL
	domain := extractDomain(requestURL)
	if domain == "" {
		return false
	}
	domain = strings.ToLower(domain)

	// Block by custom domains (with subdomain matching)
	if isDomainBlocked(domain, rb.blockedDomains) {
		return true
	}

	// Block by ad domains
	if rb.blockAds && isDomainBlocked(domain, adDomains) {
		return true
	}

	return false
}

// isDomainBlocked checks if domain or any parent domain is in the blocked set.
// Source: Scrapling toolbelt/navigation.py:_is_domain_blocked
func isDomainBlocked(domain string, blocked map[string]bool) bool {
	if blocked[domain] {
		return true
	}
	// Check parent domains: sub.example.com → example.com
	parts := strings.Split(domain, ".")
	for i := 1; i < len(parts)-1; i++ {
		parent := strings.Join(parts[i:], ".")
		if blocked[parent] {
			return true
		}
	}
	return false
}

// extractDomain extracts the hostname from a URL string.
func extractDomain(rawURL string) string {
	// Quick parse: find ://host/
	idx := strings.Index(rawURL, "://")
	if idx < 0 {
		return ""
	}
	rest := rawURL[idx+3:]
	// Remove port and path
	if i := strings.IndexAny(rest, ":/"); i >= 0 {
		rest = rest[:i]
	}
	return rest
}
