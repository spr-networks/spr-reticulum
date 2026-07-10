package main

import (
	"fmt"
	"net/http"
	"sort"
)

// TopoNode/TopoEdge/Topology mirror the shapes the SPR host expects when it
// merges a plugin graph into the router topology view (see spr-tailscale).
// The host attaches the plugin's "root" anchor node to the router node.
type TopoNode struct {
	ID       string
	Kind     string
	Name     string
	IP       string `json:",omitempty"`
	ConnType string `json:",omitempty"`
	Online   bool
}

type TopoEdge struct {
	From  string
	To    string
	Layer string
	Kind  string
}

type Topology struct {
	Nodes []TopoNode
	Edges []TopoEdge
}

// onlineByShortName indexes the rnstatus interface list by short name, which
// is the config section name RNS reports back (e.g. "Beleth RNS Hub" for
// "TCPInterface[Beleth RNS Hub/rns.beleth.net:4242]", "AutoInterface" for
// the built-in AutoInterface section, "TCPServer" for the listener).
func onlineByShortName(ifaces []InterfaceStatus) map[string]bool {
	online := map[string]bool{}
	for _, iface := range ifaces {
		online[iface.ShortName] = iface.Online
	}
	return online
}

// buildTopology builds the plugin topology graph: a root anchor (this
// router's rnsd node) plus one node per enabled configured RNS interface,
// each with an edge toward root. Interface Online state comes from the live
// rnstatus interface list; an enabled interface missing from rnstatus (e.g.
// rnsd still starting) reports as offline. When the daemon is down only the
// root anchor is returned.
func buildTopology(cfg Config, running bool, ifaces []InterfaceStatus) Topology {
	topo := Topology{
		Nodes: []TopoNode{{ID: "root", ConnType: "reticulum", Online: true}},
		Edges: []TopoEdge{},
	}
	if !running {
		return topo
	}
	online := onlineByShortName(ifaces)

	add := func(id, name, ip string, isOnline bool) {
		topo.Nodes = append(topo.Nodes, TopoNode{
			ID:       id,
			Kind:     "interface",
			Name:     name,
			IP:       ip,
			ConnType: "reticulum",
			Online:   isOnline,
		})
		topo.Edges = append(topo.Edges, TopoEdge{
			From: id, To: "root", Layer: "rns", Kind: "reticulum",
		})
	}

	if cfg.AutoInterfaceEnabled {
		add("AutoInterface", "AutoInterface eth0", "", online["AutoInterface"])
	}
	for _, iface := range cfg.TCPClientInterfaces {
		if !iface.Enabled {
			continue
		}
		// config names are validated unique and cannot collide with the
		// reserved "AutoInterface"/"TCPServer" section names, so they are
		// safe to use as node IDs directly.
		add(
			iface.Name,
			fmt.Sprintf("TCPClient %s:%d", iface.TargetHost, iface.TargetPort),
			iface.TargetHost,
			online[iface.Name],
		)
	}
	if cfg.TCPServerInterface.Enabled {
		add(
			"TCPServer",
			fmt.Sprintf("TCPServer :%d", cfg.TCPServerInterface.ListenPort),
			"",
			online["TCPServer"],
		)
	}

	// root has an empty Name and stays first
	sort.Slice(topo.Nodes, func(i, j int) bool { return topo.Nodes[i].Name < topo.Nodes[j].Name })
	sort.Slice(topo.Edges, func(i, j int) bool { return topo.Edges[i].From < topo.Edges[j].From })
	return topo
}

func handleGetTopology(w http.ResponseWriter, r *http.Request) {
	status := collectStatus(gDaemon)
	jsonResponse(w, buildTopology(getConfig(), status.Running, status.Interfaces))
}
