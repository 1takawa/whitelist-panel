// Package legacy reads user records exported from the previous panel (a MongoDB `users` collection written by
// mongoexport) so they can be imported into this panel. Only the fields that have a meaning here are kept; legacy
// passwords and device records are dropped while parsing and never leave this package.
package legacy

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"strconv"
	"strings"
	"time"
)

// User is one legacy account.
type User struct {
	Name           string
	Blocked        bool
	Unlimited      bool
	MaxBytes       int64
	Up, Down       int64
	ExpirationDays int
	Created        time.Time // zero when the date is missing or unreadable
	Token          string    // the legacy subscription token
	Note           string
	TelegramID     int64
	Devices        int // number of bound devices (HWID); not migrated
}

// Parse reads a JSON array or newline-delimited JSON (both are what mongoexport writes) of legacy user documents,
// accepting MongoDB Extended JSON numbers such as {"$numberLong":"123"}.
func Parse(r io.Reader) ([]User, error) {
	raw, err := io.ReadAll(io.LimitReader(r, 64<<20))
	if err != nil {
		return nil, err
	}
	raw = bytes.TrimPrefix(bytes.TrimSpace(raw), []byte("\xef\xbb\xbf"))
	if len(raw) == 0 {
		return nil, errors.New("the file is empty")
	}
	var docs []map[string]any
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	if raw[0] == '[' {
		if err := dec.Decode(&docs); err != nil {
			return nil, fmt.Errorf("not valid JSON: %v", err)
		}
	} else {
		for {
			var d map[string]any
			if err := dec.Decode(&d); err == io.EOF {
				break
			} else if err != nil {
				return nil, fmt.Errorf("not valid JSON lines: %v", err)
			}
			docs = append(docs, d)
		}
	}
	out := make([]User, 0, len(docs))
	for i, d := range docs {
		u, err := fromDoc(d)
		if err != nil {
			return nil, fmt.Errorf("record %d: %v", i+1, err)
		}
		out = append(out, u)
	}
	return out, nil
}

func fromDoc(d map[string]any) (User, error) {
	u := User{}
	id, ok := d["_id"].(string)
	if !ok || strings.TrimSpace(id) == "" {
		return u, errors.New("no _id (the user name); is this the users collection?")
	}
	u.Name = strings.TrimSpace(id)
	u.Blocked, _ = d["blocked"].(bool)
	u.Unlimited, _ = d["unlimited_user"].(bool)
	u.MaxBytes = num(d["max_download_bytes"])
	u.Up, u.Down = num(d["upload_bytes"]), num(d["download_bytes"])
	u.ExpirationDays = int(num(d["expiration_days"]))
	u.Token, _ = d["subscription_token"].(string)
	u.Note, _ = d["note"].(string)
	u.TelegramID = num(d["telegram_id"])
	if s, ok := d["account_creation_date"].(string); ok {
		if t, err := time.Parse("2006-01-02", strings.TrimSpace(s)); err == nil {
			u.Created = t
		}
	}
	if m, ok := d["hwid_device_info"].(map[string]any); ok {
		u.Devices = len(m)
	} else if a, ok := d["hwid_devices"].([]any); ok {
		u.Devices = len(a)
	}
	if u.MaxBytes < 0 {
		u.MaxBytes = 0
	}
	if u.Up < 0 {
		u.Up = 0
	}
	if u.Down < 0 {
		u.Down = 0
	}
	if u.ExpirationDays < 0 {
		u.ExpirationDays = 0
	}
	return u, nil
}

// num reads a number in any of the shapes MongoDB tools produce; anything else is 0.
func num(v any) int64 {
	switch x := v.(type) {
	case json.Number:
		return parseNum(x.String())
	case string:
		return parseNum(x)
	case map[string]any:
		for _, k := range []string{"$numberLong", "$numberInt", "$numberDouble", "$numberDecimal"} {
			if s, ok := x[k]; ok {
				return num(s)
			}
		}
	}
	return 0
}

func parseNum(s string) int64 {
	if n, err := strconv.ParseInt(s, 10, 64); err == nil {
		return n
	}
	if f, err := strconv.ParseFloat(s, 64); err == nil && !math.IsNaN(f) && !math.IsInf(f, 0) && math.Abs(f) < 9e18 {
		return int64(f)
	}
	return 0
}
