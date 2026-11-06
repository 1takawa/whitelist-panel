package regru

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// fakeAPI serves the zone/* functions from an in-memory record list.
type fakeAPI struct {
	rrs   []map[string]string
	calls []string
	fail  map[string]string // function -> error code answered for the domain
}

func (f *fakeAPI) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	fn := strings.TrimPrefix(r.URL.Path, "/zone/")
	if r.Method != http.MethodPost || r.URL.RawQuery != "" || r.FormValue("username") != "u" || r.FormValue("password") != "p" {
		json.NewEncoder(w).Encode(map[string]any{"result": "error", "error_code": "PASSWORD_AUTH_FAILED", "error_text": "bad"})
		return
	}
	var in map[string]any
	json.Unmarshal([]byte(r.FormValue("input_data")), &in)
	s := func(k string) string { v, _ := in[k].(string); return v }
	f.calls = append(f.calls, fn+" "+s("subdomain")+" "+s("ipaddr")+s("canonical_name")+s("text")+s("content"))
	dom := map[string]any{"dname": "example.test", "result": "success"}
	if code := f.fail[fn]; code != "" {
		dom = map[string]any{"dname": "example.test", "result": "error", "error_code": code, "error_text": "refused"}
	}
	switch fn {
	case "get_resource_records":
		dom["rrs"] = f.rrs
	case "add_alias":
		f.rrs = append(f.rrs, map[string]string{"subname": s("subdomain"), "rectype": "A", "content": s("ipaddr"), "prio": "0"})
	case "remove_record":
		var keep []map[string]string
		for _, x := range f.rrs {
			if !(x["subname"] == s("subdomain") && x["rectype"] == s("record_type") && (s("content") == "" || x["content"] == s("content"))) {
				keep = append(keep, x)
			}
		}
		f.rrs = keep
	}
	json.NewEncoder(w).Encode(map[string]any{"result": "success", "answer": map[string]any{"domains": []any{dom}}})
}

func TestUnreachableRegRuIsExplainedAsAProblemOfThisServer(t *testing.T) {
	// A server that accepts the connection and never answers: what a block of the address looks like.
	block := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { <-block }))
	t.Cleanup(func() { close(block); srv.Close() })
	c, err := New(Config{Username: "u", Password: "p", Zone: "example.test", BaseURL: srv.URL})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	err = c.Check(ctx)
	var ce *ConnError
	if !errors.As(err, &ce) || !ce.Timeout() {
		t.Fatalf("a timeout is a connection error: %v", err)
	}
	msg := Describe(err)
	for _, want := range []string{"этот сервер не может подключиться к reg.ru", "не отвечает", "HTTPS_PROXY"} {
		if !strings.Contains(msg, want) {
			t.Errorf("%q missing from %q", want, msg)
		}
	}
	for _, bad := range []string{"context deadline exceeded", "Post ", "http://"} {
		if strings.Contains(msg, bad) {
			t.Errorf("the technical text %q must not reach the user: %q", bad, msg)
		}
	}

	// A refused connection says why, without repeating the address.
	dead := httptest.NewServer(http.NotFoundHandler())
	url := dead.URL
	dead.Close()
	c2, _ := New(Config{Username: "u", Password: "p", Zone: "example.test", BaseURL: url})
	if m := Describe(c2.Check(context.Background())); !strings.HasPrefix(m, "этот сервер не может подключиться к reg.ru: ") || strings.Contains(m, "Post ") {
		t.Errorf("%q", m)
	}
}

func newTest(t *testing.T, f *fakeAPI) *Client {
	t.Helper()
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)
	c, err := New(Config{Username: "u", Password: "p", Zone: "Example.Test", BaseURL: srv.URL})
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestEnsure(t *testing.T) {
	f := &fakeAPI{rrs: []map[string]string{
		{"subname": "node1", "rectype": "A", "content": "1.1.1.1", "prio": "0"},
		{"subname": "same", "rectype": "A", "content": "2.2.2.2", "prio": "0"},
	}}
	c := newTest(t, f)
	ctx := context.Background()
	rs, err := c.Records(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var ops []string
	for _, tc := range [][3]string{{"new", "A", "3.3.3.3"}, {"same", "A", "2.2.2.2"}, {"node1", "A", "9.9.9.9"}} {
		acts, err := c.Ensure(ctx, rs, tc[0], tc[1], tc[2], false, false)
		if err != nil {
			t.Fatal(err)
		}
		for _, a := range acts {
			ops = append(ops, a.Op+" "+a.Sub+" "+a.Value)
		}
	}
	if got, want := strings.Join(ops, "; "), "add new 3.3.3.3; ok same 2.2.2.2; remove node1 1.1.1.1; add node1 9.9.9.9"; got != want {
		t.Errorf("ops = %q\nwant  %q", got, want)
	}
	after, _ := c.Records(ctx)
	if len(after) != 3 {
		t.Errorf("want 3 records after, got %v", after)
	}
}

func TestDryRunChangesNothing(t *testing.T) {
	f := &fakeAPI{rrs: []map[string]string{{"subname": "a", "rectype": "A", "content": "1.1.1.1"}}}
	c := newTest(t, f)
	ctx := context.Background()
	rs, _ := c.Records(ctx)
	acts, err := c.Ensure(ctx, rs, "a", "A", "2.2.2.2", false, true)
	if err != nil || len(acts) != 2 || acts[0].Done || acts[1].Done {
		t.Fatalf("acts=%v err=%v", acts, err)
	}
	if len(f.calls) != 1 { // only the get_resource_records above
		t.Errorf("dry run called the API: %v", f.calls)
	}
}

func TestErrors(t *testing.T) {
	f := &fakeAPI{fail: map[string]string{"add_alias": "CNAME_ANDOTHERDATA"}}
	c := newTest(t, f)
	var ae *Error
	if err := c.Add(context.Background(), "A", "x", "1.2.3.4"); !errors.As(err, &ae) || ae.Code != "CNAME_ANDOTHERDATA" {
		t.Fatalf("want CNAME_ANDOTHERDATA, got %v", err)
	}
	c.pass = "wrong"
	err := c.Check(context.Background())
	if !errors.As(err, &ae) || ae.Code != "PASSWORD_AUTH_FAILED" || !strings.Contains(Describe(err), "пароль") {
		t.Errorf("want a readable password failure, got %v / %q", err, Describe(err))
	}
}

func TestNewValidates(t *testing.T) {
	for _, cfg := range []Config{
		{Password: "p", Zone: "example.test"},
		{Username: "u", Zone: "example.test"},
		{Username: "u", Password: "p", Zone: "nodot"},
		{Username: "u", Password: "p", Zone: "example.test", CertPEM: "not a cert", KeyPEM: "not a key"},
	} {
		if _, err := New(cfg); err == nil {
			t.Errorf("New(%+v) accepted a bad configuration", cfg)
		}
	}
}

func TestKindAndSub(t *testing.T) {
	for in, want := range map[string]string{"1.2.3.4": "A", "2001:db8::1": "AAAA", "node.example.com": "CNAME"} {
		if got := Kind(in); got != want {
			t.Errorf("Kind(%q)=%s want %s", in, got, want)
		}
	}
	c := &Client{zone: "example.test"}
	for in, want := range map[string]string{"example.test": "@", "A.Example.Test.": "a", "x.y.example.test": "x.y"} {
		if got, ok := c.Sub(in); !ok || got != want {
			t.Errorf("Sub(%q)=%q,%v want %q", in, got, ok, want)
		}
	}
	if _, ok := c.Sub("other.org"); ok {
		t.Error("a name of another zone was accepted")
	}
}
