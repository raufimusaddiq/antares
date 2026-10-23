package store

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"
)

// Measures the cost of one search against a user at the index quota. It builds a large database, so it
// runs only on request: ANTARES_SEARCH_MEASURE=1 go test ./internal/store -run TestMeasureSearchAtQuota -v
// The numbers in docs/runbooks/search.md come from this test.
func TestMeasureSearchAtQuota(t *testing.T) {
	if os.Getenv("ANTARES_SEARCH_MEASURE") == "" {
		t.Skip("set ANTARES_SEARCH_MEASURE=1 to run")
	}
	// Unbudgeted extremes (what the turn cap alone allowed) and the two corners of today's budget.
	for _, sc := range []struct{ docs, runes int }{{MaxIndexedUserDocs, 500}, {MaxIndexedUserDocs, MaxIndexedText}, {MaxIndexedUserDocs, MaxIndexedUserRunes / MaxIndexedUserDocs}, {MaxIndexedUserRunes / MaxIndexedText, MaxIndexedText}} {
		runes := sc.runes
		d, u := setupTwo(t)
		ctx := context.Background()
		const chats = 100
		per := sc.docs / chats
		for c := 0; c < chats; c++ {
			ch, err := CreateChat(ctx, d.DB, u.a, u.bindA, fmt.Sprintf("sess-m-%d", c), "", t0.Add(time.Duration(c)*time.Second))
			if err != nil {
				t.Fatal(err)
			}
			// random lower-case hex text of the requested length; one turn in 1000 contains "rareneedle"
			if _, err := d.DB.Exec(`INSERT INTO search_docs (user_id, chat_id, seq, role, body, folded)
				WITH RECURSIVE n(i) AS (SELECT 0 UNION ALL SELECT i+1 FROM n WHERE i < ?),
				t(i, b) AS (SELECT i, lower(hex(randomblob(?))) || CASE WHEN i % 1000 = 0 THEN ' rareneedle' ELSE '' END FROM n)
				SELECT ?, ?, i, 'user', b, b FROM t`, per-1, runes/2, u.a, ch.ID); err != nil {
				t.Fatal(err)
			}
		}
		var size int64
		_ = d.DB.QueryRow(`SELECT page_count * page_size FROM pragma_page_count, pragma_page_size`).Scan(&size)
		for _, c := range []struct {
			name  string
			terms []string
		}{{"miss (scans everything)", []string{"zzzzqqqq"}}, {"rare hit", []string{"rareneedle"}}, {"two terms, miss", []string{"zzzzqqqq", "yyyyxxxx"}}} {
			best := time.Hour
			for i := 0; i < 3; i++ {
				start := time.Now()
				if _, _, err := SearchChats(ctx, d.DB, SearchQuery{UserID: u.a, Terms: c.terms, Limit: 20}); err != nil {
					t.Fatal(err)
				}
				best = min(best, time.Since(start))
			}
			t.Logf("%6d docs x %4d runes (db %4d MB): %-24s best of 3 = %v", chats*per, runes, size>>20, c.name, best)
		}
	}
}
