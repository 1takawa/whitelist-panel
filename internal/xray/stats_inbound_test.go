package xray

import "testing"

func TestParseStatsAllKeepsInboundCounters(t *testing.T) {
	data := []byte(`{"stat":[
		{"name":"user>>>bob>>>traffic>>>uplink","value":"5"},
		{"name":"inbound>>>in-x>>>traffic>>>uplink","value":"10"},
		{"name":"inbound>>>in-x>>>traffic>>>downlink","value":20},
		{"name":"inbound>>>api>>>traffic>>>uplink","value":99},
		{"name":"outbound>>>direct>>>traffic>>>uplink","value":7}]}`)
	all, err := ParseStatsAll(data)
	if err != nil {
		t.Fatal(err)
	}
	users, ins := SplitStats(all)
	if len(users) != 1 || users["bob"].Up != 5 {
		t.Fatalf("users: %+v", users)
	}
	if len(ins) != 1 || ins["in-x"] != (Traffic{Up: 10, Down: 20}) {
		t.Fatalf("the API inbound and outbounds are ignored: %+v", ins)
	}
	if only, _ := ParseStats(data); len(only) != 1 {
		t.Fatalf("ParseStats still returns users only: %+v", only)
	}
}

func TestParseVlessEncAndKeys(t *testing.T) {
	out := "Authentication: X25519, not Post-Quantum\n\"decryption\": \"mlkem768x25519plus.native.600s.A\"\n\"encryption\": \"mlkem768x25519plus.native.0rtt.B\"\n\nAuthentication: ML-KEM-768, Post-Quantum\n\"decryption\": \"mlkem768x25519plus.native.600s.C\"\n\"encryption\": \"mlkem768x25519plus.native.0rtt.D\"\n"
	x, m, err := ParseVlessEnc(out)
	if err != nil || x.Decryption == "" || m.Encryption != "mlkem768x25519plus.native.0rtt.D" {
		t.Fatalf("%+v %+v %v", x, m, err)
	}
	if _, _, err := ParseVlessEnc("nothing useful"); err == nil {
		t.Fatal("garbage must be rejected")
	}
	if _, _, err := ParseMLDSA65("Seed: a\n"); err == nil {
		t.Fatal("a missing verify key must be rejected")
	}
}

func TestParseECHRealOutput(t *testing.T) {
	// Captured from Xray 26.10.29: the values are on the line after their labels.
	out := "ECH config list: \nAF7+DQBaAAAgACCB\nECH server keys: \nACDZUhzp9FJc==\n"
	keys, list, err := ParseECH(out)
	if err != nil || keys != "ACDZUhzp9FJc==" || list != "AF7+DQBaAAAgACCB" {
		t.Fatalf("%q %q %v", keys, list, err)
	}
	if k, l, err := ParseECH("ECH config list: AAA\nECH server keys: BBB\n"); err != nil || k != "BBB" || l != "AAA" {
		t.Fatalf("same-line values: %q %q %v", k, l, err)
	}
	if _, _, err := ParseECH("ECH config list:\n"); err == nil {
		t.Fatal("missing keys must be rejected")
	}
}
