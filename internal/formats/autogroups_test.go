package formats

import (
	"testing"

	"anypanel2/internal/links"
	"gopkg.in/yaml.v3"
)

// groupEps are six servers with ids and places.
func groupEps() []links.Endpoint {
	list := eps()
	for i := range list {
		list[i].ID = string(rune('a' + i))
		list[i].Order = i
	}
	return list
}

func ruGroup() *AutoOptions {
	return &AutoOptions{Name: "🇷🇺 Авто РУ", URL: "https://x/", IntervalSec: 5, TimeoutSec: 2, Position: 4,
		Members: map[string]bool{"a": true, "c": true, "e": true}}
}

func remarksOf(cfgs []map[string]any) []string {
	var out []string
	for _, c := range cfgs {
		out = append(out, c["remarks"].(string))
	}
	return out
}

func TestXrayJSONSeveralAutoNodes(t *testing.T) {
	list := groupEps()
	main := autoOpt()
	main.Last = true
	main.Position = 100
	cfgs := decodeConfigs(t, mustJSON(t, list, JSONOptions{Auto: main, MoreAuto: []*AutoOptions{ruGroup()}, Info: []string{"info"}}))
	got := remarksOf(cfgs)
	want := []string{"info", "DE Reality", "WS", "gRPC", "XHTTP", "🇷🇺 Авто РУ", "Upgrade", "Plain", "🌐 Авто"}
	if len(got) != len(want) {
		t.Fatalf("got %v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("order: got %v, want %v", got, want)
		}
	}

	// The group holds exactly its members.
	for _, c := range cfgs {
		if c["remarks"] != "🇷🇺 Авто РУ" {
			continue
		}
		n := 0
		for _, o := range c["outbounds"].([]any) {
			if tag, _ := o.(map[string]any)["tag"].(string); len(tag) > 5 && tag[:5] == "node-" {
				n++
			}
		}
		if n != 3 {
			t.Fatalf("the group has 3 members, got %d outbounds", n)
		}
	}

	// A server that asked to stay out of the main node still takes part in a group that names it.
	excl := groupEps()
	excl[0].NoAuto, excl[2].NoAuto = true, true
	found := false
	for _, c := range decodeConfigs(t, mustJSON(t, excl, JSONOptions{Auto: main, MoreAuto: []*AutoOptions{ruGroup()}})) {
		if c["remarks"] == "🇷🇺 Авто РУ" {
			found = true
		}
	}
	if !found {
		t.Fatal("the group must stay")
	}

	// A group with fewer than two of the subscriber's servers is left out.
	solo := &AutoOptions{Name: "solo", URL: "https://x/", IntervalSec: 5, TimeoutSec: 2, Members: map[string]bool{"a": true, "zzz": true}}
	for _, r := range remarksOf(decodeConfigs(t, mustJSON(t, list, JSONOptions{MoreAuto: []*AutoOptions{solo}}))) {
		if r == "solo" {
			t.Fatal("a group needs two servers")
		}
	}
}

func TestSameGapAutoNodesFollowTheirPositions(t *testing.T) {
	list := groupEps()
	list[1].Order = 3 // the slots 1 and 2 of the list were taken by the two nodes
	a := &AutoOptions{Name: "first", URL: "https://x/", IntervalSec: 5, TimeoutSec: 2, Position: 1, Members: map[string]bool{"a": true, "b": true}}
	b := &AutoOptions{Name: "second", URL: "https://x/", IntervalSec: 5, TimeoutSec: 2, Position: 2, Members: map[string]bool{"a": true, "b": true}}
	got := remarksOf(decodeConfigs(t, mustJSON(t, list, JSONOptions{MoreAuto: []*AutoOptions{b, a}})))
	if got[1] != "first" || got[2] != "second" || got[3] != "WS" {
		t.Fatalf("%v", got)
	}
}

func TestClashSeveralAutoNodes(t *testing.T) {
	list := groupEps()
	main := autoOpt()
	raw, err := Clash(list, ClashOptions{Auto: main, MoreAuto: []*AutoOptions{ruGroup()}, Info: []string{"info"}})
	if err != nil {
		t.Fatal(err)
	}
	var cfg struct {
		Groups []struct {
			Name    string
			Type    string
			Proxies []string
		} `yaml:"proxy-groups"`
	}
	if err := yaml.Unmarshal(raw, &cfg); err != nil {
		t.Fatal(err)
	}
	if len(cfg.Groups) != 3 {
		t.Fatalf("select + two automatic nodes, got %d groups", len(cfg.Groups))
	}
	if cfg.Groups[2].Name != "🇷🇺 Авто РУ" || cfg.Groups[2].Type != "url-test" || len(cfg.Groups[2].Proxies) != 3 {
		t.Fatalf("%+v", cfg.Groups[2])
	}
	sel := cfg.Groups[0].Proxies
	idx := func(name string) int {
		for i, n := range sel {
			if n == name {
				return i
			}
		}
		return -1
	}
	if idx("🌐 Авто") < 0 || idx("🇷🇺 Авто РУ") < 0 || idx("🇷🇺 Авто РУ") != idx("Upgrade")-1 {
		t.Fatalf("the group stands in front of the server at its position: %v", sel)
	}

	// Two nodes with one name do not clash.
	dup := ruGroup()
	dup.Name = "🌐 Авто"
	raw, err = Clash(list, ClashOptions{Auto: main, MoreAuto: []*AutoOptions{dup}})
	if err != nil {
		t.Fatal(err)
	}
	cfg.Groups = nil
	_ = yaml.Unmarshal(raw, &cfg)
	if cfg.Groups[1].Name == cfg.Groups[2].Name {
		t.Fatal("group names must differ")
	}
}
