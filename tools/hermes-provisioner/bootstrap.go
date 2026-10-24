package main

import (
	"errors"
	"net/url"
	"strings"
)

var errInferenceBootstrap = errors.New("inference bootstrap is missing, invalid, or does not match the profile endpoint")

type inferenceBootstrap struct {
	Key     string `json:"key"`
	BaseURL string `json:"base_url"`
}

func (b inferenceBootstrap) validate() error {
	if b.Key == "" || len(b.Key) > 4096 || strings.IndexFunc(b.Key, func(r rune) bool { return r < 0x21 || r > 0x7e }) >= 0 {
		return errInferenceBootstrap
	}
	u, err := url.Parse(b.BaseURL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || strings.ContainsAny(b.BaseURL, "\r\n\t \\") {
		return errInferenceBootstrap
	}
	return nil
}
