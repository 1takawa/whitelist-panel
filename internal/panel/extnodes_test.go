package panel

import (
	"context"
	"encoding/base64"
	"strings"
	"testing"
	"time"

	"anypanel2/internal/model"
	"anypanel2/internal/xray"
)

const (
	extVless  = "vless://aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee@nl.example.net:443?type=tcp&security=reality&sni=www.apple.com&fp=chrome&pbk=PUB&sid=ab&flow=xtls-rprx-vision#NL%20Reality"
	extTrojan = "trojan://secret@de.example.net:8443?type=ws&path=%2Fws&security=tls&sni=de.example.net#DE"
)

func TestExternalNodeCRUDAndDuplicates(t *testing.T) {
	ctx := context.Background()
	svc, _, _ := newService(t)

	n, err := svc.CreateExternalNode(ctx, model.ExternalNode{URI: extVless, Enable: true})
	if err != nil {
		t.Fatal(err)
	}
	if n.Name != "NL Reality" || n.Protocol != "vless" || n.Host != "nl.example.net" || n.Port != 443 || n.Security != "reality" || n.ID == "" {
		t.Fatalf("view: %+v", n)
	}

	// The same server under another display name is a duplicate.
	if _, err := svc.CreateExternalNode(ctx, model.ExternalNode{URI: strings.Replace(extVless, "NL%20Reality", "other", 1), Enable: true}); !isInvalid(err) {
		t.Fatalf("duplicate: %v", err)
	}
	for _, bad := range []string{"", "vmess://x", "garbage", "vless://nope@h:1"} {
		if _, err := svc.CreateExternalNode(ctx, model.ExternalNode{URI: bad}); !isInvalid(err) {
			t.Errorf("%q must be rejected: %v", bad, err)
		}
	}
	if _, err := svc.CreateExternalNode(ctx, model.ExternalNode{URI: extTrojan, Name: strings.Repeat("я", 101)}); !isInvalid(err) {
		t.Fatalf("long name: %v", err)
	}

	// Rename and disable; the stored link carries the new name.
	upd, err := svc.UpdateExternalNode(ctx, n.ID, model.ExternalNode{Name: "Нидерланды", Enable: false})
	if err != nil || upd.Name != "Нидерланды" || upd.Enable || !strings.HasSuffix(upd.URI, "#%D0%9D%D0%B8%D0%B4%D0%B5%D1%80%D0%BB%D0%B0%D0%BD%D0%B4%D1%8B") {
		t.Fatalf("%+v %v", upd, err)
	}
	t2, _ := svc.CreateExternalNode(ctx, model.ExternalNode{URI: extTrojan, Enable: true})
	if _, err := svc.UpdateExternalNode(ctx, t2.ID, model.ExternalNode{URI: extVless}); !isInvalid(err) {
		t.Fatalf("changing a link to an existing server: %v", err)
	}
	if err := svc.DeleteExternalNode(ctx, n.ID); err != nil {
		t.Fatal(err)
	}
	if list, _ := svc.ListExternalNodes(ctx); len(list) != 1 || list[0].ID != t2.ID {
		t.Fatalf("%+v", list)
	}
}

func TestImportLinesAndBase64Subscription(t *testing.T) {
	ctx := context.Background()
	svc, _, _ := newService(t)

	text := "# my servers\n" + extVless + "\n\n  " + extTrojan + "  \nvmess://abc\n" + extVless + "\nnot a link\n"
	res, err := svc.ImportExternalNodes(ctx, text)
	if err != nil {
		t.Fatal(err)
	}
	if res.Added != 2 || len(res.Problems) != 3 {
		t.Fatalf("%+v", res)
	}
	if res.Problems[0].Line != 5 || res.Problems[1].Line != 6 || res.Problems[2].Line != 7 {
		t.Fatalf("problems are reported with their line numbers: %+v", res.Problems)
	}

	svc2, _, _ := newService(t)
	body := base64.StdEncoding.EncodeToString([]byte(extVless + "\n" + extTrojan + "\n"))
	res, err = svc2.ImportExternalNodes(ctx, body)
	if err != nil || res.Added != 2 {
		t.Fatalf("a base64 subscription body is accepted: %+v %v", res, err)
	}
	if _, err := svc2.ImportExternalNodes(ctx, "   "); !isInvalid(err) {
		t.Fatalf("empty import: %v", err)
	}
}

func TestExternalNodesJoinOnlyActiveSubscribers(t *testing.T) {
	ctx := context.Background()
	exp := time.Now().Add(-time.Hour)
	for _, tc := range []struct {
		name   string
		client model.Client
		want   int // external links in the subscription
	}{
		{"active", model.Client{Email: "ok", Enable: true}, 2},
		{"expired", model.Client{Email: "old", Enable: true, ExpiryAt: &exp}, 0},
		{"exhausted", model.Client{Email: "full", Enable: true, TotalBytes: 10}, 0},
		{"disabled", model.Client{Email: "off", Enable: false}, 0},
	} {
		svc, fx, c := stage3Fixture(t, nil, tc.client)
		if tc.name == "exhausted" {
			fx.stats = map[string]xray.Traffic{"full": {Up: 10, Down: 10}}
			_ = svc.PollTraffic(ctx)
		}
		_, _ = svc.ImportExternalNodes(ctx, extVless+"\n"+extTrojan)
		_, _ = svc.CreateExternalNode(ctx, model.ExternalNode{URI: "trojan://x@off.example.net:443?security=tls", Enable: false})

		sb, err := svc.PublicSubscription(ctx, c.SubID)
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		ext := 0
		for _, l := range sb.Links {
			if strings.HasPrefix(l.InboundID, "ext:") {
				ext++
			}
		}
		if ext != tc.want {
			t.Errorf("%s: %d external links, want %d", tc.name, ext, tc.want)
		}
		if tc.want == 2 {
			body := strings.Join(sb.Lines, "\n")
			if !strings.Contains(body, "nl.example.net") || !strings.Contains(body, "de.example.net") || strings.Contains(body, "off.example.net") {
				t.Errorf("only enabled nodes are listed:\n%s", body)
			}
			if len(sb.Endpoints) != 3 { // the panel's own inbound + two external
				t.Errorf("Clash/JSON get the external servers too: %d endpoints", len(sb.Endpoints))
			}
		}
	}
}
