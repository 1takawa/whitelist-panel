// Command regru-dns manages the DNS records of one domain through the REG.RU API 2. The panel does the same from its
// settings and from the node installer; this is the same code on the command line.
//
//	regru-dns [-domain D] check                     test the credentials and the access to the zone, show the zone's NS
//	regru-dns [-domain D] list [-sub S]             show the resource records
//	regru-dns [-domain D] ensure [-n] [-keep] SUB TARGET   make SUB point to TARGET (IPv4 -> A, IPv6 -> AAAA, name -> CNAME)
//	regru-dns [-domain D] ensure [-n] -type TXT SUB TEXT   add a TXT record unless it exists
//	regru-dns [-domain D] apply [-n] [-keep] FILE|-        ensure every "SUB TARGET [TYPE]" line of the file
//	regru-dns [-domain D] remove [-n] -type T [-content C] SUB   delete matching records
//	regru-dns [-domain D] verify [-type T] [-want V] SUB   confirm the record is in the REG.RU zone
//
// Credentials come from REGRU_USERNAME and REGRU_PASSWORD (the "alternative API password" from the REG.RU API settings)
// in the environment or in a .env / regru.env file (KEY=VALUE lines) in the working directory. If a certificate is
// uploaded in the API settings, also set REGRU_CERT and REGRU_KEY to its files. The domain defaults to REGRU_DOMAIN,
// then to example.com. Flags go before the positional arguments.
package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"

	"anypanel2/internal/regru"
)

const defaultDomain = "example.com"

func main() {
	if err := run(os.Args[1:], os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "error:", regru.Describe(err))
		os.Exit(1)
	}
}

func loadEnvFile(path string) {
	f, err := os.Open(path)
	if err != nil {
		return
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		k, v, ok := strings.Cut(line, "=")
		if line == "" || strings.HasPrefix(line, "#") || !ok {
			continue
		}
		k, v = strings.TrimSpace(k), strings.Trim(strings.TrimSpace(v), `"'`)
		if _, set := os.LookupEnv(k); !set {
			os.Setenv(k, v)
		}
	}
}

func newClient(domain string) (*regru.Client, error) {
	loadEnvFile("regru.env")
	loadEnvFile(".env")
	if domain == "" {
		domain = os.Getenv("REGRU_DOMAIN")
	}
	if domain == "" {
		domain = defaultDomain
	}
	cfg := regru.Config{Username: os.Getenv("REGRU_USERNAME"), Password: os.Getenv("REGRU_PASSWORD"), Zone: domain, BaseURL: os.Getenv("REGRU_API_URL")}
	for _, f := range []struct {
		env string
		dst *string
	}{{"REGRU_CERT", &cfg.CertPEM}, {"REGRU_KEY", &cfg.KeyPEM}} {
		if path := os.Getenv(f.env); path != "" {
			b, err := os.ReadFile(path)
			if err != nil {
				return nil, err
			}
			*f.dst = string(b)
		}
	}
	return regru.New(cfg)
}

func printRecords(w io.Writer, rs []regru.Record, sub string) {
	sort.SliceStable(rs, func(i, j int) bool {
		if rs[i].Sub != rs[j].Sub {
			return rs[i].Sub < rs[j].Sub
		}
		return rs[i].Type < rs[j].Type
	})
	n := 0
	for _, r := range rs {
		if sub != "" && !strings.EqualFold(r.Sub, sub) {
			continue
		}
		prio := ""
		if r.Prio != "" && r.Prio != "0" {
			prio = " prio=" + r.Prio
		}
		fmt.Fprintf(w, "%-24s %-6s %s%s\n", r.Sub, r.Type, r.Content, prio)
		n++
	}
	fmt.Fprintf(w, "%d record(s)\n", n)
}

func ensure(ctx context.Context, w io.Writer, c *regru.Client, rs []regru.Record, sub, typ, target string, keep, dry bool) error {
	acts, err := c.Ensure(ctx, rs, sub, typ, target, keep, dry)
	for _, a := range acts {
		label := fmt.Sprintf("%s %s -> %s", a.Type, a.Sub, a.Value)
		switch {
		case a.Op == "ok":
			fmt.Fprintf(w, "ok       %s (already present)\n", label)
		case a.Done:
			fmt.Fprintf(w, "%-8s %s\n", map[string]string{"add": "added", "remove": "removed"}[a.Op], label)
		default:
			fmt.Fprintf(w, "would    %s %s\n", a.Op, label)
		}
	}
	return err
}

func run(args []string, w io.Writer) error {
	global := flag.NewFlagSet("regru-dns", flag.ContinueOnError)
	domain := global.String("domain", "", "domain to manage (default $REGRU_DOMAIN or "+defaultDomain+")")
	if err := global.Parse(args); err != nil {
		return err
	}
	rest := global.Args()
	if len(rest) == 0 {
		return errors.New("usage: regru-dns [-domain D] check|list|ensure|apply|remove|verify ... (see the file header)")
	}
	cmd, rest := rest[0], rest[1:]
	fs := flag.NewFlagSet(cmd, flag.ContinueOnError)
	var (
		dry  = fs.Bool("n", false, "dry run: print what would change")
		keep = fs.Bool("keep", false, "ensure/apply: keep other values of the same type instead of replacing them")
		typ  = fs.String("type", "", "record type (A, AAAA, CNAME, TXT)")
		sub  = fs.String("sub", "", "list: only this subdomain")
		cont = fs.String("content", "", "remove: only the record with this content")
		want = fs.String("want", "", "verify: expected value")
	)
	if err := fs.Parse(rest); err != nil {
		return err
	}
	pos := fs.Args()
	c, err := newClient(*domain)
	if err != nil {
		return err
	}
	ctx := context.Background()

	switch cmd {
	case "check":
		if err := c.Check(ctx); err != nil {
			return fmt.Errorf("zone access to %s: %w", c.Zone(), err)
		}
		fmt.Fprintf(w, "ok  credentials accepted, zone of %s is manageable via the API\n", c.Zone())
		rs, err := c.Records(ctx)
		if err != nil {
			return err
		}
		for _, r := range rs {
			if r.Sub == "@" && r.Type == "NS" {
				fmt.Fprintf(w, "    NS %s\n", r.Content)
			}
		}
	case "list":
		rs, err := c.Records(ctx)
		if err != nil {
			return err
		}
		printRecords(w, rs, *sub)
	case "ensure":
		if len(pos) != 2 {
			return errors.New("usage: ensure [-n] [-keep] [-type T] SUB TARGET")
		}
		t := strings.ToUpper(*typ)
		if t == "" {
			t = regru.Kind(pos[1])
		}
		rs, err := c.Records(ctx)
		if err != nil {
			return err
		}
		return ensure(ctx, w, c, rs, pos[0], t, pos[1], *keep, *dry)
	case "apply":
		if len(pos) != 1 {
			return errors.New("usage: apply [-n] [-keep] FILE|-  (lines: SUB TARGET [TYPE]; # comments)")
		}
		in := io.Reader(os.Stdin)
		if pos[0] != "-" {
			f, err := os.Open(pos[0])
			if err != nil {
				return err
			}
			defer f.Close()
			in = f
		}
		rs, err := c.Records(ctx)
		if err != nil {
			return err
		}
		failed := 0
		sc := bufio.NewScanner(in)
		for n := 1; sc.Scan(); n++ {
			line, _, _ := strings.Cut(sc.Text(), "#")
			f := strings.Fields(line)
			if len(f) == 0 {
				continue
			}
			if len(f) < 2 || len(f) > 3 {
				fmt.Fprintf(w, "FAIL     line %d: want \"SUB TARGET [TYPE]\"\n", n)
				failed++
				continue
			}
			t := regru.Kind(f[1])
			if len(f) == 3 {
				t = strings.ToUpper(f[2])
			}
			if err := ensure(ctx, w, c, rs, f[0], t, f[1], *keep, *dry); err != nil {
				fmt.Fprintf(w, "FAIL     line %d: %v\n", n, regru.Describe(err))
				failed++
			}
		}
		if failed > 0 {
			return fmt.Errorf("%d line(s) failed", failed)
		}
	case "remove":
		if len(pos) != 1 || *typ == "" {
			return errors.New("usage: remove [-n] -type T [-content C] SUB  (without -content every record of that type on SUB goes)")
		}
		if err := regru.ValidSub(pos[0]); err != nil {
			return err
		}
		t := strings.ToUpper(*typ)
		if *dry {
			fmt.Fprintf(w, "would    remove %s %s %s\n", t, pos[0], *cont)
			return nil
		}
		if err := c.Remove(ctx, t, pos[0], *cont); err != nil {
			return err
		}
		fmt.Fprintf(w, "removed  %s %s %s\n", t, pos[0], *cont)
	case "verify":
		if len(pos) != 1 {
			return errors.New("usage: verify [-type T] [-want V] SUB  (asks the REG.RU zone through the API)")
		}
		t := strings.ToUpper(*typ)
		if t == "" {
			t = "A"
			if *want != "" {
				t = regru.Kind(*want)
			}
		}
		rs, err := c.Records(ctx)
		if err != nil {
			return err
		}
		var vals []string
		for _, r := range rs {
			if strings.EqualFold(r.Sub, pos[0]) && strings.EqualFold(r.Type, t) {
				vals = append(vals, r.Content)
			}
		}
		ok := len(vals) > 0
		if *want != "" {
			ok = false
			for _, v := range vals {
				ok = ok || strings.EqualFold(strings.TrimSuffix(v, "."), strings.TrimSuffix(*want, "."))
			}
		}
		status := "ok  "
		if !ok {
			status = "FAIL"
		}
		fmt.Fprintf(w, "%s %s %s: %s\n", status, t, c.FQDN(pos[0]), strings.Join(vals, ", "))
		if !ok {
			return errors.New("record not in the zone")
		}
	default:
		return fmt.Errorf("unknown command %q", cmd)
	}
	return nil
}
