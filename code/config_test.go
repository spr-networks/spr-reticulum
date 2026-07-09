package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func validTestConfig() Config {
	cfg := defaultConfig()
	cfg.TCPClientInterfaces = []TCPClientInterface{
		{Name: "Beleth RNS Hub", Enabled: true, TargetHost: "rns.beleth.net", TargetPort: 4242},
	}
	return cfg
}

func TestValidateAcceptsDefaults(t *testing.T) {
	cfg := defaultConfig()
	if err := cfg.Validate(); err != nil {
		t.Fatalf("default config should validate: %v", err)
	}
	cfg = validTestConfig()
	if err := cfg.Validate(); err != nil {
		t.Fatalf("valid config should validate: %v", err)
	}
}

func TestValidateRejectsBadInput(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*Config)
	}{
		{"empty name", func(c *Config) { c.TCPClientInterfaces[0].Name = "" }},
		{"ini injection in name", func(c *Config) { c.TCPClientInterfaces[0].Name = "x]]\n[[evil" }},
		{"brackets in name", func(c *Config) { c.TCPClientInterfaces[0].Name = "evil]section" }},
		{"reserved name", func(c *Config) { c.TCPClientInterfaces[0].Name = "TCPServer" }},
		{"newline in host", func(c *Config) { c.TCPClientInterfaces[0].TargetHost = "a.com\nenable_transport = True" }},
		{"space in host", func(c *Config) { c.TCPClientInterfaces[0].TargetHost = "a b.com" }},
		{"empty host", func(c *Config) { c.TCPClientInterfaces[0].TargetHost = "" }},
		{"port zero", func(c *Config) { c.TCPClientInterfaces[0].TargetPort = 0 }},
		{"port too big", func(c *Config) { c.TCPClientInterfaces[0].TargetPort = 65536 }},
		{"negative port", func(c *Config) { c.TCPClientInterfaces[0].TargetPort = -1 }},
		{"server port invalid", func(c *Config) { c.TCPServerInterface.ListenPort = 0 }},
		{"log level too big", func(c *Config) { c.LogLevel = 8 }},
		{"log level negative", func(c *Config) { c.LogLevel = -1 }},
		{"duplicate names", func(c *Config) {
			c.TCPClientInterfaces = append(c.TCPClientInterfaces, c.TCPClientInterfaces[0])
		}},
		{"too many interfaces", func(c *Config) {
			for i := 0; i <= MaxTCPClientInterfaces; i++ {
				c.TCPClientInterfaces = append(c.TCPClientInterfaces, TCPClientInterface{
					Name: strings.Repeat("x", i+1), TargetHost: "a.com", TargetPort: 4242,
				})
			}
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := validTestConfig()
			tc.mutate(&cfg)
			if err := cfg.Validate(); err == nil {
				t.Fatalf("expected validation error for %s", tc.name)
			}
		})
	}
}

func TestValidateAcceptsIPv6Host(t *testing.T) {
	cfg := validTestConfig()
	cfg.TCPClientInterfaces[0].TargetHost = "2001:db8::1"
	if err := cfg.Validate(); err != nil {
		t.Fatalf("IPv6 host should validate: %v", err)
	}
	cfg.TCPClientInterfaces[0].TargetHost = "::1"
	if err := cfg.Validate(); err != nil {
		t.Fatalf("::1 should validate: %v", err)
	}
}

func TestRenderRNSConfig(t *testing.T) {
	cfg := validTestConfig()
	cfg.EnableTransport = true
	cfg.TCPServerInterface = TCPServerInterface{Enabled: true, ListenPort: 4242}
	out := renderRNSConfig(&cfg, "172.20.0.2")

	for _, want := range []string{
		"enable_transport = True",
		"share_instance = Yes",
		"loglevel = 4",
		"[[AutoInterface]]",
		"devices = eth0",
		"[[Beleth RNS Hub]]",
		"type = TCPClientInterface",
		"target_host = rns.beleth.net",
		"target_port = 4242",
		"[[TCPServer]]",
		"type = TCPServerInterface",
		"listen_ip = 172.20.0.2",
		"listen_port = 4242",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("rendered config missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "0.0.0.0") {
		t.Errorf("server must never bind 0.0.0.0:\n%s", out)
	}
}

func TestRenderRNSConfigTransportOffServerOff(t *testing.T) {
	cfg := defaultConfig()
	out := renderRNSConfig(&cfg, "172.20.0.2")
	if !strings.Contains(out, "enable_transport = False") {
		t.Errorf("transport should render as False:\n%s", out)
	}
	// TCPServer section present but disabled
	idx := strings.Index(out, "[[TCPServer]]")
	if idx < 0 || !strings.Contains(out[idx:], "enabled = No") {
		t.Errorf("server should render disabled:\n%s", out)
	}
}

func TestRenderRNSConfigServerNeedsContainerIP(t *testing.T) {
	cfg := defaultConfig()
	cfg.TCPServerInterface.Enabled = true
	out := renderRNSConfig(&cfg, "")
	idx := strings.Index(out, "[[TCPServer]]")
	if idx < 0 || !strings.Contains(out[idx:], "enabled = No") {
		t.Errorf("server must be disabled when container IP is unknown:\n%s", out)
	}
	if strings.Contains(out, "0.0.0.0") {
		t.Errorf("server must never bind 0.0.0.0:\n%s", out)
	}
}

func TestConfigRoundtrip(t *testing.T) {
	dir := t.TempDir()
	origConfig := ConfigFile
	ConfigFile = filepath.Join(dir, "config.json")
	defer func() { ConfigFile = origConfig }()

	// first load creates the default config file
	if err := loadConfig(); err != nil {
		t.Fatalf("initial loadConfig failed: %v", err)
	}
	info, err := os.Stat(ConfigFile)
	if err != nil {
		t.Fatalf("config file not created: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0600 {
		t.Errorf("config file should be 0600, got %o", perm)
	}

	want := validTestConfig()
	want.EnableTransport = true
	if err := setConfig(want); err != nil {
		t.Fatalf("setConfig failed: %v", err)
	}
	gConfig = defaultConfig()
	if err := loadConfig(); err != nil {
		t.Fatalf("loadConfig failed: %v", err)
	}
	got := getConfig()
	if !got.EnableTransport || len(got.TCPClientInterfaces) != 1 ||
		got.TCPClientInterfaces[0].TargetHost != "rns.beleth.net" {
		t.Fatalf("roundtrip mismatch: %+v", got)
	}
}

func TestLoadConfigRejectsInvalidStored(t *testing.T) {
	dir := t.TempDir()
	origConfig := ConfigFile
	ConfigFile = filepath.Join(dir, "config.json")
	defer func() { ConfigFile = origConfig }()

	bad := `{"TCPClientInterfaces":[{"Name":"x]]\n[[evil","TargetHost":"a.com","TargetPort":4242}]}`
	if err := os.WriteFile(ConfigFile, []byte(bad), 0600); err != nil {
		t.Fatal(err)
	}
	if err := loadConfig(); err == nil {
		t.Fatal("loadConfig should reject an invalid stored config")
	}
}

func TestParseRnstatusJSON(t *testing.T) {
	sample := `{
	  "interfaces": [
	    {"clients": null, "name": "AutoInterface[AutoInterface]", "short_name": "AutoInterface",
	     "type": "AutoInterface", "rxb": 1024, "txb": 2048, "status": true, "mode": 1,
	     "bitrate": 10000000.0, "ifac_signature": null, "ifac_size": null, "ifac_netname": null},
	    {"clients": 3, "name": "TCPServerInterface[TCPServer/172.20.0.2:4242]", "short_name": "TCPServer",
	     "type": "TCPServerInterface", "rxb": 10, "txb": 20, "status": false, "mode": 1,
	     "bitrate": null, "ifac_signature": null, "ifac_size": null, "ifac_netname": null}
	  ],
	  "rxb": 1034, "txb": 2068, "rxs": 0, "txs": 0,
	  "transport_id": "adadadadadadadadadadadadadadadad", "transport_uptime": 42.5, "rss": null
	}`
	stats, err := parseRnstatusJSON([]byte(sample))
	if err != nil {
		t.Fatalf("parse failed: %v", err)
	}
	if len(stats.Interfaces) != 2 {
		t.Fatalf("expected 2 interfaces, got %d", len(stats.Interfaces))
	}
	auto := stats.Interfaces[0]
	if !auto.Status || auto.Type != "AutoInterface" || auto.RXB != 1024 || auto.Clients != nil {
		t.Errorf("bad auto interface parse: %+v", auto)
	}
	srv := stats.Interfaces[1]
	if srv.Status || srv.Clients == nil || *srv.Clients != 3 || srv.Bitrate != nil {
		t.Errorf("bad server interface parse: %+v", srv)
	}
	if stats.RXB != 1034 || stats.TransportID == "" || stats.TransportUptime != 42.5 {
		t.Errorf("bad totals parse: %+v", stats)
	}
}

func TestParseRnstatusText(t *testing.T) {
	sample := `
 Shared Instance[default]
    Status  : Up
    Serving : 2 programs
    Rate    : 1.00 Gbps
    Traffic : ↓1.31 KB
              ↑1.19 KB

 AutoInterface[AutoInterface]
    Status : Up
    Mode   : Full
    Rate   : 10.00 Mbps
    Peers  : 1 reachable
    Traffic : ↓15.23 KB
              ↑9.91 KB

 TCPInterface[Beleth RNS Hub/rns.beleth.net:4242]
    Status : Down
    Mode   : Full
    Rate   : 10.00 Mbps
    Traffic : ↓0 B
              ↑0 B
`
	ifaces := parseRnstatusText(sample)
	if len(ifaces) != 3 {
		t.Fatalf("expected 3 interfaces, got %d: %+v", len(ifaces), ifaces)
	}
	if !ifaces[0].Online || ifaces[0].Clients == nil || *ifaces[0].Clients != 2 {
		t.Errorf("bad shared instance parse: %+v", ifaces[0])
	}
	if ifaces[1].Type != "AutoInterface" || !ifaces[1].Online {
		t.Errorf("bad auto interface parse: %+v", ifaces[1])
	}
	if ifaces[2].Online || ifaces[2].ShortName != "Beleth RNS Hub" || ifaces[2].Type != "TCPInterface" {
		t.Errorf("bad tcp interface parse: %+v", ifaces[2])
	}
}

func TestPathEntryParse(t *testing.T) {
	sample := `[{"hash": "cafebabe", "timestamp": 1751980000.1, "via": "deadbeef",
	  "hops": 2, "expires": 1752000000.5, "interface": "TCPInterface[Hub/rns.example.net:4242]"}]`
	entries := []PathEntry{}
	if err := json.Unmarshal([]byte(sample), &entries); err != nil {
		t.Fatalf("parse failed: %v", err)
	}
	if len(entries) != 1 || entries[0].Hops != 2 || entries[0].Hash != "cafebabe" {
		t.Fatalf("bad path entry parse: %+v", entries)
	}
}
