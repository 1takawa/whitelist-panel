package model

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
)

// Doc is a free-form JSON object: the part of an Xray configuration the panel passes through without modelling it
// field by field (the protocol "settings" and the "streamSettings" of an inbound, the profile template of a server).
// In the REST API it is an ordinary JSON object. In MongoDB it is stored as a JSON string, so the driver never
// reinterprets nested documents (numbers stay numbers, empty arrays stay arrays).
type Doc map[string]any

// MarshalBSONValue stores the document as a BSON string holding its JSON text (a nil document is BSON null).
func (d Doc) MarshalBSONValue() (byte, []byte, error) {
	if d == nil {
		return 0x0A, nil, nil
	}
	b, err := json.Marshal(map[string]any(d))
	if err != nil {
		return 0, nil, err
	}
	data := make([]byte, 4+len(b)+1)
	binary.LittleEndian.PutUint32(data, uint32(len(b)+1))
	copy(data[4:], b)
	return 0x02, data, nil
}

// UnmarshalBSONValue reads what MarshalBSONValue wrote.
func (d *Doc) UnmarshalBSONValue(t byte, data []byte) error {
	switch t {
	case 0x0A, 0x06: // null, undefined
		*d = nil
		return nil
	case 0x02:
		if len(data) < 5 {
			return fmt.Errorf("model.Doc: short BSON string")
		}
		n := int(binary.LittleEndian.Uint32(data))
		if n < 1 || 4+n > len(data) {
			return fmt.Errorf("model.Doc: bad BSON string length")
		}
		var m map[string]any
		if err := json.Unmarshal(data[4:4+n-1], &m); err != nil {
			return fmt.Errorf("model.Doc: %w", err)
		}
		*d = m
		return nil
	}
	return fmt.Errorf("model.Doc: cannot read BSON type 0x%02x", t)
}

// Clone returns a deep copy.
func (d Doc) Clone() Doc {
	if d == nil {
		return nil
	}
	return Doc(cloneMap(d))
}

func cloneMap(m map[string]any) map[string]any {
	out := make(map[string]any, len(m))
	for k, v := range m {
		out[k] = cloneAny(v)
	}
	return out
}

func cloneAny(v any) any {
	switch x := v.(type) {
	case map[string]any:
		return cloneMap(x)
	case Doc:
		return cloneMap(x)
	case []any:
		out := make([]any, len(x))
		for i := range x {
			out[i] = cloneAny(x[i])
		}
		return out
	case []string:
		return append([]string(nil), x...)
	}
	return v
}

// Sub returns the nested object at key, or nil. The result is the live map, not a copy.
func (d Doc) Sub(key string) map[string]any {
	switch x := d[key].(type) {
	case map[string]any:
		return x
	case Doc:
		return x
	}
	return nil
}

// Ensure returns the nested object at key, creating it when absent or not an object.
func (d Doc) Ensure(key string) map[string]any {
	if m := d.Sub(key); m != nil {
		return m
	}
	m := map[string]any{}
	d[key] = m
	return m
}

// Str reads a string value ("" when absent or of another type).
func Str(m map[string]any, key string) string {
	s, _ := m[key].(string)
	return s
}

// Bool reads a boolean value.
func Bool(m map[string]any, key string) bool {
	b, _ := m[key].(bool)
	return b
}

// Strs reads a list of strings; a single string counts as a list of one.
func Strs(m map[string]any, key string) []string {
	switch x := m[key].(type) {
	case []any:
		out := make([]string, 0, len(x))
		for _, e := range x {
			if s, ok := e.(string); ok {
				out = append(out, s)
			}
		}
		return out
	case []string:
		return append([]string(nil), x...)
	case string:
		if x != "" {
			return []string{x}
		}
	}
	return nil
}

// MergeDoc merges src over dst: objects merge recursively, any other value (arrays included) replaces. dst is changed.
func MergeDoc(dst, src map[string]any) {
	for k, v := range src {
		sm, sok := v.(map[string]any)
		dm, dok := dst[k].(map[string]any)
		if sok && dok {
			MergeDoc(dm, sm)
			continue
		}
		dst[k] = cloneAny(v)
	}
}
