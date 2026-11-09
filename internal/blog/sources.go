package blog

import (
	"net"
	"net/netip"
	"net/url"
	"strings"
	"time"
)

// A source is an address the assistant says it used. It is shown to the author and, in a block's text, to readers, so it
// has to be one a reader's browser can safely be sent to: https, no login in it, a public host. Antares never fetches it.

// PublicHTTPS reports whether s is an https address with a public host: no user or password, no port other than 443,
// not localhost, not a name that cannot resolve publicly, and not an IP address in a private, loopback, link-local,
// unspecified or shared range. (Names are not resolved here, so a name that points at a private address is not caught:
// the browser, not Antares, follows the link.)
func PublicHTTPS(s string) bool {
	if len(s) == 0 || len(s) > 2048 || strings.TrimSpace(s) != s {
		return false
	}
	u, err := url.Parse(s)
	if err != nil || u.Scheme != "https" || u.User != nil || u.Host == "" {
		return false
	}
	if p := u.Port(); p != "" && p != "443" {
		return false
	}
	return publicHost(u.Hostname())
}

func publicHost(h string) bool {
	h = strings.ToLower(strings.TrimSuffix(h, "."))
	if h == "" || h == "localhost" || strings.HasSuffix(h, ".localhost") || strings.HasSuffix(h, ".local") ||
		strings.HasSuffix(h, ".internal") || strings.HasSuffix(h, ".lan") || strings.HasSuffix(h, ".home") || strings.HasSuffix(h, ".invalid") {
		return false
	}
	if ip, err := netip.ParseAddr(h); err == nil {
		return publicIP(ip)
	}
	if net.ParseIP(h) != nil { // an odd spelling of an address (zone, mapped form)
		return false
	}
	// A bare name (no dot) cannot be a public site; an all-digit last label is an address spelled strangely (2130706433).
	i := strings.LastIndex(h, ".")
	if i < 0 {
		return false
	}
	tld := h[i+1:]
	if tld == "" || strings.Trim(tld, "0123456789") == "" {
		return false
	}
	return true
}

func publicIP(ip netip.Addr) bool {
	ip = ip.Unmap()
	if !ip.IsValid() || ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() ||
		ip.IsMulticast() || ip.IsUnspecified() || ip.IsInterfaceLocalMulticast() {
		return false
	}
	if ip.Is4() {
		b := ip.As4()
		if b[0] == 100 && b[1]&0xC0 == 64 { // 100.64.0.0/10, shared address space
			return false
		}
		if b[0] == 0 || b[0] >= 240 { // "this network", reserved and broadcast
			return false
		}
	}
	return true
}

// Source is one cited address of a block.
type Source struct {
	URL         string `json:"url"`
	Title       string `json:"title"`
	RetrievedAt string `json:"retrievedAt"`
	// Verified is never set by this code: the run's events do not expose what a search returned (research R4), so there is
	// nothing to check an address against. It stays in the model for a Hermes that does.
	Verified bool `json:"verified"`
}

const maxSourceTitle = 200

// cleanSource returns the source as it will be kept, or false if it cannot be: not a public https address, no title
// worth showing, or a retrieval date that is not a date.
func cleanSource(s Source, today time.Time) (Source, bool) {
	if !PublicHTTPS(s.URL) {
		return Source{}, false
	}
	// Refused, never trimmed or repaired: a title that does not fit is not the title the assistant meant.
	title := collapseSpace(s.Title)
	if title == "" || len([]rune(title)) > maxSourceTitle || !isPlainText(title) {
		return Source{}, false
	}
	d, err := time.Parse("2006-01-02", s.RetrievedAt)
	if err != nil || d.After(today.AddDate(0, 0, 1)) || d.Before(today.AddDate(-5, 0, 0)) {
		return Source{}, false
	}
	return Source{URL: s.URL, Title: title, RetrievedAt: s.RetrievedAt}, true
}

func collapseSpace(s string) string { return strings.Join(strings.Fields(s), " ") }
