package xray

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"regexp"
	"strings"
	"time"
)

// Exec runs the Xray binary with the given arguments and returns its standard output. It serves the key generators
// (`xray vlessenc`, `xray mldsa65`, `xray tls ech`), which only the binary knows how to do.
func (m *Manager) Exec(ctx context.Context, args ...string) (string, error) {
	if !m.Installed() {
		return "", ErrNotInstalled
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	var stdout, stderr bytes.Buffer
	cmd := exec.CommandContext(ctx, m.Bin, args...)
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("xray %s: %w: %s", strings.Join(args, " "), err, tail(stderr.String(), 300))
	}
	return stdout.String(), nil
}

// VlessEnc is one authentication variant of VLESS encryption: the server's "decryption" and the clients' "encryption".
type VlessEnc struct {
	Decryption string `json:"decryption"`
	Encryption string `json:"encryption"`
}

var vlessLineRe = regexp.MustCompile(`"?(decryption|encryption)"?\s*[:=]\s*"([^"]+)"`)

// ParseVlessEnc reads the output of `xray vlessenc`: an X25519 variant and a post-quantum ML-KEM-768 variant.
func ParseVlessEnc(out string) (x25519, mlkem VlessEnc, err error) {
	var cur *VlessEnc
	for _, line := range strings.Split(out, "\n") {
		l := strings.ToLower(line)
		switch {
		case strings.Contains(l, "authentication") && strings.Contains(l, "x25519"):
			cur = &x25519
		case strings.Contains(l, "authentication") && strings.Contains(l, "ml-kem"):
			cur = &mlkem
		}
		if cur == nil {
			continue
		}
		if m := vlessLineRe.FindStringSubmatch(line); m != nil {
			if m[1] == "decryption" {
				cur.Decryption = m[2]
			} else {
				cur.Encryption = m[2]
			}
		}
	}
	if x25519.Decryption == "" || x25519.Encryption == "" || mlkem.Decryption == "" || mlkem.Encryption == "" {
		return x25519, mlkem, errors.New("unexpected output of `xray vlessenc`")
	}
	return x25519, mlkem, nil
}

// VlessEncMode switches the handshake mode ("native", "xorpub" or "random") of a VLESS encryption string:
// mlkem768x25519plus.<mode>.<ticket>[.<padding>…].<key>.
func VlessEncMode(s, mode string) string {
	parts := strings.Split(s, ".")
	if len(parts) < 4 || !strings.HasPrefix(parts[0], "mlkem768x25519plus") {
		return s
	}
	switch mode {
	case "native", "xorpub", "random":
		parts[1] = mode
	}
	return strings.Join(parts, ".")
}

var kvRe = regexp.MustCompile(`(?i)^\s*([A-Za-z0-9 _-]+?)\s*:\s*(\S+)\s*$`)

// ParseMLDSA65 reads the output of `xray mldsa65`: the secret seed (server) and the verification key (clients).
func ParseMLDSA65(out string) (seed, verify string, err error) {
	for _, line := range strings.Split(out, "\n") {
		if m := kvRe.FindStringSubmatch(line); m != nil {
			switch strings.ToLower(m[1]) {
			case "seed":
				seed = m[2]
			case "verify":
				verify = m[2]
			}
		}
	}
	if seed == "" || verify == "" {
		return "", "", errors.New("unexpected output of `xray mldsa65`")
	}
	return seed, verify, nil
}

// ParseECH reads the output of `xray tls ech`: the server's key set and the config list for clients. The value is
// printed on the line after its label:
//
//	ECH config list:
//	AF7+DQBa…
//	ECH server keys:
//	ACDZUhzp…
func ParseECH(out string) (serverKeys, configList string, err error) {
	lines := strings.Split(out, "\n")
	for i, line := range lines {
		label, value, _ := strings.Cut(line, ":")
		k := strings.ToLower(strings.TrimSpace(label))
		if !strings.HasPrefix(k, "ech ") {
			continue
		}
		value = strings.TrimSpace(value)
		for j := i + 1; value == "" && j < len(lines); j++ {
			value = strings.TrimSpace(lines[j])
			if strings.HasPrefix(strings.ToLower(value), "ech ") {
				value = ""
				break
			}
		}
		switch {
		case strings.Contains(k, "server") && strings.Contains(k, "key"):
			serverKeys = value
		case strings.Contains(k, "config"):
			configList = value
		}
	}
	if serverKeys == "" || configList == "" {
		return "", "", errors.New("unexpected output of `xray tls ech`")
	}
	return serverKeys, configList, nil
}
