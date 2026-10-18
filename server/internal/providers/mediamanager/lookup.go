package mediamanager

import (
	"encoding/json"
	"fmt"
)

// LookupResult is a search hit from Sonarr or Radarr. It is near-identical to
// the full library resource, minus an id (nil until the item is added), and it
// is passed mostly as-is when adding, so every field the service sent is kept
// (see the package comment) and the typed fields are only the ones Wheelhouse
// reads.
type LookupResult struct {
	// ID is nil for results that are not in the library. The services omit it
	// entirely (they do not send null), which JSON decoding also maps to nil.
	ID       *int    `json:"id,omitempty"`
	Title    string  `json:"title"`
	Year     int     `json:"year"`
	Overview string  `json:"overview,omitempty"`
	Images   []Image `json:"images,omitempty"`
	TvdbID   int     `json:"tvdbId,omitempty"`
	TmdbID   int     `json:"tmdbId,omitempty"`
	Network  string  `json:"network,omitempty"`
	Studio   string  `json:"studio,omitempty"`

	doc rawDoc
}

// UnmarshalJSON keeps the fields it does not model.
func (r *LookupResult) UnmarshalJSON(data []byte) error {
	type plain LookupResult
	var p plain
	doc, err := decodeDoc(data, &p)
	if err != nil {
		return err
	}
	*r = LookupResult(p)
	r.doc = doc
	return nil
}

// MarshalJSON writes the unmodelled fields back unchanged.
func (r LookupResult) MarshalJSON() ([]byte, error) {
	type plain LookupResult
	return r.doc.encode(plain(r))
}

// Set returns a copy with one field set (any JSON-encodable value). The typed
// fields of the copy reflect the change.
func (r LookupResult) Set(name string, value any) (LookupResult, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return LookupResult{}, fmt.Errorf("cannot encode %s", name)
	}
	return r.edit(func(m map[string]json.RawMessage) { m[name] = encoded })
}

// Unset returns a copy without a field.
func (r LookupResult) Unset(name string) (LookupResult, error) {
	return r.edit(func(m map[string]json.RawMessage) { delete(m, name) })
}

// edit re-decodes the document after a change, so the typed fields, the raw
// document and the baseline stay consistent.
func (r LookupResult) edit(change func(map[string]json.RawMessage)) (LookupResult, error) {
	data, err := r.MarshalJSON()
	if err != nil {
		return LookupResult{}, err
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(data, &m); err != nil {
		return LookupResult{}, err
	}
	change(m)
	data, err = json.Marshal(m)
	if err != nil {
		return LookupResult{}, err
	}
	var out LookupResult
	if err := json.Unmarshal(data, &out); err != nil {
		return LookupResult{}, err
	}
	return out, nil
}

// AddOptions are the user's choices when adding an item.
type AddOptions struct {
	QualityProfileID int
	RootFolderPath   string
	Monitored        bool
	// SearchNow starts a search for the item (or its missing episodes) right away.
	SearchNow bool
	// MinimumAvailability is for movies only; it defaults to "released".
	MinimumAvailability string
}

// SeriesAddPayload is the body Sonarr's add expects: the lookup result without
// its id, plus the user's choices.
func (r LookupResult) SeriesAddPayload(o AddOptions) (LookupResult, error) {
	return r.payload(map[string]any{
		"qualityProfileId":  o.QualityProfileID,
		"languageProfileId": 1,
		"rootFolderPath":    o.RootFolderPath,
		"monitored":         o.Monitored,
		"seasonFolder":      true,
		"monitorNewItems":   "all",
		"addOptions":        map[string]any{"searchForMissingEpisodes": o.SearchNow},
	})
}

// MovieAddPayload is the body Radarr's add expects.
func (r LookupResult) MovieAddPayload(o AddOptions) (LookupResult, error) {
	availability := o.MinimumAvailability
	if availability == "" {
		availability = "released"
	}
	return r.payload(map[string]any{
		"qualityProfileId":    o.QualityProfileID,
		"rootFolderPath":      o.RootFolderPath,
		"monitored":           o.Monitored,
		"minimumAvailability": availability,
		"addOptions":          map[string]any{"searchForMovie": o.SearchNow},
	})
}

func (r LookupResult) payload(fields map[string]any) (LookupResult, error) {
	out, err := r.Unset("id")
	if err != nil {
		return LookupResult{}, err
	}
	for name, value := range fields {
		if out, err = out.Set(name, value); err != nil {
			return LookupResult{}, err
		}
	}
	return out, nil
}
