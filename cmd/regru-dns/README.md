# regru-dns

Small CLI that creates, lists, removes and verifies DNS records of one domain
(`example.com` by default) through the REG.RU API 2. Standard library only.

## One-time setup in the REG.RU account

1. Account -> Settings -> API: enable API access, set an **alternative API password**
   (use it instead of the account password) and add the IP address you run the tool from to the allowed list.
   Without that the API answers `ACCESS_DENIED_FROM_IP`.
2. The domain's NS must be REG.RU's (`ns1.reg.ru`, `ns2.reg.ru`); otherwise the API changes a zone nobody serves.
3. `copy cmd\regru-dns\regru.env.example regru.env` and fill in the login and password
   (`regru.env` is in `.gitignore`; environment variables `REGRU_USERNAME` / `REGRU_PASSWORD` work too).

## Commands

```
go build -o regru-dns.exe ./cmd/regru-dns

regru-dns check                                  # credentials, zone access, NS
regru-dns list [-sub node1]                      # records of the zone
regru-dns ensure -n node1 203.0.113.10           # dry run; drop -n to apply. A for IPv4, AAAA for IPv6, CNAME for a name
regru-dns ensure -type TXT _acme-challenge abc   # TXT is additive
regru-dns apply -n subdomains.txt                # lines: "SUB TARGET [TYPE]", # comments
regru-dns remove -n -type A -content 203.0.113.10 node1
regru-dns verify -want 203.0.113.10 node1        # confirms through the API that the record is in the zone
```

`ensure` is idempotent: an identical record is left alone, a different A/AAAA/CNAME value of the same
name is removed first (use `-keep` to keep it), so repeated runs do not pile up duplicates.
`remove` requires `-type`; without `-content` it deletes every record of that type on the name.
Flags go before the positional arguments. `@` is the apex, `*` the wildcard.

The API has no per-record TTL: the TTL is the zone's SOA value.
