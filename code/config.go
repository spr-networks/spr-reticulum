package main

import (
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
)

var TEST_PREFIX = os.Getenv("TEST_PREFIX")

// ConfigFile is the validated JSON config (source of truth, written by the API).
// GeneratedRNSConfigFile is the RNS config rendered from it, kept in
// /configs/spr-reticulum/ for inspection/persistence. RNSConfigDir is the
// directory rnsd actually runs from; it lives in the state dir because
// Reticulum keeps its identity/storage under <configdir>/storage.
var ConfigFile = TEST_PREFIX + "/configs/spr-reticulum/config.json"
var GeneratedRNSConfigFile = TEST_PREFIX + "/configs/spr-reticulum/rns.config"
var RNSConfigDir = TEST_PREFIX + "/state/plugins/spr-reticulum/rns"

var Configmtx sync.RWMutex

const MaxTCPClientInterfaces = 16

// Section names used by the generator; user supplied interface names must not
// collide with them.
var reservedInterfaceNames = map[string]bool{
	"AutoInterface": true,
	"TCPServer":     true,
}

type TCPClientInterface struct {
	Name       string
	Enabled    bool
	TargetHost string
	TargetPort int
}

type TCPServerInterface struct {
	Enabled    bool
	ListenPort int
}

type Config struct {
	// Route traffic for other peers (Reticulum transport node).
	EnableTransport bool
	// Peer with other RNS nodes on the plugin bridge (link-local IPv6).
	AutoInterfaceEnabled bool
	// Outbound connections to remote RNS hubs / transport nodes.
	TCPClientInterfaces []TCPClientInterface
	// Accept inbound RNS connections, bound to the container IP on the
	// spr-reticulum bridge only. Default off.
	TCPServerInterface TCPServerInterface
	// RNS log level 0..7 (default 4).
	LogLevel int
}

func defaultConfig() Config {
	return Config{
		EnableTransport:      false,
		AutoInterfaceEnabled: true,
		TCPClientInterfaces:  []TCPClientInterface{},
		TCPServerInterface:   TCPServerInterface{Enabled: false, ListenPort: 4242},
		LogLevel:             4,
	}
}

var gConfig = defaultConfig()

var nameRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9 ._-]{0,63}$`)
var hostRe = regexp.MustCompile(`^[A-Za-z0-9:][A-Za-z0-9.:_-]{0,252}$`)

func validInterfaceName(name string) error {
	if !nameRe.MatchString(name) {
		return fmt.Errorf("invalid interface name %q: allowed are letters, digits, space, '.', '_', '-' (max 64 chars, must start with a letter or digit)", name)
	}
	if reservedInterfaceNames[name] {
		return fmt.Errorf("interface name %q is reserved", name)
	}
	return nil
}

func validHost(host string) error {
	if hostRe.MatchString(host) {
		return nil
	}
	return fmt.Errorf("invalid host %q: allowed are hostnames, IPv4 or IPv6 addresses", host)
}

func validPort(port int) error {
	if port < 1 || port > 65535 {
		return fmt.Errorf("invalid port %d: must be 1-65535", port)
	}
	return nil
}

// Validate checks all user supplied values against allow-lists. Everything
// validated here ends up in the generated RNS config file, so the character
// sets deliberately exclude newlines, '[', ']', '=' and '#'.
func (c *Config) Validate() error {
	if c.LogLevel < 0 || c.LogLevel > 7 {
		return fmt.Errorf("invalid LogLevel %d: must be 0-7", c.LogLevel)
	}
	if len(c.TCPClientInterfaces) > MaxTCPClientInterfaces {
		return fmt.Errorf("too many TCP client interfaces (max %d)", MaxTCPClientInterfaces)
	}
	seen := map[string]bool{}
	for _, iface := range c.TCPClientInterfaces {
		if err := validInterfaceName(iface.Name); err != nil {
			return err
		}
		if seen[iface.Name] {
			return fmt.Errorf("duplicate interface name %q", iface.Name)
		}
		seen[iface.Name] = true
		if err := validHost(iface.TargetHost); err != nil {
			return err
		}
		if err := validPort(iface.TargetPort); err != nil {
			return err
		}
	}
	if err := validPort(c.TCPServerInterface.ListenPort); err != nil {
		return err
	}
	return nil
}

func loadConfig() error {
	Configmtx.Lock()
	defer Configmtx.Unlock()
	data, err := os.ReadFile(ConfigFile)
	if err != nil {
		if os.IsNotExist(err) {
			gConfig = defaultConfig()
			return writeConfigLocked()
		}
		return err
	}
	cfg := defaultConfig()
	if err := json.Unmarshal(data, &cfg); err != nil {
		return err
	}
	if cfg.TCPClientInterfaces == nil {
		cfg.TCPClientInterfaces = []TCPClientInterface{}
	}
	if err := cfg.Validate(); err != nil {
		return fmt.Errorf("stored config invalid: %w", err)
	}
	gConfig = cfg
	return nil
}

func getConfig() Config {
	Configmtx.RLock()
	defer Configmtx.RUnlock()
	cfg := gConfig
	cfg.TCPClientInterfaces = append([]TCPClientInterface{}, gConfig.TCPClientInterfaces...)
	return cfg
}

func setConfig(cfg Config) error {
	Configmtx.Lock()
	defer Configmtx.Unlock()
	gConfig = cfg
	return writeConfigLocked()
}

func writeConfigLocked() error {
	data, err := json.MarshalIndent(gConfig, "", "  ")
	if err != nil {
		return err
	}
	return atomicWrite(ConfigFile, append(data, '\n'), 0600)
}

// atomicWrite writes data to a temp file in the target directory and renames
// it into place.
func atomicWrite(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".tmp-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if err := tmp.Chmod(perm); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}

func boolToIni(b bool) string {
	if b {
		return "Yes"
	}
	return "No"
}

// renderRNSConfig renders the Reticulum config file from the validated JSON
// config. containerIP is the container's address on the spr-reticulum bridge;
// the TCPServerInterface only ever binds to it (never 0.0.0.0). If it cannot
// be determined the server interface is rendered disabled.
func renderRNSConfig(cfg *Config, containerIP string) string {
	var b strings.Builder
	b.WriteString("# Generated by the spr-reticulum plugin from config.json.\n")
	b.WriteString("# Do not edit manually - changes are overwritten. Manage via the SPR UI.\n\n")

	// upstream default config style: True/False for enable_transport
	transport := "False"
	if cfg.EnableTransport {
		transport = "True"
	}
	b.WriteString("[reticulum]\n")
	b.WriteString(fmt.Sprintf("  enable_transport = %s\n", transport))
	b.WriteString("  share_instance = Yes\n")
	b.WriteString("  instance_name = default\n")
	b.WriteString("  panic_on_interface_error = No\n\n")

	b.WriteString("[logging]\n")
	b.WriteString(fmt.Sprintf("  loglevel = %d\n\n", cfg.LogLevel))

	b.WriteString("[interfaces]\n\n")

	// Link-local IPv6 peering on the container's interface on the
	// spr-reticulum docker bridge.
	b.WriteString("  [[AutoInterface]]\n")
	b.WriteString("    type = AutoInterface\n")
	b.WriteString(fmt.Sprintf("    enabled = %s\n", boolToIni(cfg.AutoInterfaceEnabled)))
	b.WriteString("    devices = eth0\n\n")

	for _, iface := range cfg.TCPClientInterfaces {
		b.WriteString(fmt.Sprintf("  [[%s]]\n", iface.Name))
		b.WriteString("    type = TCPClientInterface\n")
		b.WriteString(fmt.Sprintf("    enabled = %s\n", boolToIni(iface.Enabled)))
		b.WriteString(fmt.Sprintf("    target_host = %s\n", iface.TargetHost))
		b.WriteString(fmt.Sprintf("    target_port = %d\n\n", iface.TargetPort))
	}

	serverEnabled := cfg.TCPServerInterface.Enabled
	if serverEnabled && containerIP == "" {
		b.WriteString("  # TCPServerInterface disabled: container IP could not be determined\n")
		serverEnabled = false
	}
	b.WriteString("  [[TCPServer]]\n")
	b.WriteString("    type = TCPServerInterface\n")
	b.WriteString(fmt.Sprintf("    enabled = %s\n", boolToIni(serverEnabled)))
	if containerIP != "" {
		b.WriteString(fmt.Sprintf("    listen_ip = %s\n", containerIP))
	} else {
		b.WriteString("    listen_ip = 127.0.0.1\n")
	}
	b.WriteString(fmt.Sprintf("    listen_port = %d\n", cfg.TCPServerInterface.ListenPort))

	return b.String()
}

// writeRNSConfigFiles renders the RNS config and writes it both to the configs
// dir (inspection copy) and into the rnsd config directory in the state dir.
func writeRNSConfigFiles() error {
	cfg := getConfig()
	rendered := []byte(renderRNSConfig(&cfg, getContainerIP()))
	if err := os.MkdirAll(RNSConfigDir, 0700); err != nil {
		return err
	}
	if err := atomicWrite(GeneratedRNSConfigFile, rendered, 0600); err != nil {
		return err
	}
	return atomicWrite(filepath.Join(RNSConfigDir, "config"), rendered, 0600)
}

// getContainerIP returns the container's IPv4 address on eth0 (the interface
// attached to the spr-reticulum docker bridge).
func getContainerIP() string {
	iface, err := net.InterfaceByName("eth0")
	if err != nil {
		return ""
	}
	addrs, err := iface.Addrs()
	if err != nil {
		return ""
	}
	for _, addr := range addrs {
		if ipnet, ok := addr.(*net.IPNet); ok && ipnet.IP.To4() != nil {
			return ipnet.IP.String()
		}
	}
	return ""
}
