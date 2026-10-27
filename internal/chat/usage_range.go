package chat

import (
	"net/http"
	"sort"
	"time"

	"github.com/raufimusaddiq/antares/internal/store"
)

// Usage dashboard (#151, PRD §21.7, §22): the caller's own replies over the last 7, 30 or 90 UTC days, by model, by
// chat and by day. Tokens are what Hermes reported; replies that reported nothing are counted as unreported, never as
// zero. Spend is this app's estimate from its dated price list (priceListDate), shown only where every counted reply
// had a known price; it is not the provider's bill, which an admin reads from the shared account (#146).

var usageRanges = map[string]int{"7": 7, "30": 30, "90": 90}

type usageTotals struct {
	Runs       int      `json:"runs"`       // finished replies
	Unreported int      `json:"unreported"` // of those, how many reported no usage
	Unpriced   int      `json:"unpriced"`   // replies with usage on a model with no known price
	Input      int64    `json:"input"`
	Output     int64    `json:"output"`
	CacheRead  int64    `json:"cacheRead"`
	Estimate   *float64 `json:"estimate"` // null when no counted reply could be priced
}

func (t *usageTotals) add(r store.UsageRow) {
	t.Runs += r.Runs
	if !r.Reported {
		t.Unreported += r.Runs
		return
	}
	t.Input, t.Output, t.CacheRead = t.Input+r.Input, t.Output+r.Output, t.CacheRead+r.CacheRead
	if p, ok := prices[r.Model]; ok {
		e := estimateIDR(p, r.RunUsage)
		if t.Estimate != nil {
			e += *t.Estimate
		}
		t.Estimate = &e
	} else {
		t.Unpriced += r.Runs
	}
}

type usageByModel struct {
	Model string `json:"model"` // "" when Hermes did not say
	usageTotals
}

type usageByChat struct {
	ChatID  string `json:"chatId"` // "" for chats you deleted
	Title   string `json:"title"`
	Deleted bool   `json:"deleted"`
	usageTotals
}

type usageByDay struct {
	Day string `json:"day"`
	usageTotals
}

type usageRangeView struct {
	Days      int            `json:"days"`
	Since     string         `json:"since"` // the first UTC day counted
	Currency  string         `json:"currency"`
	PriceDate string         `json:"priceDate"` // when the price list was read
	Total     usageTotals    `json:"total"`
	ByModel   []usageByModel `json:"byModel"`
	ByChat    []usageByChat  `json:"byChat"`
	ByDay     []usageByDay   `json:"byDay"` // every day in the range, oldest first, empty days included
}

func (s *Service) getUsageRange(w http.ResponseWriter, r *http.Request) {
	uid, _ := user(r)
	days, ok := usageRanges[r.URL.Query().Get("days")]
	if !ok {
		apiError(w, http.StatusBadRequest, "INVALID_REQUEST", "days must be 7, 30 or 90")
		return
	}
	since := dayStart(s.now()).AddDate(0, 0, -(days - 1))
	rows, err := store.UsageRows(r.Context(), s.db, uid, since)
	if err != nil {
		internalError(w, "usage range", err)
		return
	}
	v := usageRangeView{Days: days, Since: since.Format("2006-01-02"), Currency: priceCurrency, PriceDate: priceListDate,
		ByModel: []usageByModel{}, ByChat: []usageByChat{}, ByDay: make([]usageByDay, days)}
	for i := range v.ByDay {
		v.ByDay[i].Day = since.AddDate(0, 0, i).Format("2006-01-02")
	}
	models, chats := map[string]*usageByModel{}, map[string]*usageByChat{}
	for _, row := range rows {
		v.Total.add(row)
		if i := dayIndex(since, row.Day); i >= 0 && i < days {
			v.ByDay[i].add(row)
		}
		if row.Reported {
			m := models[row.Model]
			if m == nil {
				m = &usageByModel{Model: row.Model}
				models[row.Model] = m
			}
			m.add(row)
		}
		c := chats[row.ChatID]
		if c == nil {
			c = &usageByChat{ChatID: row.ChatID, Title: row.Title, Deleted: row.ChatID == ""}
			chats[row.ChatID] = c
		}
		c.add(row)
	}
	for _, m := range models {
		v.ByModel = append(v.ByModel, *m)
	}
	sort.Slice(v.ByModel, func(i, j int) bool {
		return v.ByModel[i].Input+v.ByModel[i].Output > v.ByModel[j].Input+v.ByModel[j].Output
	})
	for _, c := range chats {
		v.ByChat = append(v.ByChat, *c)
	}
	sort.Slice(v.ByChat, func(i, j int) bool {
		a, b := v.ByChat[i], v.ByChat[j]
		if a.Input+a.Output != b.Input+b.Output {
			return a.Input+a.Output > b.Input+b.Output
		}
		return a.ChatID < b.ChatID
	})
	writeJSON(w, http.StatusOK, v)
}

func dayIndex(since time.Time, day string) int {
	d, err := time.Parse("2006-01-02", day)
	if err != nil {
		return -1
	}
	return int(d.Sub(since).Hours() / 24)
}
