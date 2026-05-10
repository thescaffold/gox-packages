package utils

// Tuple5 is the canonical 5-tuple shape returned by HTTP wrappers (jsx-style)
// and stored as `[success, status, statusText, errBody, data]`.
type Tuple5 struct {
	Success    bool
	Status     int
	StatusText string
	ErrBody    any
	Data       any
}

// OnlyData runs fn() and returns just its data field. On failure it returns
// nil (callers can decide to surface the error). Mirrors TS onlyData()
// (request.util.ts) which throws on failure; in Go we return nil because
// errors don't propagate through return values the same way.
//
// For full error semantics, use OnlyDataE which returns an error too.
func OnlyData(fn func() Tuple5) any {
	t := fn()
	if !t.Success {
		return nil
	}
	return t.Data
}

// OnlyDataE is like OnlyData but also returns an error envelope on failure.
// On failure: returns nil, an error containing the title/message.
func OnlyDataE(fn func() Tuple5) (any, error) {
	t := fn()
	if !t.Success {
		title, message := t.StatusText, ""
		if d, ok := t.Data.(map[string]any); ok {
			if v, ok := d["title"].(string); ok {
				title = v
			}
			if v, ok := d["message"].(string); ok {
				message = v
			}
		}
		return nil, &requestError{Status: t.Status, Title: title, Message: message}
	}
	return t.Data, nil
}

type requestError struct {
	Status  int
	Title   string
	Message string
}

func (e *requestError) Error() string {
	if e.Message == "" {
		return e.Title
	}
	return e.Title + ": " + e.Message
}
