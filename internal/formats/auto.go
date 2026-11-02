package formats

import (
	"fmt"

	"anypanel2/internal/links"
)

// AutoOptions describe the automatic node entry (see model.AutoNodeSettings).
type AutoOptions struct {
	Name        string
	Last        bool // after the servers instead of before them
	Position    int  // otherwise: before the first server whose Order is at least this
	URL         string
	IntervalSec int
	TimeoutSec  int
	ToleranceMs int
}

// autoAt is where the automatic node stands among the endpoints: an index into eps, len(eps) meaning after all of them.
func autoAt(eps []links.Endpoint, a *AutoOptions) int {
	if a.Last {
		return len(eps)
	}
	for i, e := range eps {
		if e.Order >= a.Position {
			return i
		}
	}
	return len(eps)
}

// autoMembers are the endpoints that take part in the automatic node.
func autoMembers(eps []links.Endpoint) []links.Endpoint {
	var out []links.Endpoint
	for _, e := range eps {
		if !e.NoAuto {
			out = append(out, e)
		}
	}
	return out
}

const nodeTagPrefix = "node-"

// autoConfig renders the automatic node for Xray clients: every server is an outbound, an observatory probes
// them all at once every interval and a balancer sends traffic through the one with the lowest delay among those that answer. When none
// answers the first server is used, so the connection does not go dark.
func autoConfig(eps []links.Endpoint, opt JSONOptions) (obj, error) {
	a := opt.Auto
	var outbounds []any
	var firstTag string
	frag, noises := parse(opt.Fragment), parse(opt.Noises)
	needFragment := false
	for i, e := range eps {
		proxy, err := outbound(e)
		if err != nil {
			return nil, err
		}
		tag := fmt.Sprintf("%s%d", nodeTagPrefix, i+1)
		proxy["tag"] = tag
		if firstTag == "" {
			firstTag = tag
		}
		// Mux and the Vision flow are mutually exclusive in Xray, so Vision links never get a mux block.
		if m := parse(opt.Mux); m != nil && e.Flow == "" {
			proxy["mux"] = m
		}
		if (frag != nil || noises != nil) && e.Security != "none" {
			proxy["streamSettings"].(obj)["sockopt"] = obj{"dialerProxy": "fragment"}
			needFragment = true
		}
		outbounds = append(outbounds, proxy)
	}
	if needFragment {
		settings := obj{}
		if frag != nil {
			settings["fragment"] = frag
		}
		if noises != nil {
			settings["noises"] = noises
		}
		outbounds = append(outbounds, obj{"tag": "fragment", "protocol": "freedom", "settings": settings})
	}
	outbounds = append(outbounds,
		obj{"tag": "direct", "protocol": "freedom", "settings": obj{"domainStrategy": "UseIP"}},
		obj{"tag": "block", "protocol": "blackhole"})

	rules, strategy := baseRules(opt, true)
	rules = append(rules, obj{"type": "field", "network": "tcp,udp", "balancerTag": "auto"})

	return obj{
		"remarks": a.Name,
		"log":     obj{"loglevel": "warning"},
		"dns":     clientDNS(opt),
		"stats":   obj{},
		"observatory": obj{
			"subjectSelector":   []string{nodeTagPrefix},
			"probeUrl":          a.URL,
			"probeInterval":     fmt.Sprintf("%ds", a.IntervalSec),
			"enableConcurrency": true,
		},
		"inbounds":  []any{socksIn(), httpIn()},
		"outbounds": outbounds,
		"routing": obj{
			"domainStrategy": strategy,
			"balancers": []any{obj{
				"tag": "auto", "selector": []string{nodeTagPrefix}, "strategy": obj{"type": "leastPing"}, "fallbackTag": firstTag,
			}},
			"rules": rules,
		},
	}, nil
}
