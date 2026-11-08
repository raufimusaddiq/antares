package blog

import "regexp"

// A post's address: lowercase letters, digits and single hyphens, 3-80 characters, neither starting nor ending with a
// hyphen, and not a word the app uses for its own routes.
var slugRE = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9]|-[a-z0-9]){2,79}$`)

var reservedSlugs = map[string]bool{
	"admin": true, "api": true, "assets": true, "blog": true, "edit": true, "index": true, "login": true, "logout": true,
	"new": true, "pending": true, "publish": true, "rejected": true, "settings": true, "static": true, "sw": true,
}

// ValidSlug reports whether s may name a published post.
func ValidSlug(s string) bool { return slugRE.MatchString(s) && !reservedSlugs[s] }

// A public address is matched against this before any query runs; anything else is the same 404 as an unknown post.
func wellFormedSlug(s string) bool { return len(s) <= 80 && slugRE.MatchString(s) }
