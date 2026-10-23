package hermes

// Usage is the token accounting Hermes reports for one run.
//
// Source, read from the deployed container (nousresearch/hermes-agent, 2026-10-23):
//   - gateway/platforms/api_server_runs.py:86-91   _USAGE_FIELDS: input_tokens, output_tokens,
//     total_tokens, cache_read_tokens, cache_write_tokens
//   - gateway/platforms/api_server_runs.py:867-874 _run_usage: a missing or non-numeric counter
//     reads as 0, so an all-zero block means "not counted", not "free"
//   - agent/turn_usage.py:181                      the counters are summed over every model call of
//     the run, so they are a billing-style total, not what the model carries in its window
//   - tui_gateway/server.py:1408                   input_tokens includes cache reads and writes
//
// The block rides on GET /v1/runs/{id} and on the terminal run.* event.
type Usage struct {
	Input      int64 // includes CacheRead and CacheWrite
	Output     int64
	CacheRead  int64
	CacheWrite int64
	Model      string // runtime.model: the model that actually served the run, "" when not reported
}

const maxCount = 1 << 40

// parseUsage returns nil when Hermes reported nothing countable. Unknown is not
// zero: a block with no input and no output tokens is treated as not reported.
func parseUsage(usage, runtime map[string]any) *Usage {
	n := func(k string) int64 {
		f, ok := usage[k].(float64)
		if !ok || f < 0 || f > maxCount {
			return 0
		}
		return int64(f)
	}
	u := &Usage{Input: n("input_tokens"), Output: n("output_tokens"), CacheRead: n("cache_read_tokens"), CacheWrite: n("cache_write_tokens")}
	if u.Input == 0 && u.Output == 0 {
		return nil
	}
	if m, ok := runtime["model"].(string); ok && len(m) <= 100 {
		u.Model = m
	} else if m, ok := usage["model"].(string); ok && len(m) <= 100 {
		u.Model = m
	}
	return u
}

func asObject(v any) map[string]any {
	m, _ := v.(map[string]any)
	return m
}
// probe 1792785818
