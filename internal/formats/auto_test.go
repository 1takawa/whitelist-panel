package formats

import (
	"encoding/base64"
	"strings"
	"testing"

	"anypanel2/internal/links"
	"gopkg.in/yaml.v3"
)

func autoOpt() *AutoOptions {
	return &AutoOptions{Name: "🌐 Авто", URL: "https://www.gstatic.com/generate_204", IntervalSec: 5, TimeoutSec: 2, ToleranceMs: 30}
}

func TestXrayJSONAutoNode(t *testing.T) {
	list := eps()
	raw := mustJSON(t, list, JSONOptions{Auto: autoOpt(), Info: []string{"info"}})
	cfgs := decodeConfigs(t, raw)
	if len(cfgs) != len(list)+2 {
		t.Fatalf("one config per server plus the info node plus the automatic node, got %d", len(cfgs))
	}
	if cfgs[0]["remarks"] != "info" || cfgs[1]["remarks"] != "🌐 Авто" {
		t.Fatalf("the automatic node follows the info node: %v %v", cfgs[0]["remarks"], cfgs[1]["remarks"])
	}
	a := cfgs[1]
	obs := a["observatory"].(map[string]any)
	if obs["probeInterval"] != "5s" || obs["probeUrl"] != "https://www.gstatic.com/generate_204" || obs["enableConcurrency"] != true {
		t.Fatalf("observatory: %+v", obs)
	}
	if _, ok := a["burstObservatory"]; ok {
		t.Fatal("the plain observatory reacts faster than the burst one (measured), so only it is used")
	}
	outs := a["outbounds"].([]any)
	tags := map[string]bool{}
	for _, o := range outs {
		tags[o.(map[string]any)["tag"].(string)] = true
	}
	for i := range list {
		if !tags["node-"+string(rune('1'+i))] {
			t.Fatalf("outbound node-%d missing: %v", i+1, tags)
		}
	}
	if !tags["direct"] || !tags["block"] {
		t.Fatal("direct and block outbounds are required")
	}
	routing := a["routing"].(map[string]any)
	bal := routing["balancers"].([]any)[0].(map[string]any)
	if bal["tag"] != "auto" || bal["fallbackTag"] != "node-1" || bal["strategy"].(map[string]any)["type"] != "leastPing" {
		t.Fatalf("balancer: %+v", bal)
	}
	rules := routing["rules"].([]any)
	if last := rules[len(rules)-1].(map[string]any); last["balancerTag"] != "auto" {
		t.Fatalf("the catch-all rule must use the balancer: %+v", last)
	}
	if a["stats"] == nil || a["inbounds"] == nil {
		t.Fatal("stats (for the observatory) and inbounds (INCY needs them to recognise a full config) are required")
	}

	last := decodeConfigs(t, mustJSON(t, list, JSONOptions{Auto: &AutoOptions{Name: "A", Last: true, URL: "https://x/", IntervalSec: 5, TimeoutSec: 2}}))
	if last[len(last)-1]["remarks"] != "A" {
		t.Fatal("Last puts the automatic node after the servers")
	}
}

func TestXrayJSONAutoNodeNeedsTwoServersAndHonoursExclusion(t *testing.T) {
	list := eps()
	one := mustJSON(t, list[:1], JSONOptions{Auto: autoOpt()})
	if strings.Contains(string(one), "observatory") {
		t.Fatal("a single server needs no automatic node")
	}
	ex := append([]links.Endpoint{}, list[:3]...)
	ex[1].NoAuto, ex[2].NoAuto = true, true
	if strings.Contains(string(mustJSON(t, ex, JSONOptions{Auto: autoOpt()})), "observatory") {
		t.Fatal("only one server takes part: no automatic node")
	}
	ex[2].NoAuto = false
	cfgs := decodeConfigs(t, mustJSON(t, ex, JSONOptions{Auto: autoOpt()}))
	n := 0
	for _, o := range cfgs[0]["outbounds"].([]any) {
		if strings.HasPrefix(o.(map[string]any)["tag"].(string), "node-") {
			n++
		}
	}
	if n != 2 {
		t.Fatalf("excluded servers stay out of the group, got %d members", n)
	}
	if len(cfgs) != 4 { // auto + 3 servers
		t.Fatalf("excluded servers are still listed on their own: %d", len(cfgs))
	}
	if strings.Contains(string(mustJSON(t, list, JSONOptions{})), "observatory") {
		t.Fatal("no automatic node unless it is enabled")
	}
}

func TestXrayJSONAutoNodeKeepsFragmentAndMux(t *testing.T) {
	list := []links.Endpoint{eps()[1], eps()[0]} // WS over TLS, REALITY with the vision flow
	opt := JSONOptions{Auto: autoOpt(), Mux: `{"enabled":true,"concurrency":8}`, Fragment: `{"packets":"tlshello","length":"100-200","interval":"10-20"}`}
	cfgs := decodeConfigs(t, mustJSON(t, list, opt))
	var withMux, fragment int
	for _, o := range cfgs[0]["outbounds"].([]any) {
		m := o.(map[string]any)
		if m["mux"] != nil {
			withMux++
		}
		if m["tag"] == "fragment" {
			fragment++
		}
	}
	if withMux != 1 || fragment != 1 {
		t.Fatalf("mux only on the server without vision flow, one fragment outbound: mux=%d fragment=%d", withMux, fragment)
	}
}

func TestClashAutoNodeGroup(t *testing.T) {
	list := eps()
	list[1].NoAuto = true
	raw, err := Clash(list, ClashOptions{Auto: autoOpt()})
	if err != nil {
		t.Fatal(err)
	}
	var cfg struct {
		Groups []struct {
			Name      string   `yaml:"name"`
			Type      string   `yaml:"type"`
			Proxies   []string `yaml:"proxies"`
			URL       string   `yaml:"url"`
			Interval  int      `yaml:"interval"`
			Timeout   int      `yaml:"timeout"`
			Tolerance int      `yaml:"tolerance"`
			Lazy      *bool    `yaml:"lazy"`
		} `yaml:"proxy-groups"`
	}
	if err := yaml.Unmarshal(raw, &cfg); err != nil {
		t.Fatal(err)
	}
	var auto, sel = cfg.Groups[1], cfg.Groups[0]
	if auto.Name != "🌐 Авто" || auto.Type != "url-test" || auto.Interval != 5 || auto.Timeout != 2000 || auto.Tolerance != 30 || auto.Lazy == nil || *auto.Lazy {
		t.Fatalf("group: %+v", auto)
	}
	if len(auto.Proxies) != len(list)-1 {
		t.Fatalf("the excluded server must not be in the group: %v", auto.Proxies)
	}
	if sel.Proxies[0] != "🌐 Авто" {
		t.Fatalf("the automatic node is the first choice: %v", sel.Proxies)
	}
	raw, _ = Clash(list, ClashOptions{Auto: &AutoOptions{Name: "A", Last: true, URL: "https://x/", IntervalSec: 5, TimeoutSec: 2}})
	_ = yaml.Unmarshal(raw, &cfg)
	if got := cfg.Groups[0].Proxies; got[len(got)-2] != "A" || got[len(got)-1] != "DIRECT" {
		t.Fatalf("Last puts the group after the servers: %v", got)
	}
	plain, _ := Clash(eps(), ClashOptions{})
	if !strings.Contains(string(plain), "interval: 300") {
		t.Fatal("without the feature the old group stays")
	}
}

func TestAutoNodeStandsWhereItWasPlaced(t *testing.T) {
	list := eps()
	for i := range list {
		list[i].Order = i * 10
	}
	names := func(cfgs []map[string]any) []string {
		var out []string
		for _, c := range cfgs {
			out = append(out, c["remarks"].(string))
		}
		return out
	}
	for _, c := range []struct {
		pos  int
		last bool
		at   int
	}{{0, false, 0}, {5, false, 1}, {10, false, 1}, {11, false, 2}, {999, false, len(list)}, {0, true, len(list)}} {
		a := autoOpt()
		a.Position, a.Last = c.pos, c.last
		got := names(decodeConfigs(t, mustJSON(t, list, JSONOptions{Auto: a, Info: []string{"info"}})))
		if len(got) != len(list)+2 || got[1+c.at] != a.Name {
			t.Fatalf("position %d last %v: want the node at %d, got %v", c.pos, c.last, c.at, got)
		}
	}
	raw, err := Clash(list, ClashOptions{Auto: &AutoOptions{Name: "A", Position: 10, URL: "https://x/", IntervalSec: 5, TimeoutSec: 2}})
	if err != nil {
		t.Fatal(err)
	}
	var cfg struct {
		Groups []struct {
			Name    string   `yaml:"name"`
			Proxies []string `yaml:"proxies"`
		} `yaml:"proxy-groups"`
	}
	if err := yaml.Unmarshal(raw, &cfg); err != nil {
		t.Fatal(err)
	}
	sel := cfg.Groups[0].Proxies
	if len(sel) < 3 || sel[1] != "A" {
		t.Fatalf("the group stands second among the choices: %v", sel)
	}
}

const testProfile = `{"Name":"RoscomVPN","RouteOrder":"block-proxy-direct","DomainStrategy":"IPIfNonMatch",
 "DirectSites":["geosite:private","geosite:category-ru","geosite:whitelist","geosite:microsoft"],
 "DirectIp":["geoip:private","geoip:direct"],
 "ProxySites":["geosite:youtube","geosite:telegram"],
 "BlockSites":["geosite:win-spy","geosite:torrent","geosite:category-ads"]}`

func TestRoutingProfileIsWrittenIntoTheConfigs(t *testing.T) {
	link := "happ://routing/onadd/" + base64.StdEncoding.EncodeToString([]byte(testProfile))
	for name, raw := range map[string]string{"json": testProfile, "link": link} {
		cfgs := decodeConfigs(t, mustJSON(t, eps(), JSONOptions{Profile: raw}))
		rules := cfgs[0]["routing"].(map[string]any)["rules"].([]any)
		var domains []string
		var tags []string
		for _, r := range rules {
			m := r.(map[string]any)
			if d, ok := m["domain"].([]any); ok {
				for _, x := range d {
					domains = append(domains, x.(string))
				}
				tags = append(tags, m["outboundTag"].(string))
			}
		}
		all := strings.Join(domains, ",")
		for _, want := range []string{"geosite:category-ru", "geosite:microsoft", "domain:ru", "domain:su", "domain:xn--p1ai", "geosite:youtube"} {
			if !strings.Contains(all, want) {
				t.Fatalf("%s: %s missing from %s", name, want, all)
			}
		}
		for _, bad := range []string{"whitelist", "torrent", "category-ads", "geoip:direct", "win-spy"} {
			if strings.Contains(all, bad) || strings.Contains(string(mustJSON(t, eps(), JSONOptions{Profile: raw})), bad) {
				t.Fatalf("%s: %s is not in every geo file and must not be written", name, bad)
			}
		}
		if got := strings.Join(tags, ","); got != "proxy,direct" {
			t.Fatalf("%s: the profile's order (proxy before direct; block had nothing safe): %s", name, got)
		}
		if cfgs[0]["routing"].(map[string]any)["domainStrategy"] != "AsIs" {
			t.Fatalf("%s: no IP list that needs resolving, so no resolving", name)
		}
	}

	// Without a profile, or with routing switched off, the configs are as before.
	plain := string(mustJSON(t, eps(), JSONOptions{}))
	for _, raw := range []string{"", "happ://routing/off", "garbage"} {
		if got := string(mustJSON(t, eps(), JSONOptions{Profile: raw})); got != plain {
			t.Fatalf("%q must change nothing", raw)
		}
	}
	// The automatic node has no proxy outbound: its balancer takes that place.
	auto := decodeConfigs(t, mustJSON(t, eps(), JSONOptions{Profile: testProfile, Auto: autoOpt()}))[0]
	rules := auto["routing"].(map[string]any)["rules"].([]any)
	found := false
	for _, r := range rules {
		m := r.(map[string]any)
		if _, ok := m["domain"]; ok && m["balancerTag"] == "auto" {
			found = true
		}
		if m["outboundTag"] == "proxy" {
			t.Fatalf("the automatic node has no proxy outbound: %+v", m)
		}
	}
	if !found {
		t.Fatal("proxy rules of the profile go to the balancer")
	}
}
