package stripe

import (
	"net/http"
	"slices"
)

// pageParams reads limit (1 to 100, default 10) and starting_after. On
// failure it writes a 400 and returns false.
func pageParams(w http.ResponseWriter, p *Params) (int, string, bool) {
	limit, hasLimit, err := p.Int("limit")
	if err != nil {
		writeParamError(w, err)
		return 0, "", false
	}
	if !hasLimit {
		limit = 10
	}
	if limit < 1 || limit > 100 {
		invalidRequest(w, http.StatusBadRequest, "parameter_invalid_integer", "limit",
			"This value must be between 1 and 100.")
		return 0, "", false
	}
	after, _, err := p.String("starting_after")
	if err != nil {
		writeParamError(w, err)
		return 0, "", false
	}
	return int(limit), after, true
}

// paginate returns the page of all (newest first) after the object with id
// after, and whether more follow. An unknown after is a 400, as on Stripe.
func paginate[T any](w http.ResponseWriter, all []T, after string, limit int, id func(T) string, resource string) ([]T, bool, bool) {
	if after != "" {
		i := slices.IndexFunc(all, func(v T) bool { return id(v) == after })
		if i < 0 {
			invalidRequest(w, http.StatusBadRequest, "resource_missing", "starting_after",
				"No such "+resource+": '"+after+"'")
			return nil, false, false
		}
		all = all[i+1:]
	}
	page := all[:min(limit, len(all))]
	if page == nil {
		page = []T{}
	}
	return page, len(all) > len(page), true
}
