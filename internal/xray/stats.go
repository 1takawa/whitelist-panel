package xray

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

// Traffic is a byte counter pair.
type Traffic struct{ Up, Down int64 }

// InboundStatPrefix marks a per-inbound counter in the map QueryStats returns ("inbound:in-<id>"). Client names
// cannot contain ':', so these keys never collide with a user's email.
const InboundStatPrefix = "inbound:"

// ParseStats parses the output of `xray api statsquery` and returns per-user (email) counters.
// Names look like "user>>>EMAIL>>>traffic>>>uplink"; inbound/outbound counters are ignored.
func ParseStats(data []byte) (map[string]Traffic, error) {
	return parseStats(data, false)
}

// ParseStatsAll is ParseStats that also returns the per-inbound counters, under InboundStatPrefix + tag.
func ParseStatsAll(data []byte) (map[string]Traffic, error) {
	return parseStats(data, true)
}

func parseStats(data []byte, withInbounds bool) (map[string]Traffic, error) {
	var doc struct {
		Stat []struct {
			Name  string `json:"name"`
			Value any    `json:"value"` // number or numeric string depending on the Xray version
		} `json:"stat"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("parse xray stats: %w", err)
	}
	out := map[string]Traffic{}
	for _, s := range doc.Stat {
		parts := strings.Split(s.Name, ">>>")
		if len(parts) != 4 || parts[2] != "traffic" {
			continue
		}
		key := parts[1]
		switch parts[0] {
		case "user":
		case "inbound":
			if !withInbounds || key == "api" {
				continue
			}
			key = InboundStatPrefix + key
		default:
			continue
		}
		v, ok := toInt64(s.Value)
		if !ok {
			continue
		}
		t := out[key]
		switch parts[3] {
		case "uplink":
			t.Up += v
		case "downlink":
			t.Down += v
		default:
			continue
		}
		out[key] = t
	}
	return out, nil
}

// SplitStats separates a QueryStats result into per-user counters (by email) and per-inbound counters (by tag).
func SplitStats(m map[string]Traffic) (users, inbounds map[string]Traffic) {
	users, inbounds = map[string]Traffic{}, map[string]Traffic{}
	for k, v := range m {
		if tag, ok := strings.CutPrefix(k, InboundStatPrefix); ok {
			inbounds[tag] = v
		} else {
			users[k] = v
		}
	}
	return users, inbounds
}

func toInt64(v any) (int64, bool) {
	switch x := v.(type) {
	case float64:
		return int64(x), true
	case string:
		n, err := strconv.ParseInt(x, 10, 64)
		return n, err == nil
	case nil:
		return 0, true // zero counters are omitted by some versions
	}
	return 0, false
}
