package mediamanager

import (
	"bytes"
	"encoding/json"
	"maps"
)

// rawDoc lets a typed struct round-trip a JSON object without losing the fields
// the struct does not model. The arr apps want a PUT to carry the whole
// resource, and an add to carry the whole lookup result, while the typed struct
// only names what Wheelhouse reads.
//
// raw is everything the service sent. base is how the typed fields marshaled
// right after decoding. On encode, a typed field whose marshaled form still
// equals base keeps the exact bytes the service sent (nested unknown fields
// included); only fields the caller changed are replaced.
type rawDoc struct {
	raw  map[string]json.RawMessage
	base map[string]json.RawMessage
}

// decodeDoc fills typed (a pointer to a struct type that has no JSON methods of
// its own) and records the document.
func decodeDoc(data []byte, typed any) (rawDoc, error) {
	if err := json.Unmarshal(data, typed); err != nil {
		return rawDoc{}, err
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return rawDoc{}, err
	}
	base, err := fieldsOf(typed)
	if err != nil {
		return rawDoc{}, err
	}
	return rawDoc{raw: raw, base: base}, nil
}

func fieldsOf(typed any) (map[string]json.RawMessage, error) {
	b, err := json.Marshal(typed)
	if err != nil {
		return nil, err
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, err
	}
	return m, nil
}

// encode merges the typed fields into the recorded document.
func (d rawDoc) encode(typed any) ([]byte, error) {
	cur, err := fieldsOf(typed)
	if err != nil {
		return nil, err
	}
	out := make(map[string]json.RawMessage, len(d.raw)+len(cur))
	maps.Copy(out, d.raw)
	for k, v := range cur {
		if old, ok := d.base[k]; ok && bytes.Equal(old, v) {
			continue // unchanged: keep what the service sent (or what edit removed)
		}
		out[k] = v
	}
	for k := range d.base {
		if _, ok := cur[k]; !ok {
			delete(out, k) // an optional field was cleared
		}
	}
	return json.Marshal(out)
}
