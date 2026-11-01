package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"anypanel2/internal/agent"
	"anypanel2/internal/model"
	"anypanel2/internal/nodeinstall"
	"anypanel2/internal/panel"
	"anypanel2/internal/xray"
)

type stubInstaller struct{}

func (stubInstaller) Check(context.Context, nodeinstall.Target, int, int, string) (*nodeinstall.Report, error) {
	return &nodeinstall.Report{OK: true, HostKey: strings.Repeat("cd", 32), OS: "Ubuntu 24.04", Arch: "x86_64", Root: true,
		Problems: []string{}, Warnings: []string{}, Listening: []int{22}}, nil
}
func (stubInstaller) Install(_ context.Context, o nodeinstall.Options, log func(string)) (*nodeinstall.Result, error) {
	log("установка")
	return &nodeinstall.Result{Fingerprint: strings.Repeat("ab", 32), HostKey: strings.Repeat("cd", 32), PrivateKey: "PRIVATE-KEY-MATERIAL"}, nil
}
func (stubInstaller) Upgrade(context.Context, nodeinstall.Target, func(string)) error { return nil }
func (stubInstaller) OpenFirewall(context.Context, nodeinstall.Target, int, []int, func(string)) error {
	return nil
}

func TestNodeSetupEndpoints(t *testing.T) {
	e := newEnv(t, false)
	e.svc.Installer = stubInstaller{}
	e.svc.NodeClient = func(model.Server) panel.NodeAPI { return deadNode{} }

	rec := do(e.h, http.MethodPost, "/api/servers/ssh-check", `{"host":"203.0.113.20","user":"root","password":"hunter2-pass"}`, e.cookie, nil)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"ok":true`) || !strings.Contains(rec.Body.String(), `"hostKey"`) {
		t.Fatalf("check: %d %s", rec.Code, rec.Body)
	}
	if strings.Contains(rec.Body.String(), "hunter2-pass") {
		t.Fatal("the password is not echoed back")
	}
	if rec := do(e.h, http.MethodPost, "/api/servers/ssh-check", `{"host":"203.0.113.20"}`, e.cookie, nil); rec.Code != 400 {
		t.Fatalf("no password: %d", rec.Code)
	}

	body := `{"host":"203.0.113.20","user":"root","password":"hunter2-pass","name":"Frankfurt","country":"DE",
	  "sni":"www.microsoft.com","realityDomain":"www.microsoft.com","createInbound":true,"addClients":true}`
	rec = do(e.h, http.MethodPost, "/api/servers/install", body, e.cookie, nil)
	if rec.Code != 200 {
		t.Fatalf("install: %d %s", rec.Code, rec.Body)
	}
	var job struct{ ID, Status string }
	_ = json.Unmarshal(rec.Body.Bytes(), &job)
	if job.ID == "" || strings.Contains(rec.Body.String(), "hunter2-pass") {
		t.Fatalf("%s", rec.Body)
	}

	var final struct {
		Status, Error, ServerID string
		Log                     []string
	}
	for i := 0; i < 200; i++ {
		rec = do(e.h, http.MethodGet, "/api/server-jobs/"+job.ID, "", e.cookie, nil)
		_ = json.Unmarshal(rec.Body.Bytes(), &final)
		if final.Status != "running" {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if rec.Code != 200 || final.Status != "done" || final.ServerID == "" || len(final.Log) == 0 {
		t.Fatalf("job: %d %s", rec.Code, rec.Body)
	}

	list := do(e.h, http.MethodGet, "/api/servers", "", e.cookie, nil).Body.String()
	for _, secret := range []string{"PRIVATE-KEY-MATERIAL", "hunter2-pass", "sshKey"} {
		if strings.Contains(list, secret) {
			t.Fatalf("%q must never be returned: %s", secret, list)
		}
	}
	for _, want := range []string{`"managed":true`, `"flag":"🇩🇪"`, `"country":"DE"`, `"pingMs"`, `"onlineClients"`} {
		if !strings.Contains(list, want) {
			t.Fatalf("missing %s in %s", want, list)
		}
	}

	rec = do(e.h, http.MethodGet, "/api/servers/"+final.ServerID+"/clients", "", e.cookie, nil)
	if rec.Code != 200 || strings.TrimSpace(rec.Body.String()) != "[]" {
		t.Fatalf("an empty list is [], not null: %d %s", rec.Code, rec.Body)
	}
	if rec := do(e.h, http.MethodGet, "/api/server-jobs/nope", "", e.cookie, nil); rec.Code != 404 {
		t.Fatalf("unknown job: %d", rec.Code)
	}

	rec = do(e.h, http.MethodPost, "/api/servers/"+final.ServerID+"/upgrade", "", e.cookie, nil)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"kind":"upgrade"`) {
		t.Fatalf("upgrade: %d %s", rec.Code, rec.Body)
	}
}

func TestNodeSetupIsSessionOnly(t *testing.T) {
	e := newEnv(t, false)
	e.svc.Installer = stubInstaller{}
	e.svc.NodeClient = func(model.Server) panel.NodeAPI { return deadNode{} }
	tok := e.newToken(t)
	for _, r := range []struct{ method, path, body string }{
		{http.MethodPost, "/api/servers/ssh-check", `{"host":"203.0.113.20","password":"x"}`},
		{http.MethodPost, "/api/servers/install", `{}`},
		{http.MethodPost, "/api/servers/abc/upgrade", ""},
		{http.MethodGet, "/api/servers/abc/clients", ""},
		{http.MethodGet, "/api/server-jobs/abc", ""},
	} {
		if rec := do(e.h, r.method, r.path, r.body, nil, bearer(tok)); rec.Code != 401 {
			t.Errorf("%s %s with the API token: want 401, got %d", r.method, r.path, rec.Code)
		}
		if rec := do(e.h, r.method, r.path, r.body, nil, nil); rec.Code != 401 {
			t.Errorf("%s %s anonymous: want 401, got %d", r.method, r.path, rec.Code)
		}
	}
}

func TestNodeInstallRejectsBadInputOverHTTP(t *testing.T) {
	e := newEnv(t, false)
	e.svc.Installer = stubInstaller{}
	e.svc.NodeClient = func(model.Server) panel.NodeAPI { return deadNode{} }
	for name, body := range map[string]string{
		"no password": `{"host":"203.0.113.20","name":"x"}`,
		"bad host":    `{"host":"no no","password":"p","name":"x"}`,
		"no name":     `{"host":"203.0.113.20","password":"p"}`,
		"bad country": `{"host":"203.0.113.20","password":"p","name":"x","country":"Germany"}`,
		"no domain":   `{"host":"203.0.113.20","password":"p","name":"x","createInbound":true}`,
	} {
		if rec := do(e.h, http.MethodPost, "/api/servers/install", body, e.cookie, nil); rec.Code != 400 {
			t.Errorf("%s: %d %s", name, rec.Code, rec.Body)
		}
	}
	if rec := do(e.h, http.MethodPost, "/api/servers/abc/upgrade", "", e.cookie, nil); rec.Code != 404 {
		t.Errorf("unknown server: %d", rec.Code)
	}
}

// deadNode is an agent nobody answers for: connections fail at once instead of timing out.
type deadNode struct{}

func (deadNode) Status(context.Context) (agent.Status, error) {
	return agent.Status{}, errors.New("connection refused")
}
func (deadNode) Check(context.Context, []byte) error { return errors.New("connection refused") }
func (deadNode) Apply(context.Context, []byte) error { return errors.New("connection refused") }
func (deadNode) Stats(context.Context, bool) (map[string]xray.Traffic, error) {
	return nil, errors.New("connection refused")
}
func (deadNode) Restart(context.Context) error { return errors.New("connection refused") }
func (deadNode) ObservedFingerprint() string   { return "" }

func TestPreviewAppEndpoint(t *testing.T) {
	e := newEnv(t, false)
	body := `{"subscription":{"remarkTemplate":"{{INBOUND}}","title":"{{EMAIL}} VPN","infoNode":true,"expiredTemplate":"gone {{EXPIRE_DATE}}"},"state":"expired"}`
	rec := do(e.h, http.MethodPost, "/api/settings/preview-app", body, e.cookie, nil)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"title":"alice VPN"`) || !strings.Contains(rec.Body.String(), `"onlyInfo":true`) || !strings.Contains(rec.Body.String(), `"infoName":"gone 20`) {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	if rec := do(e.h, http.MethodPost, "/api/settings/preview-app", `{"subscription":{},"state":"nope"}`, e.cookie, nil); rec.Code != 400 {
		t.Fatalf("unknown state: %d", rec.Code)
	}
	if rec := do(e.h, http.MethodPost, "/api/settings/preview-app", body, nil, nil); rec.Code != 401 {
		t.Fatalf("anonymous: %d", rec.Code)
	}
}

func (stubInstaller) SetupReality(context.Context, nodeinstall.Target, string, []int, func(string)) error {
	return nil
}

func (stubInstaller) SyncSNI(context.Context, nodeinstall.Target, []nodeinstall.SNISite, func(string)) error {
	return nil
}
