package legacy

import (
	"strings"
	"testing"
	"time"
)

// Synthetic records shaped like the legacy collection (no real data).
const sample = `[
 {"_id":"alice","password":"SECRETSECRETSECRETSECRET","max_download_bytes":{"$numberLong":"5368709120"},"expiration_days":30,
  "account_creation_date":"2026-10-08","blocked":false,"unlimited_user":false,"status":"Offline",
  "upload_bytes":{"$numberLong":"1000"},"download_bytes":{"$numberLong":"2000"},"online_count":0,
  "subscription_token":"tok-alice-0123456789","note":"friend","telegram_id":{"$numberLong":"123456789"},
  "hwid_device_limit":2,"hwid_devices":["a","b"],"hwid_device_info":{"a":{"os":"x"},"b":{"os":"y"}},
  "traffic_snapshots":{"local":{}}},
 {"_id":"bob","password":"x","max_download_bytes":0,"expiration_days":0,"account_creation_date":"not a date","blocked":true,
  "upload_bytes":0,"download_bytes":{"$numberDouble":"12.0"},"subscription_token":"tok-bob"}
]`

func TestParseExtendedJSONArray(t *testing.T) {
	us, err := Parse(strings.NewReader(sample))
	if err != nil || len(us) != 2 {
		t.Fatalf("%v %+v", err, us)
	}
	a := us[0]
	if a.Name != "alice" || a.MaxBytes != 5<<30 || a.Up != 1000 || a.Down != 2000 || a.ExpirationDays != 30 || a.TelegramID != 123456789 ||
		a.Token != "tok-alice-0123456789" || a.Note != "friend" || a.Devices != 2 || a.Blocked ||
		!a.Created.Equal(time.Date(2026, 10, 8, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("alice: %+v", a)
	}
	b := us[1]
	if !b.Blocked || b.Down != 12 || !b.Created.IsZero() || b.Devices != 0 || b.TelegramID != 0 {
		t.Fatalf("bob (missing and odd fields): %+v", b)
	}
}

func TestParseJSONLinesAndBOM(t *testing.T) {
	in := "\xef\xbb\xbf{\"_id\":\"a\"}\n{\"_id\":\"b\",\"upload_bytes\":7}\n"
	us, err := Parse(strings.NewReader(in))
	if err != nil || len(us) != 2 || us[1].Up != 7 {
		t.Fatalf("%v %+v", err, us)
	}
}

func TestParseRejectsWrongInput(t *testing.T) {
	for name, in := range map[string]string{
		"empty":       "  ",
		"not json":    "hello",
		"no id":       `[{"name":"x"}]`,
		"object id":   `[{"_id":{"$oid":"5f"}}]`,
		"broken line": "{\"_id\":\"a\"}\n{oops",
	} {
		if _, err := Parse(strings.NewReader(in)); err == nil {
			t.Errorf("%s must fail", name)
		}
	}
}

func TestNegativeValuesAreClamped(t *testing.T) {
	us, _ := Parse(strings.NewReader(`[{"_id":"a","upload_bytes":-5,"max_download_bytes":-1,"expiration_days":-3}]`))
	if u := us[0]; u.Up != 0 || u.MaxBytes != 0 || u.ExpirationDays != 0 {
		t.Fatalf("%+v", u)
	}
}

func TestLegacySecretsAreNotKept(t *testing.T) {
	us, _ := Parse(strings.NewReader(sample))
	if strings.Contains(strings.ToLower(strings.TrimSpace(strings.Join([]string{us[0].Name, us[0].Token, us[0].Note}, " "))), "secretsecret") {
		t.Fatal("the legacy password must not survive parsing")
	}
}
