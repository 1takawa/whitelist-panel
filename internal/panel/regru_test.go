package panel

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"anypanel2/internal/model"
	"anypanel2/internal/store"
)

// fakeRegRu is a REG.API with one zone: it checks the login, can deny the caller's address and keeps the records.
type fakeRegRu struct {
	mu    sync.Mutex
	rrs   []map[string]string            // the zone example.test
	other map[string][]map[string]string // further zones of the account (a zone named broken.test is refused)
	deny  bool                           // answer ACCESS_DENIED_FROM_IP
}

func (f *fakeRegRu) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	fail := func(code string) {
		json.NewEncoder(w).Encode(map[string]any{"result": "error", "error_code": code, "error_text": code})
	}
	switch {
	case f.deny:
		fail("ACCESS_DENIED_FROM_IP")
		return
	case r.FormValue("username") != "me" || r.FormValue("password") != "secret":
		fail("PASSWORD_AUTH_FAILED")
		return
	}
	var in map[string]any
	json.Unmarshal([]byte(r.FormValue("input_data")), &in)
	s := func(k string) string { v, _ := in[k].(string); return v }
	zone := "example.test"
	if ds, _ := in["domains"].([]any); len(ds) > 0 {
		if m, _ := ds[0].(map[string]any); m != nil {
			zone, _ = m["dname"].(string)
		}
	}
	if zone == "broken.test" {
		fail("DOMAIN_NOT_FOUND")
		return
	}
	rrs := &f.rrs
	if zone != "example.test" {
		if f.other == nil {
			f.other = map[string][]map[string]string{}
		}
		list := f.other[zone]
		rrs = &list
		defer func() { f.other[zone] = *rrs }()
	}
	dom := map[string]any{"dname": zone, "result": "success"}
	switch strings.TrimPrefix(r.URL.Path, "/zone/") {
	case "get_resource_records":
		dom["rrs"] = *rrs
	case "add_alias", "add_aaaa":
		*rrs = append(*rrs, map[string]string{"subname": s("subdomain"), "rectype": "A", "content": s("ipaddr"), "prio": "0"})
	case "remove_record":
		var keep []map[string]string
		for _, x := range *rrs {
			if !(x["subname"] == s("subdomain") && x["rectype"] == s("record_type") && (s("content") == "" || x["content"] == s("content"))) {
				keep = append(keep, x)
			}
		}
		*rrs = keep
	}
	json.NewEncoder(w).Encode(map[string]any{"result": "success", "answer": map[string]any{"domains": []any{dom}}})
}

func regruSetup(t *testing.T) (*Service, *fakeRegRu) {
	t.Helper()
	svc, _, _ := newService(t)
	f := &fakeRegRu{rrs: []map[string]string{
		{"subname": "@", "rectype": "NS", "content": "ns1.reg.ru."},
		{"subname": "old", "rectype": "A", "content": "203.0.113.5"},
		{"subname": "panel", "rectype": "A", "content": "203.0.113.9"},
		{"subname": "_acme", "rectype": "TXT", "content": "x"},
		{"subname": "taken", "rectype": "CNAME", "content": "elsewhere.example."},
	}}
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)
	svc.RegRuBase = srv.URL
	svc.LookupHost = func(_ context.Context, host string) ([]string, error) {
		if host == "node.example.org" {
			return []string{"2001:db8::1", "198.51.100.20"}, nil
		}
		return nil, context.DeadlineExceeded
	}
	svc.PublicIP = func(context.Context) (string, error) { return "192.0.2.77", nil }
	return svc, f
}

func connect(t *testing.T, svc *Service) RegRuStatus {
	t.Helper()
	st, err := svc.SaveRegRu(context.Background(), RegRuSave{Username: "me", Password: "secret", Zone: "Example.Test."})
	if err != nil {
		t.Fatal(err)
	}
	return st
}

func TestRegRuSaveAndStatus(t *testing.T) {
	ctx := context.Background()
	svc, f := regruSetup(t)

	if st, _ := svc.RegRuStatus(ctx); st.Configured || st.Connected {
		t.Fatalf("nothing is saved yet: %+v", st)
	}
	for name, in := range map[string]RegRuSave{
		"no login":       {Password: "secret", Zone: "example.test"},
		"no password":    {Username: "me", Zone: "example.test"},
		"zone is a word": {Username: "me", Password: "secret", Zone: "example"},
		"cert alone":     {Username: "me", Password: "secret", Zone: "example.test", Cert: "-----BEGIN CERTIFICATE-----"},
		"broken pair":    {Username: "me", Password: "secret", Zone: "example.test", Cert: "nope", Key: "nope"},
	} {
		if _, err := svc.SaveRegRu(ctx, in); !isInvalid(err) {
			t.Errorf("%s: want a validation error, got %v", name, err)
		}
	}
	if st, _ := svc.RegRuStatus(ctx); st.PasswordSet {
		t.Fatal("a rejected form stores nothing")
	}

	st := connect(t, svc)
	if !st.Connected || st.Zone != "example.test" || st.Username != "me" || !st.PasswordSet || st.CertSet {
		t.Fatalf("status after connecting: %+v", st)
	}

	// The password never leaves through the settings API, and saving the settings form keeps the connection.
	set, _ := svc.GetSettings(ctx)
	if raw, _ := json.Marshal(set); strings.Contains(string(raw), "secret") {
		t.Fatal("the password leaked into the settings JSON")
	}
	cp := *set
	cp.RegRu = model.RegRuSettings{Username: "intruder", Zone: "evil.test"}
	if _, err := svc.PutSettings(ctx, cp); err != nil {
		t.Fatal(err)
	}
	if st, _ := svc.RegRuStatus(ctx); st.Username != "me" || !st.Connected {
		t.Fatalf("the settings form must not touch the connection: %+v", st)
	}

	// Keeping the stored password when the field is left empty.
	if st, err := svc.SaveRegRu(ctx, RegRuSave{Username: "me", Zone: "example.test"}); err != nil || !st.Connected {
		t.Fatalf("empty password keeps the stored one: %+v %v", st, err)
	}

	// REG.RU refuses this server: the user is told which address to allow.
	f.deny = true
	st, _ = svc.RegRuStatus(ctx)
	if st.Connected || st.ServerIP != "192.0.2.77" || !strings.Contains(st.Error, "разрешённые IP") {
		t.Fatalf("denied status: %+v", st)
	}
	f.deny = false

	// Wrong password: saved (so the form is not lost) but reported.
	st, err := svc.SaveRegRu(ctx, RegRuSave{Username: "me", Password: "wrong", Zone: "example.test"})
	if err != nil || st.Connected || !strings.Contains(st.Error, "пароль") {
		t.Fatalf("wrong password: %+v %v", st, err)
	}

	if err := svc.DisconnectRegRu(ctx); err != nil {
		t.Fatal(err)
	}
	if st, _ := svc.RegRuStatus(ctx); st.Configured || st.PasswordSet || st.Username != "" {
		t.Fatalf("disconnect forgets everything: %+v", st)
	}
	if _, err := svc.RegRuDomains(ctx); !isInvalid(err) {
		t.Fatalf("no connection, no domains: %v", err)
	}
}

func TestRegRuDomainsAndGenerate(t *testing.T) {
	ctx := context.Background()
	svc, f := regruSetup(t)
	connect(t, svc)

	set, _ := svc.GetSettings(ctx)
	set.PanelDomain = "panel.example.test"
	if _, err := svc.PutSettings(ctx, *set); err != nil {
		t.Fatal(err)
	}
	doms, err := svc.RegRuDomains(ctx)
	if err != nil || len(doms) != 1 || doms[0].Name != "old.example.test" || doms[0].IP != "203.0.113.5" {
		t.Fatalf("only the A record that is not the panel's own: %+v %v", doms, err)
	}

	// An address, a name that resolves (IPv4 preferred) and a server.
	g, err := svc.GenerateRegRuDomain(ctx, RegRuGenerateRequest{Host: "198.51.100.7"})
	if err != nil || !g.Created || g.IP != "198.51.100.7" || !strings.HasSuffix(g.Domain, ".example.test") {
		t.Fatalf("generate: %+v %v", g, err)
	}
	g2, err := svc.GenerateRegRuDomain(ctx, RegRuGenerateRequest{Host: "node.example.org", Label: "Node1"})
	if err != nil || g2.Domain != "node1.example.test" || g2.IP != "198.51.100.20" {
		t.Fatalf("label and resolving: %+v %v", g2, err)
	}
	again, err := svc.GenerateRegRuDomain(ctx, RegRuGenerateRequest{Host: "198.51.100.20", Label: "node1"})
	if err != nil || again.Created || again.Domain != g2.Domain {
		t.Fatalf("the same label and address again changes nothing: %+v %v", again, err)
	}
	if _, err := svc.GenerateRegRuDomain(ctx, RegRuGenerateRequest{Host: "198.51.100.99", Label: "node1"}); !isInvalid(err) {
		t.Fatalf("label is taken by another address: %v", err)
	}
	if _, err := svc.GenerateRegRuDomain(ctx, RegRuGenerateRequest{Host: "198.51.100.99", Label: "taken"}); !isInvalid(err) {
		t.Fatalf("label is taken by a CNAME: %v", err)
	}
	for _, bad := range []string{"-x", "a.b", "a b", "ü"} {
		if _, err := svc.GenerateRegRuDomain(ctx, RegRuGenerateRequest{Host: "198.51.100.1", Label: bad}); !isInvalid(err) {
			t.Errorf("label %q must be refused: %v", bad, err)
		}
	}
	if _, err := svc.GenerateRegRuDomain(ctx, RegRuGenerateRequest{Host: "nowhere.example.org"}); !isInvalid(err) {
		t.Fatalf("an unresolvable host: %v", err)
	}
	// The main server is found through the panel's own domain.
	set.PanelDomain = "node.example.org"
	if _, err := svc.PutSettings(ctx, *set); err != nil {
		t.Fatal(err)
	}
	if g, err := svc.GenerateRegRuDomain(ctx, RegRuGenerateRequest{}); err != nil || g.IP != "198.51.100.20" {
		t.Fatalf("main server address: %+v %v", g, err)
	}

	// Several names in a row never collide and each lands in the zone once.
	seen := map[string]bool{}
	for range 10 {
		g, err := svc.GenerateRegRuDomain(ctx, RegRuGenerateRequest{Host: "198.51.100.50"})
		if err != nil || seen[g.Domain] {
			t.Fatalf("duplicate or failed: %+v %v", g, err)
		}
		seen[g.Domain] = true
	}
	subs := map[string]int{}
	for _, r := range f.rrs {
		subs[r["subname"]]++
	}
	for s, n := range subs {
		if n > 1 && s != "@" {
			t.Errorf("%s appears %d times in the zone", s, n)
		}
	}
}

func TestRegRuSecretsInFullExportOnly(t *testing.T) {
	ctx := context.Background()
	svc, _ := regruSetup(t)
	_ = svc.Store.PutAdmin(ctx, store.Admin{Username: "boss", PasswordHash: "$argon2id$v=19$m=65536,t=3,p=2$c2FsdA$aGFzaA", CreatedAt: time.Now()})
	connect(t, svc)

	_, full, err := svc.ExportDB(ctx, ExportFull)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(full), `"regruPassword":"secret"`) || !strings.Contains(string(full), `"username":"me"`) {
		t.Fatal("the full copy must carry the REG.RU connection")
	}
	_, users, err := svc.ExportDB(ctx, ExportUsers)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(users), "secret") {
		t.Fatal("the users file must not carry the password")
	}

	// A full import restores the connection.
	if err := svc.DisconnectRegRu(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.ImportDB(ctx, full, ImportOptions{}); err != nil {
		t.Fatal(err)
	}
	if st, _ := svc.RegRuStatus(ctx); !st.Connected {
		t.Fatalf("import restores the connection: %+v", st)
	}
}
