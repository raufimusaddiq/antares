package modelpolicy

import (
	"testing"
	"time"

	"github.com/raufimusaddiq/antares/internal/kenari"
)

func TestUnusableSunsetFailsClosed(t *testing.T) {
	now := time.Date(2026, 11, 20, 0, 0, 0, 0, time.UTC)
	tests := []struct {
		name string
		m    kenari.Model
		want string
	}{
		{"no sunset", kenari.Model{ID: "a", Endpoints: []string{"chat"}}, OK},
		{"future sunset", kenari.Model{ID: "a", Endpoints: []string{"chat"}, SunsetAt: "2030-01-01T00:00:00Z"}, OK},
		{"past sunset", kenari.Model{ID: "a", Endpoints: []string{"chat"}, SunsetAt: "2020-01-01T00:00:00Z"}, Sunset},
		{"unparsable sunset", kenari.Model{ID: "a", Endpoints: []string{"chat"}, SunsetAt: "soon"}, Sunset},
		{"flagged unusable sunset", kenari.Model{ID: "a", Endpoints: []string{"chat"}, SunsetInvalid: true}, Sunset},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := Reason([]kenari.Model{tc.m}, nil, "a", now); got != tc.want {
				t.Fatalf("Reason = %q, want %q", got, tc.want)
			}
			if got := ChatUsable(tc.m, now); got != (tc.want == OK) {
				t.Fatalf("ChatUsable = %v", got)
			}
		})
	}
}
