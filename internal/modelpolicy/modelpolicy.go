// Package modelpolicy decides whether a model an administrator chose as a default can be used right now, and what a purpose
// runs when it cannot (specs/004-admin-full-control). One place, so the admin screen's warning and the run that really happens
// never disagree.
package modelpolicy

import (
	"context"
	"database/sql"
	"slices"
	"time"

	"github.com/raufimusaddiq/antares/internal/kenari"
	"github.com/raufimusaddiq/antares/internal/store"
)

// Catalogue is the Kenari model list.
type Catalogue interface {
	List(ctx context.Context) ([]kenari.Model, error)
}

// Why a saved default cannot be used.
const (
	OK        = ""
	NotListed = "not_listed" // Kenari no longer lists it for this purpose
	Sunset    = "sunset"
	Off       = "off" // an administrator switched it off
)

// ChatUsable is true for a model Kenari lists for chat that has not been sunset.
func ChatUsable(m kenari.Model, now time.Time) bool { return m.Chat() && !pastSunset(m, now) }

func pastSunset(m kenari.Model, now time.Time) bool {
	t, err := time.Parse(time.RFC3339, m.SunsetAt)
	return err == nil && !t.After(now)
}

// Reason says why model cannot be the default for a chat-endpoint purpose (chat, summary, blog assistant): "" when it can.
func Reason(cat []kenari.Model, blocked []string, model string, now time.Time) string {
	if slices.Contains(blocked, model) {
		return Off
	}
	i := slices.IndexFunc(cat, func(m kenari.Model) bool { return m.ID == model })
	switch {
	case i < 0 || !cat[i].Chat():
		return NotListed
	case pastSunset(cat[i], now):
		return Sunset
	}
	return OK
}

// Default returns the model saved as the default for purpose when it can be used, else "". An unreadable catalogue keeps a
// default that is not switched off (it cannot be proven gone); a switched-off one is never returned.
func Default(ctx context.Context, db *sql.DB, cat Catalogue, purpose string, now time.Time) string {
	model, err := store.ModelDefault(ctx, db, purpose)
	if err != nil || model == "" {
		return ""
	}
	blocked, err := store.BlockedModels(ctx, db)
	if err != nil {
		return ""
	}
	if slices.Contains(blocked, model) {
		return ""
	}
	if cat == nil {
		return model
	}
	models, err := cat.List(ctx)
	if err != nil {
		return model
	}
	if Reason(models, blocked, model, now) != OK {
		return ""
	}
	return model
}

// Pick is the model a helper call (titles, summaries) should use: the saved default if usable, else the configured one; and
// false when that one is switched off, so no call is made.
func Pick(ctx context.Context, db *sql.DB, cat Catalogue, purpose, configured string, now time.Time) (string, bool) {
	if m := Default(ctx, db, cat, purpose, now); m != "" {
		return m, true
	}
	blocked, err := store.BlockedModels(ctx, db)
	if err != nil || slices.Contains(blocked, configured) {
		return "", false
	}
	return configured, true
}
