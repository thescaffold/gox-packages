package filter

import (
	"fmt"
	"strconv"
	"strings"
)

// Result holds everything a CRUD handler needs to execute a filtered query.
// It mirrors the TS makeFilter() return tuple exactly:
//
//	[selector, page, perPage, columns, relations]
type Result struct {
	// Conditions is a slice of OR-able WHERE maps.
	// Each map entry is col→value (plain or LIKE) that the caller ANDs together.
	// Multiple entries in the slice are OR-ed: WHERE (a AND b) OR (c AND d)
	Conditions []map[string]any

	Page      int
	PerPage   int
	Columns   []string // SELECT columns; always includes "id", "created_at" when non-empty
	Relations []string // PRELOAD / JOIN names

	// DateFrom / DateTo are ISO-8601 strings for a createdAt BETWEEN clause.
	// Empty string means no date filter.
	DateFrom string
	DateTo   string
}

// reserved keys that are consumed by MakeFilter and NOT forwarded as field filters.
var reserved = map[string]bool{
	"query":     true,
	"page":      true,
	"from":      true,
	"to":        true,
	"perPage":   true,
	"relations": true,
	"columns":   true,
	"scope":     true,
}

// MakeFilter converts raw query params into a Result, mirroring TS makeFilter() exactly.
//
// searchable lists the column names that the `query=` param searches via LIKE.
func MakeFilter(queries map[string]string, searchable []string) Result {
	query := queries["query"]
	from := queries["from"]
	to := queries["to"]

	page := parseIntDefault(queries["page"], 1)
	perPage := parseIntDefault(queries["perPage"], 12)

	columns := splitTrimmed(queries["columns"])
	if len(columns) > 0 {
		columns = append([]string{"id", "created_at"}, columns...)
	}
	relations := splitTrimmed(queries["relations"])

	// Build field filters from remaining (non-reserved) keys.
	fieldFilters := map[string]any{}
	for k, v := range queries {
		if reserved[k] {
			continue
		}
		obj := expandDotted(k, v)
		fieldFilters = recursiveMerge(fieldFilters, obj)
	}

	// Build selector (list of OR-able condition maps).
	var selector []map[string]any

	if query != "" {
		for _, col := range searchable {
			selector = append(selector, map[string]any{
				col: Like(query),
			})
		}
	}

	if len(fieldFilters) > 0 {
		if len(selector) > 0 {
			// merge field filters into every existing LIKE condition
			for i, s := range selector {
				for k, v := range fieldFilters {
					s[k] = v
				}
				selector[i] = s
			}
		} else {
			selector = []map[string]any{fieldFilters}
		}
	}

	res := Result{
		Conditions: selector,
		Page:       page,
		PerPage:    perPage,
		Columns:    columns,
		Relations:  relations,
		DateFrom:   from,
		DateTo:     to,
	}
	return res
}

// Like wraps a value to signal that the caller should build a LIKE clause.
type Like string

// LikePattern returns the SQL LIKE pattern for a Like value.
func (l Like) Pattern() string {
	return fmt.Sprintf("%%%s%%", string(l))
}

// Offset returns the GORM/SQL OFFSET for the given page.
func (r *Result) Offset() int {
	if r.Page <= 1 {
		return 0
	}
	return (r.Page - 1) * r.PerPage
}

// --- helpers ---

func parseIntDefault(s string, def int) int {
	if s == "" {
		return def
	}
	n, err := strconv.Atoi(s)
	if err != nil || n < 1 {
		return def
	}
	return n
}

func splitTrimmed(s string) []string {
	if s == "" {
		return nil
	}
	parts := strings.Split(strings.TrimSpace(s), ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if t := strings.TrimSpace(p); t != "" {
			out = append(out, t)
		}
	}
	return out
}

// expandDotted turns "user.name" → {"user": {"name": value}}.
func expandDotted(key, value string) map[string]any {
	parts := strings.Split(key, ".")
	if len(parts) == 1 {
		return map[string]any{key: value}
	}
	// build from the inside out
	obj := map[string]any{parts[len(parts)-1]: value}
	for i := len(parts) - 2; i >= 0; i-- {
		obj = map[string]any{parts[i]: obj}
	}
	return obj
}

// recursiveMerge deep-merges src into dst (both must be map[string]any at every level).
func recursiveMerge(dst, src map[string]any) map[string]any {
	if dst == nil {
		dst = map[string]any{}
	}
	for k, sv := range src {
		if dv, ok := dst[k]; ok {
			dsub, dIsMap := dv.(map[string]any)
			ssub, sIsMap := sv.(map[string]any)
			if dIsMap && sIsMap {
				dst[k] = recursiveMerge(dsub, ssub)
				continue
			}
		}
		dst[k] = sv
	}
	return dst
}
