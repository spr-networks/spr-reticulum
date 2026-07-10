package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"
)

// Paths to the pinned rns tooling installed in the image (overridable for
// development/testing).
func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

var rnsdBin = envOr("RNSD_BIN", "/opt/rns/bin/rnsd")
var rnstatusBin = envOr("RNSTATUS_BIN", "/opt/rns/bin/rnstatus")
var rnpathBin = envOr("RNPATH_BIN", "/opt/rns/bin/rnpath")

const rnsdRestartBackoff = 5 * time.Second
const rnsdStopTimeout = 8 * time.Second
const cliTimeout = 15 * time.Second

// RNSDaemon supervises rnsd as a child process: it renders the RNS config
// before each start, restarts the daemon if it exits unexpectedly, and
// tightens permissions on the identity/storage files rnsd creates.
type RNSDaemon struct {
	mtx       sync.Mutex
	cmd       *exec.Cmd
	running   bool
	startedAt time.Time
	gen       int // bumped on intentional stop so the waiter doesn't restart
}

func (d *RNSDaemon) Running() bool {
	d.mtx.Lock()
	defer d.mtx.Unlock()
	return d.running
}

func (d *RNSDaemon) Uptime() float64 {
	d.mtx.Lock()
	defer d.mtx.Unlock()
	if !d.running {
		return 0
	}
	return time.Since(d.startedAt).Seconds()
}

func (d *RNSDaemon) Start() error {
	if err := writeRNSConfigFiles(); err != nil {
		return fmt.Errorf("failed to write RNS config: %w", err)
	}

	d.mtx.Lock()
	defer d.mtx.Unlock()
	if d.running {
		return nil
	}

	cmd := exec.Command(rnsdBin, "--config", RNSConfigDir)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("failed to start rnsd: %w", err)
	}
	d.cmd = cmd
	d.running = true
	d.startedAt = time.Now()
	gen := d.gen
	log.Printf("rnsd started (pid %d)", cmd.Process.Pid)

	go d.waitAndMaybeRestart(cmd, gen)
	go tightenStoragePerms()
	return nil
}

func (d *RNSDaemon) waitAndMaybeRestart(cmd *exec.Cmd, gen int) {
	err := cmd.Wait()

	d.mtx.Lock()
	intentional := d.gen != gen
	if d.cmd == cmd {
		d.running = false
	}
	d.mtx.Unlock()

	if intentional {
		return
	}
	log.Printf("rnsd exited unexpectedly (%v), restarting in %s", err, rnsdRestartBackoff)
	time.Sleep(rnsdRestartBackoff)
	if err := d.Start(); err != nil {
		log.Printf("rnsd restart failed: %v", err)
	}
}

func (d *RNSDaemon) Stop() {
	d.mtx.Lock()
	if !d.running || d.cmd == nil || d.cmd.Process == nil {
		d.mtx.Unlock()
		return
	}
	d.gen++
	cmd := d.cmd
	d.mtx.Unlock()

	cmd.Process.Signal(syscall.SIGTERM)
	done := make(chan struct{})
	go func() {
		for {
			d.mtx.Lock()
			stopped := !d.running || d.cmd != cmd
			d.mtx.Unlock()
			if stopped {
				close(done)
				return
			}
			time.Sleep(100 * time.Millisecond)
		}
	}()
	select {
	case <-done:
	case <-time.After(rnsdStopTimeout):
		log.Printf("rnsd did not stop in %s, killing", rnsdStopTimeout)
		cmd.Process.Kill()
		<-done
	}
}

func (d *RNSDaemon) Restart() error {
	d.Stop()
	return d.Start()
}

// tightenStoragePerms ensures RNS identity/storage files are private (0600
// files, 0700 dirs). Runs shortly after start so rnsd has created them.
func tightenStoragePerms() {
	time.Sleep(5 * time.Second)
	storage := filepath.Join(RNSConfigDir, "storage")
	filepath.WalkDir(storage, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if entry.IsDir() {
			os.Chmod(path, 0700)
		} else {
			os.Chmod(path, 0600)
		}
		return nil
	})
}

var gRNSVersion string
var gRNSVersionOnce sync.Once

// rnsVersion returns the installed RNS version ("rnsd --version" ->
// "rnsd 1.3.7").
func rnsVersion() string {
	gRNSVersionOnce.Do(func() {
		ctx, cancel := context.WithTimeout(context.Background(), cliTimeout)
		defer cancel()
		out, err := exec.CommandContext(ctx, rnsdBin, "--version").Output()
		if err != nil {
			log.Printf("rnsd --version failed: %v", err)
			return
		}
		fields := strings.Fields(strings.TrimSpace(string(out)))
		if len(fields) > 0 {
			gRNSVersion = fields[len(fields)-1]
		}
	})
	return gRNSVersion
}

// InterfaceStatus is the per-interface status reported by GET /status.
type InterfaceStatus struct {
	Name      string
	ShortName string
	Type      string
	Online    bool
	Clients   *int     `json:",omitempty"`
	BitRate   *float64 `json:",omitempty"`
	RXBytes   int64
	TXBytes   int64
}

// rnstatusJSON mirrors the parts of `rnstatus -j` output (RNS
// Reticulum.get_interface_stats()) that the plugin consumes.
type rnstatusJSON struct {
	Interfaces      []rnstatusInterface `json:"interfaces"`
	RXB             int64               `json:"rxb"`
	TXB             int64               `json:"txb"`
	TransportID     string              `json:"transport_id"`
	TransportUptime float64             `json:"transport_uptime"`
}

type rnstatusInterface struct {
	Name      string   `json:"name"`
	ShortName string   `json:"short_name"`
	Type      string   `json:"type"`
	Status    bool     `json:"status"`
	Clients   *int     `json:"clients"`
	Bitrate   *float64 `json:"bitrate"`
	RXB       int64    `json:"rxb"`
	TXB       int64    `json:"txb"`
}

func parseRnstatusJSON(data []byte) (*rnstatusJSON, error) {
	stats := rnstatusJSON{}
	if err := json.Unmarshal(data, &stats); err != nil {
		return nil, err
	}
	return &stats, nil
}

// toInterfaceStatuses converts parsed rnstatus JSON to the plugin's interface
// status shape (shared by GET /status and the topology graph builder).
func toInterfaceStatuses(stats *rnstatusJSON) []InterfaceStatus {
	ifaces := []InterfaceStatus{}
	for _, iface := range stats.Interfaces {
		ifaces = append(ifaces, InterfaceStatus{
			Name:      iface.Name,
			ShortName: iface.ShortName,
			Type:      iface.Type,
			Online:    iface.Status,
			Clients:   iface.Clients,
			BitRate:   iface.Bitrate,
			RXBytes:   iface.RXB,
			TXBytes:   iface.TXB,
		})
	}
	return ifaces
}

// parseRnstatusText is the fallback parser for the human readable rnstatus
// output (used if --json is unavailable). Interface blocks look like:
//
//	TCPInterface[hub/rns.example.net:4242]
//	   Status  : Up
//	   Clients : 3
func parseRnstatusText(out string) []InterfaceStatus {
	ifaces := []InterfaceStatus{}
	var cur *InterfaceStatus
	for _, line := range strings.Split(out, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		indent := len(line) - len(strings.TrimLeft(line, " \t"))
		if indent <= 1 && strings.Contains(trimmed, "[") && strings.HasSuffix(trimmed, "]") {
			if cur != nil {
				ifaces = append(ifaces, *cur)
			}
			name := trimmed
			ifType := trimmed[:strings.Index(trimmed, "[")]
			short := strings.TrimSuffix(trimmed[strings.Index(trimmed, "[")+1:], "]")
			if idx := strings.Index(short, "/"); idx > 0 {
				short = short[:idx]
			}
			cur = &InterfaceStatus{Name: name, ShortName: short, Type: ifType}
			continue
		}
		if cur == nil {
			continue
		}
		key, value, found := strings.Cut(trimmed, ":")
		if !found {
			continue
		}
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		switch key {
		case "Status":
			cur.Online = strings.EqualFold(value, "Up")
		case "Clients", "Serving":
			var n int
			if _, err := fmt.Sscanf(value, "%d", &n); err == nil {
				cur.Clients = &n
			}
		}
	}
	if cur != nil {
		ifaces = append(ifaces, *cur)
	}
	return ifaces
}

// NodeStatus is the GET /status response.
type NodeStatus struct {
	Running          bool
	Version          string
	TransportEnabled bool
	AutoInterface    bool
	UptimeSeconds    float64
	TransportID      string `json:",omitempty"`
	RXBytes          int64
	TXBytes          int64
	Interfaces       []InterfaceStatus
	Error            string `json:",omitempty"`
}

// collectStatus builds the /status response from the supervisor state and
// rnstatus (JSON output preferred, text output as fallback).
func collectStatus(d *RNSDaemon) NodeStatus {
	cfg := getConfig()
	status := NodeStatus{
		Running:          d.Running(),
		Version:          rnsVersion(),
		TransportEnabled: cfg.EnableTransport,
		AutoInterface:    cfg.AutoInterfaceEnabled,
		UptimeSeconds:    d.Uptime(),
		Interfaces:       []InterfaceStatus{},
	}
	if !status.Running {
		return status
	}

	ctx, cancel := context.WithTimeout(context.Background(), cliTimeout)
	defer cancel()
	out, err := exec.CommandContext(ctx, rnstatusBin, "--config", RNSConfigDir, "-a", "-j").Output()
	if err == nil {
		if stats, jerr := parseRnstatusJSON(out); jerr == nil {
			status.RXBytes = stats.RXB
			status.TXBytes = stats.TXB
			status.TransportID = stats.TransportID
			status.Interfaces = toInterfaceStatuses(stats)
			return status
		}
	}

	// Fallback: text output (older rns without -j, or JSON parse failure).
	ctx2, cancel2 := context.WithTimeout(context.Background(), cliTimeout)
	defer cancel2()
	out, terr := exec.CommandContext(ctx2, rnstatusBin, "--config", RNSConfigDir, "-a").Output()
	if terr != nil {
		status.Error = fmt.Sprintf("rnstatus failed: %v", terr)
		return status
	}
	status.Interfaces = parseRnstatusText(string(out))
	return status
}

// PathEntry is one entry of the transport path table (rnpath -t -j).
type PathEntry struct {
	Hash      string  `json:"hash"`
	Timestamp float64 `json:"timestamp"`
	Via       string  `json:"via"`
	Hops      int     `json:"hops"`
	Expires   float64 `json:"expires"`
	Interface string  `json:"interface"`
}

func collectPathTable() ([]PathEntry, error) {
	ctx, cancel := context.WithTimeout(context.Background(), cliTimeout)
	defer cancel()
	out, err := exec.CommandContext(ctx, rnpathBin, "--config", RNSConfigDir, "-t", "-j").Output()
	if err != nil {
		return nil, fmt.Errorf("rnpath failed: %w", err)
	}
	entries := []PathEntry{}
	if err := json.Unmarshal(out, &entries); err != nil {
		return nil, fmt.Errorf("rnpath output parse failed: %w", err)
	}
	return entries, nil
}
