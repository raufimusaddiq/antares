package store

import (
	"context"
	"strings"
	"testing"
	"time"
)

func terms(s string) []string { return strings.Fields(Fold(s)) }

func mustIndex(t *testing.T, d *twoUsersDB, user, chat string, docs []IndexDoc) {
	t.Helper()
	if ok, err := ReplaceChatIndex(context.Background(), d.DB, user, chat, docs, t0); err != nil || !ok {
		t.Fatalf("index: %v %v", ok, err)
	}
}

func hitSeqs(h []SearchHit) []int {
	var out []int
	for _, x := range h {
		out = append(out, x.Seq)
	}
	return out
}

// UXP-003 / security-invariants §4: B's text is in the database and never reaches A,
// by any query, filter or guessed chat id.
func TestSearchNeverReturnsAnotherUsersText(t *testing.T) {
	d, u := setupTwo(t)
	ctx := context.Background()
	mustIndex(t, d, u.a, u.chatA.ID, []IndexDoc{{Seq: 0, Role: "user", Text: "the shared word plus apple"}})
	mustIndex(t, d, u.b, u.chatB.ID, []IndexDoc{{Seq: 0, Role: "user", Text: "the shared word plus SECRETBANANA"}, {Seq: 1, Role: "assistant", Text: "banana reply"}})

	var n int
	if err := d.DB.QueryRow(`SELECT COUNT(*) FROM search_docs WHERE body LIKE '%SECRETBANANA%'`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("B's text must be present in the database for this test to mean anything: %d %v", n, err)
	}
	for _, q := range []SearchQuery{
		{UserID: u.a, Terms: terms("secretbanana"), Limit: 10},
		{UserID: u.a, Terms: terms("banana"), Limit: 10},
		{UserID: u.a, Terms: terms("secretbanana"), ChatID: u.chatB.ID, Limit: 10}, // guessed chat id
		{UserID: u.a, Terms: terms("banana"), ChatID: u.chatB.ID, Role: "assistant", Limit: 10},
		{UserID: u.a, Terms: terms("B's"), Limit: 10}, // B's title
	} {
		hits, more, err := SearchChats(ctx, d.DB, q)
		if err != nil || len(hits) != 0 || more {
			t.Errorf("A saw B's data for %+v: %+v %v", q.Terms, hits, err)
		}
	}
	hits, _, _ := SearchChats(ctx, d.DB, SearchQuery{UserID: u.a, Terms: terms("shared word"), Limit: 10})
	if len(hits) != 1 || hits[0].ChatID != u.chatA.ID {
		t.Errorf("A's own hit: %+v", hits)
	}
	hits, _, _ = SearchChats(ctx, d.DB, SearchQuery{UserID: u.b, Terms: terms("shared word"), Limit: 10})
	if len(hits) != 1 || hits[0].ChatID != u.chatB.ID {
		t.Errorf("B's own hit: %+v", hits)
	}
}

func TestIndexWritesAreOwnerScoped(t *testing.T) {
	d, u := setupTwo(t)
	ctx := context.Background()
	// B cannot index into, invalidate or purge A's chat.
	if _, err := ReplaceChatIndex(ctx, d.DB, u.b, u.chatA.ID, []IndexDoc{{Seq: 0, Role: "user", Text: "planted"}}, t0); err != ErrNoChat {
		t.Errorf("B indexed A's chat: %v", err)
	}
	mustIndex(t, d, u.a, u.chatA.ID, []IndexDoc{{Seq: 0, Role: "user", Text: "keep me"}})
	_ = InvalidateChatIndex(ctx, d.DB, u.b, u.chatA.ID, t0.Add(time.Second))
	_ = PurgeChatIndex(ctx, d.DB, u.b, u.chatA.ID)
	if hits, _, _ := SearchChats(ctx, d.DB, SearchQuery{UserID: u.a, Terms: terms("keep me"), Limit: 5}); len(hits) != 1 {
		t.Errorf("A's index was changed by B: %+v", hits)
	}
	if tot, pen, par, _ := IndexStatus(ctx, d.DB, u.a, ""); tot != 1 || pen != 0 || par != 0 {
		t.Errorf("A's status after B's invalidate: %d/%d/%d", tot, pen, par)
	}
	// The database itself refuses a doc whose owner differs from its chat's.
	if _, err := d.DB.Exec(`INSERT INTO search_docs (user_id, chat_id, seq, role, body, folded) VALUES (?,?,5,'user','x','x')`, u.b, u.chatA.ID); err == nil {
		t.Error("a search row with the wrong owner was accepted")
	}
}

// The trap from the admin list: SQLite LOWER() folds ASCII only. These fail on a
// LOWER()/LIKE implementation and on one that folds only one side.
func TestSearchFoldsNonASCIIOnBothSides(t *testing.T) {
	d, u := setupTwo(t)
	ctx := context.Background()
	mustIndex(t, d, u.a, u.chatA.ID, []IndexDoc{
		{Seq: 0, Role: "user", Text: "Hubungi Ünal tentang ÇİĞDEM dan Straße"},
		{Seq: 1, Role: "assistant", Text: "ünal sudah dihubungi; Ömer Ångström ΣΊΣΥΦΟΣ"},
	})
	for _, q := range []string{"Ünal", "ünal", "ÜNAL", "çiğdem", "ÇİĞDEM", "ömer", "ÖMER", "ångström", "ÅNGSTRÖM", "σίσυφοσ", "ΣΊΣΥΦΟΣ", "straße"} {
		hits, _, err := SearchChats(ctx, d.DB, SearchQuery{UserID: u.a, Terms: terms(q), Limit: 10})
		if err != nil || len(hits) == 0 {
			t.Errorf("%q: no hit (%v)", q, err)
		}
	}
	hits, _, _ := SearchChats(ctx, d.DB, SearchQuery{UserID: u.a, Terms: terms("ÜNAL"), Limit: 10})
	if got := hitSeqs(hits); len(got) != 2 || got[0] != 1 || got[1] != 0 {
		t.Errorf("ÜNAL hits (newest turn first): %v", got)
	}
	// Folding is not accent-stripping: 'unal' is a different word from 'ünal'.
	if hits, _, _ := SearchChats(ctx, d.DB, SearchQuery{UserID: u.a, Terms: terms("unal"), Limit: 10}); len(hits) != 0 {
		t.Errorf("unal matched ünal: %+v", hits)
	}
	// The naive version, so the checks above are known to discriminate: LOWER() leaves 'Ü'
	// alone, so a Go-folded query never meets it.
	var naive int
	if err := d.DB.QueryRow(`SELECT COUNT(*) FROM search_docs WHERE seq = 0 AND LOWER(body) LIKE '%' || ? || '%'`, Fold("ÜNAL")).Scan(&naive); err != nil || naive != 0 {
		t.Errorf("premise changed: SQLite LOWER() now folds non-ASCII (%d rows, %v)", naive, err)
	}
}

func TestSearchQueryIsDataNotPattern(t *testing.T) {
	d, u := setupTwo(t)
	mustIndex(t, d, u.a, u.chatA.ID, []IndexDoc{{Seq: 0, Role: "user", Text: "100% sure about a_b and it's fine"}})
	for _, q := range []string{"%", "_", "100%", "a_b", "it's", `'; DROP TABLE search_docs; --`} {
		hits, _, err := SearchChats(context.Background(), d.DB, SearchQuery{UserID: u.a, Terms: terms(q), Limit: 5})
		if err != nil {
			t.Fatalf("%q: %v", q, err)
		}
		want := q == "%" || q == "100%" || q == "a_b" || q == "it's" || q == "_"
		if (len(hits) > 0) != want {
			t.Errorf("%q: %d hits", q, len(hits))
		}
	}
}

func TestSearchTitleFiltersAndKeysetPaging(t *testing.T) {
	d, u := setupTwo(t)
	ctx := context.Background()
	if hits, _, _ := SearchChats(ctx, d.DB, SearchQuery{UserID: u.a, Terms: terms("a's chat"), Limit: 5}); len(hits) != 1 || hits[0].Seq != TitleSeq || hits[0].Role != RoleTitle {
		t.Fatalf("a chat is searchable by title as soon as it exists: %+v", hits)
	}
	var docs []IndexDoc
	for i := 0; i < 7; i++ {
		role := "user"
		if i%2 == 1 {
			role = "assistant"
		}
		docs = append(docs, IndexDoc{Seq: i, Role: role, Text: "needle number"})
	}
	mustIndex(t, d, u.a, u.chatA.ID, docs)
	if hits, _, _ := SearchChats(ctx, d.DB, SearchQuery{UserID: u.a, Terms: terms("needle"), Role: "assistant", Limit: 10}); len(hits) != 3 {
		t.Errorf("assistant filter: %d", len(hits))
	}
	if hits, _, _ := SearchChats(ctx, d.DB, SearchQuery{UserID: u.a, Terms: terms("needle chat"), Limit: 10}); len(hits) != 0 {
		t.Errorf("all terms must match one turn: %+v", hits)
	}
	var seen []int
	var after *SearchCursor
	for pages := 0; pages < 10; pages++ {
		hits, more, err := SearchChats(ctx, d.DB, SearchQuery{UserID: u.a, Terms: terms("needle"), Limit: 3, After: after})
		if err != nil {
			t.Fatal(err)
		}
		seen = append(seen, hitSeqs(hits)...)
		if !more {
			break
		}
		l := hits[len(hits)-1]
		after = &SearchCursor{CreatedAt: l.ChatCreatedAt, ChatID: l.ChatID, Seq: l.Seq}
	}
	want := []int{6, 5, 4, 3, 2, 1, 0}
	if len(seen) != len(want) {
		t.Fatalf("paged %v", seen)
	}
	for i := range want {
		if seen[i] != want[i] {
			t.Fatalf("paged %v want %v", seen, want)
		}
	}
}

func TestReindexReplacesAndStaleness(t *testing.T) {
	d, u := setupTwo(t)
	ctx := context.Background()
	if st, err := StaleChats(ctx, d.DB, u.a, "", 10); err != nil || len(st) != 1 {
		t.Fatalf("unindexed chat is stale: %v %v", st, err)
	}
	mustIndex(t, d, u.a, u.chatA.ID, []IndexDoc{{Seq: 0, Role: "user", Text: "old text"}})
	if st, _ := StaleChats(ctx, d.DB, u.a, "", 10); len(st) != 0 {
		t.Fatalf("fresh chat listed stale: %v", st)
	}
	// A turn arrives after the index was read: stale again, and an older read cannot undo that.
	_ = InvalidateChatIndex(ctx, d.DB, u.a, u.chatA.ID, t0.Add(time.Minute))
	if st, _ := StaleChats(ctx, d.DB, u.a, "", 10); len(st) != 1 {
		t.Fatal("invalidated chat not stale")
	}
	if _, err := ReplaceChatIndex(ctx, d.DB, u.a, u.chatA.ID, nil, t0.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if st, _ := StaleChats(ctx, d.DB, u.a, "", 10); len(st) != 1 {
		t.Fatal("a read that began before the invalidation made the chat fresh")
	}
	if ok, _ := ReplaceChatIndex(ctx, d.DB, u.a, u.chatA.ID, []IndexDoc{{Seq: 0, Role: "user", Text: "new text"}}, t0.Add(2*time.Minute)); !ok {
		t.Fatal("newer read refused")
	}
	if ok, _ := ReplaceChatIndex(ctx, d.DB, u.a, u.chatA.ID, []IndexDoc{{Seq: 0, Role: "user", Text: "older read"}}, t0.Add(time.Minute+time.Second)); ok {
		t.Error("an older read overwrote a newer index")
	}
	if hits, _, _ := SearchChats(ctx, d.DB, SearchQuery{UserID: u.a, Terms: terms("old text"), Limit: 5}); len(hits) != 0 {
		t.Errorf("replaced text still searchable: %+v", hits)
	}
	if hits, _, _ := SearchChats(ctx, d.DB, SearchQuery{UserID: u.a, Terms: terms("new text"), Limit: 5}); len(hits) != 1 {
		t.Errorf("new text missing: %+v", hits)
	}
	if err := PurgeChatIndex(ctx, d.DB, u.a, u.chatA.ID); err != nil {
		t.Fatal(err)
	}
	if hits, _, _ := SearchChats(ctx, d.DB, SearchQuery{UserID: u.a, Terms: terms("text"), Limit: 5}); len(hits) != 0 {
		t.Errorf("purged: %+v", hits)
	}
}

func TestLongTextIsClippedAndMarkedIncomplete(t *testing.T) {
	d, u := setupTwo(t)
	long := strings.Repeat("é", MaxIndexedText) + " tailword"
	mustIndex(t, d, u.a, u.chatA.ID, []IndexDoc{{Seq: 0, Role: "user", Text: long}})
	if hits, _, _ := SearchChats(context.Background(), d.DB, SearchQuery{UserID: u.a, Terms: terms("tailword"), Limit: 5}); len(hits) != 0 {
		t.Error("text beyond the cap is indexed")
	}
	if _, pen, par, _ := IndexStatus(context.Background(), d.DB, u.a, ""); pen != 0 || par != 1 {
		t.Errorf("a clipped chat is partial, not pending: pending=%d partial=%d", pen, par)
	}
}

// The sort key must be covered by an index, or every page sorts in memory (ListAudit lesson).
func TestSearchPlanUsesOwnerIndexWithoutSorting(t *testing.T) {
	d, u := setupTwo(t)
	rows, err := d.DB.Query(`EXPLAIN QUERY PLAN SELECT c.id, COALESCE(c.title,''), c.created_at, d.seq, d.role, d.body, COALESCE(d.created_at,'')
		FROM chats c CROSS JOIN search_docs d ON d.chat_id = c.id AND d.user_id = c.user_id
		WHERE c.user_id = ? AND c.status = 'ACTIVE' AND instr(d.folded, ?) > 0 AND (c.created_at, c.id, d.seq) < (?, ?, ?)
		ORDER BY c.created_at DESC, c.id DESC, d.seq DESC LIMIT ?`, u.a, "x", "z", "z", 9, 11)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var plan []string
	for rows.Next() {
		var id, parent, aux, detail int
		var s string
		if err := rows.Scan(&id, &parent, &aux, &s); err != nil {
			_ = detail
			t.Fatal(err)
		}
		plan = append(plan, s)
	}
	all := strings.Join(plan, "\n")
	t.Log("\n" + all)
	// Sorting the turns inside one chat is bounded by that chat; sorting the whole result is not.
	if strings.Contains(all, "USE TEMP B-TREE FOR ORDER BY") || strings.Contains(all, "LAST 2 TERMS") {
		t.Errorf("search sorts in memory:\n%s", all)
	}
	if !strings.Contains(all, "chats_user_status_created") {
		t.Errorf("search does not use the chats ordering index:\n%s", all)
	}
	if strings.Contains(all, "SCAN d") || strings.Contains(all, "SCAN search_docs") {
		t.Errorf("search scans the whole index table:\n%s", all)
	}
}

// A run starting bumps chats.updated_at. Paging must not skip or repeat a chat because of it:
// the cursor's chat key is created_at, which never moves. Fails with an updated_at key.
func TestSearchPagingSurvivesARunStartingMidPagination(t *testing.T) {
	d, u := setupTwo(t)
	ctx := context.Background()
	var ids []string
	for i := 1; i <= 3; i++ {
		c, err := CreateChat(ctx, d.DB, u.a, u.bindA, "sess-page-"+string(rune('0'+i)), "", t0.Add(time.Duration(i)*time.Minute))
		if err != nil {
			t.Fatal(err)
		}
		mustIndex(t, d, u.a, c.ID, []IndexDoc{{Seq: 0, Role: "user", Text: "pagingword"}})
		ids = append(ids, c.ID) // ids[2] is the newest
	}
	first, more, err := SearchChats(ctx, d.DB, SearchQuery{UserID: u.a, Terms: terms("pagingword"), Limit: 1})
	if err != nil || !more || first[0].ChatID != ids[2] {
		t.Fatalf("first page: %+v %v %v", first, more, err)
	}
	// A run is admitted in the oldest chat: its updated_at jumps above every other chat.
	if _, _, err := AdmitRun(ctx, d.DB, u.a, ids[0], "idem-paging-0001", t0.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	var got []string
	after := &SearchCursor{CreatedAt: first[0].ChatCreatedAt, ChatID: first[0].ChatID, Seq: first[0].Seq}
	for pages := 0; pages < 5; pages++ {
		hits, more, err := SearchChats(ctx, d.DB, SearchQuery{UserID: u.a, Terms: terms("pagingword"), Limit: 1, After: after})
		if err != nil {
			t.Fatal(err)
		}
		for _, h := range hits {
			got = append(got, h.ChatID)
		}
		if !more {
			break
		}
		l := hits[len(hits)-1]
		after = &SearchCursor{CreatedAt: l.ChatCreatedAt, ChatID: l.ChatID, Seq: l.Seq}
	}
	if len(got) != 2 || got[0] != ids[1] || got[1] != ids[0] {
		t.Errorf("pages after the bump = %v, want [%s %s]: a chat was skipped or repeated", got, ids[1], ids[0])
	}
}
