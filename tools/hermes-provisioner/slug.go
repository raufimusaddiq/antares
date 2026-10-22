package main

import (
	"errors"
	"regexp"
)

var slugRE = regexp.MustCompile(`^u_[a-z0-9]{8,32}$`)

var (
	errDefaultProfile = errors.New("the default profile cannot be managed through this service")
	errBadSlug        = errors.New("slug must match ^u_[a-z0-9]{8,32}$")
)

// validateSlug is the single gate between request input and a path or argv
// element. It refuses; it never sanitises.
func validateSlug(s string) error {
	if s == "default" {
		return errDefaultProfile
	}
	if !slugRE.MatchString(s) {
		return errBadSlug
	}
	return nil
}
