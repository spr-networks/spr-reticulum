package main

import (
	"testing"
)

// rnstatus -a -j fixture: AutoInterface up, one TCP client up, one TCP client
// down, TCP server up. Field subset matches real rnstatus output shape.
const rnstatusFixture = `{
  "interfaces": [
    {"clients": null, "name": "AutoInterface[AutoInterface]", "short_name": "AutoInterface",
     "type": "AutoInterface", "rxb": 1024, "txb": 2048, "status": true, "mode": 1,
     "bitrate": 10000000.0, "ifac_signature": null, "ifac_size": null, "ifac_netname": null},
    {"clients": null, "name": "TCPInterface[Beleth RNS Hub/rns.beleth.net:4242]", "short_name": "Beleth RNS Hub",
     "type": "TCPInterface", "rxb": 100, "txb": 200, "status": true, "mode": 1,
     "bitrate": 10000000.0, "ifac_signature": null, "ifac_size": null, "ifac_netname": null},
    {"clients": null, "name": "TCPInterface[RMAP/rmap.world:4242]", "short_name": "RMAP",
     "type": "TCPInterface", "rxb": 0, "txb": 0, "status": false, "mode": 1,
     "bitrate": null, "ifac_signature": null, "ifac_size": null, "ifac_netname": null},
    {"clients": 2, "name": "TCPServerInterface[TCPServer/172.20.0.2:4242]", "short_name": "TCPServer",
     "type": "TCPServerInterface", "rxb": 10, "txb": 20, "status": true, "mode": 1,
     "bitrate": null, "ifac_signature": null, "ifac_size": null, "ifac_netname": null}
  ],
  "rxb": 1134, "txb": 2268, "rxs": 0, "txs": 0,
  "transport_id": "adadadadadadadadadadadadadadadad", "transport_uptime": 42.5, "rss": null
}`

func fixtureInterfaces(t *testing.T) []InterfaceStatus {
	t.Helper()
	stats, err := parseRnstatusJSON([]byte(rnstatusFixture))
	if err != nil {
		t.Fatalf("fixture parse failed: %v", err)
	}
	return toInterfaceStatuses(stats)
}

func topologyTestConfig() Config {
	cfg := defaultConfig()
	cfg.AutoInterfaceEnabled = true
	cfg.TCPClientInterfaces = []TCPClientInterface{
		{Name: "Beleth RNS Hub", Enabled: true, TargetHost: "rns.beleth.net", TargetPort: 4242},
		{Name: "RMAP", Enabled: true, TargetHost: "rmap.world", TargetPort: 4242},
		{Name: "Disabled Hub", Enabled: false, TargetHost: "off.example.net", TargetPort: 4242},
	}
	cfg.TCPServerInterface = TCPServerInterface{Enabled: true, ListenPort: 4242}
	return cfg
}

func nodeByID(topo Topology, id string) *TopoNode {
	for i := range topo.Nodes {
		if topo.Nodes[i].ID == id {
			return &topo.Nodes[i]
		}
	}
	return nil
}

func TestBuildTopologyDaemonDown(t *testing.T) {
	topo := buildTopology(topologyTestConfig(), false, nil)
	if len(topo.Nodes) != 1 || len(topo.Edges) != 0 {
		t.Fatalf("daemon down should yield root only, got %+v", topo)
	}
	root := topo.Nodes[0]
	if root.ID != "root" || root.ConnType != "reticulum" || !root.Online {
		t.Errorf("bad root anchor: %+v", root)
	}
}

func TestBuildTopologyFromRnstatusFixture(t *testing.T) {
	cfg := topologyTestConfig()
	topo := buildTopology(cfg, true, fixtureInterfaces(t))

	// root + AutoInterface + 2 enabled TCP clients + TCPServer;
	// the disabled client must not appear
	if len(topo.Nodes) != 5 {
		t.Fatalf("expected 5 nodes, got %d: %+v", len(topo.Nodes), topo.Nodes)
	}
	if len(topo.Edges) != 4 {
		t.Fatalf("expected 4 edges, got %d: %+v", len(topo.Edges), topo.Edges)
	}
	if nodeByID(topo, "Disabled Hub") != nil {
		t.Error("disabled interface must not appear in the topology")
	}

	root := nodeByID(topo, "root")
	if root == nil || root.ConnType != "reticulum" || !root.Online || root.Kind != "" {
		t.Errorf("bad root anchor: %+v", root)
	}
	if topo.Nodes[0].ID != "root" {
		t.Errorf("root should sort first, got %+v", topo.Nodes[0])
	}

	auto := nodeByID(topo, "AutoInterface")
	if auto == nil || auto.Kind != "interface" || auto.Name != "AutoInterface eth0" || !auto.Online || auto.IP != "" {
		t.Errorf("bad AutoInterface node: %+v", auto)
	}

	beleth := nodeByID(topo, "Beleth RNS Hub")
	if beleth == nil || beleth.Kind != "interface" || beleth.Name != "TCPClient rns.beleth.net:4242" {
		t.Fatalf("bad TCP client node: %+v", beleth)
	}
	if beleth.IP != "rns.beleth.net" {
		t.Errorf("TCP client should carry the target host as IP: %+v", beleth)
	}
	if !beleth.Online {
		t.Errorf("Beleth is up in the fixture: %+v", beleth)
	}
	if beleth.ConnType != "reticulum" {
		t.Errorf("interface nodes should have ConnType reticulum: %+v", beleth)
	}

	rmap := nodeByID(topo, "RMAP")
	if rmap == nil || rmap.Online {
		t.Errorf("RMAP is down in the fixture: %+v", rmap)
	}

	srv := nodeByID(topo, "TCPServer")
	if srv == nil || srv.Name != "TCPServer :4242" || !srv.Online {
		t.Errorf("bad TCPServer node: %+v", srv)
	}

	seenFrom := map[string]bool{}
	for _, e := range topo.Edges {
		if e.To != "root" || e.Layer != "rns" || e.Kind != "reticulum" {
			t.Errorf("bad edge: %+v", e)
		}
		if nodeByID(topo, e.From) == nil {
			t.Errorf("edge from unknown node: %+v", e)
		}
		seenFrom[e.From] = true
	}
	for _, id := range []string{"AutoInterface", "Beleth RNS Hub", "RMAP", "TCPServer"} {
		if !seenFrom[id] {
			t.Errorf("missing edge for %s", id)
		}
	}
}

func TestBuildTopologyInterfaceMissingFromRnstatus(t *testing.T) {
	// rnsd running but rnstatus does not (yet) report the interface:
	// configured interfaces must appear offline rather than vanish
	cfg := topologyTestConfig()
	topo := buildTopology(cfg, true, []InterfaceStatus{})
	if len(topo.Nodes) != 5 {
		t.Fatalf("expected 5 nodes, got %d: %+v", len(topo.Nodes), topo.Nodes)
	}
	for _, n := range topo.Nodes {
		if n.ID == "root" {
			continue
		}
		if n.Online {
			t.Errorf("interface missing from rnstatus should be offline: %+v", n)
		}
	}
}
