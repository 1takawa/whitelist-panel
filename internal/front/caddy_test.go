package front

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"anypanel2/internal/model"
)

func ws(path string, port int) model.Inbound {
	return model.Inbound{Enable: true, Port: port, Network: model.NetWS, Security: model.SecNone,
		WS: model.PathHost{Path: path}, Public: model.Public{Security: model.SecTLS}}
}

func TestFronted(t *testing.T) {
	good := ws("/a", 1)
	if !Fronted(good) {
		t.Fatal("plain ws advertised as tls must be fronted")
	}
	off := good
	off.Enable = false
	direct := good
	direct.Public.Security = ""
	reality := model.Inbound{Enable: true, Network: model.NetTCP, Security: model.SecReality, Public: model.Public{Security: model.SecTLS}}
	tcp := model.Inbound{Enable: true, Network: model.NetTCP, Security: model.SecNone, Public: model.Public{Security: model.SecTLS}}
	for name, in := range map[string]model.Inbound{"disabled": off, "direct": direct, "reality": reality, "raw tcp": tcp} {
		if Fronted(in) {
			t.Errorf("%s must not be fronted", name)
		}
	}
}

func TestBuildRoutes(t *testing.T) {
	ins := []model.Inbound{
		ws("/zz", 10002),
		{Enable: true, Port: 10003, Network: model.NetGRPC, Security: model.SecNone, GRPC: model.GRPC{ServiceName: "svc"}, Public: model.Public{Security: model.SecTLS}},
		{Enable: true, Port: 10004, Network: model.NetXHTTP, Security: model.SecNone, XHTTP: model.XHTTP{Path: "/x"}, Public: model.Public{Security: model.SecTLS}},
		ws("/aa", 10001),
		{Enable: true, Port: 8443, Network: model.NetTCP, Security: model.SecReality}, // not fronted
	}
	got := BuildRoutes(ins)
	for _, want := range []string{
		"handle /aa {\n\t\treverse_proxy 127.0.0.1:10001\n\t}",
		"handle /zz {\n\t\treverse_proxy 127.0.0.1:10002\n\t}",
		"handle /svc/* {\n\t\treverse_proxy h2c://127.0.0.1:10003",
		"handle /x* {\n\t\treverse_proxy h2c://127.0.0.1:10004",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
	if strings.Contains(got, "8443") {
		t.Error("REALITY inbound must not get a Caddy route")
	}
	if strings.Index(got, "/aa") > strings.Index(got, "/zz") {
		t.Error("routes must be sorted for stable output")
	}
	if BuildRoutes(ins) != got {
		t.Error("output must be deterministic")
	}
}

func TestApplyWritesAndReloadsOnlyOnChange(t *testing.T) {
	path := filepath.Join(t.TempDir(), "routes", "inbounds.caddy")
	var calls [][]string
	c := NewCaddy(path, []string{"systemctl", "reload", "caddy"})
	c.run = func(_ context.Context, name string, args ...string) ([]byte, error) {
		calls = append(calls, append([]string{name}, args...))
		return nil, nil
	}
	ctx := context.Background()

	if err := c.Apply(ctx, []model.Inbound{ws("/a", 1)}); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(path)
	if !strings.Contains(string(data), "handle /a") || len(calls) != 1 {
		t.Fatalf("first apply must write and reload once: %q %v", data, calls)
	}
	if err := c.Apply(ctx, []model.Inbound{ws("/a", 1)}); err != nil || len(calls) != 1 {
		t.Fatalf("unchanged routes must not reload again: %v %v", err, calls)
	}
	if err := c.Apply(ctx, []model.Inbound{ws("/b", 1)}); err != nil || len(calls) != 2 {
		t.Fatalf("changed routes must reload: %v %v", err, calls)
	}
}

func TestApplyReportsReloadFailureAndRetries(t *testing.T) {
	c := NewCaddy(filepath.Join(t.TempDir(), "r.caddy"), []string{"reload"})
	fail := true
	c.run = func(context.Context, string, ...string) ([]byte, error) {
		if fail {
			return []byte("bad config"), errors.New("exit 1")
		}
		return nil, nil
	}
	ins := []model.Inbound{ws("/a", 1)}
	if err := c.Apply(context.Background(), ins); err == nil || !strings.Contains(err.Error(), "bad config") {
		t.Fatalf("want the reload error, got %v", err)
	}
	fail = false
	if err := c.Apply(context.Background(), ins); err != nil {
		t.Fatalf("a failed reload must be retried on the next apply: %v", err)
	}
}
