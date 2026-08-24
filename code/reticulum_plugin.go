package main

import (
	"encoding/json"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
)

var UNIX_PLUGIN_LISTENER = TEST_PREFIX + "/state/plugins/spr-reticulum/socket.sock"

func pluginSocketPath() string {
	if path := os.Getenv("SPR_KRUN_PLUGIN_SOCKET"); path != "" {
		return path
	}
	return UNIX_PLUGIN_LISTENER
}

// the name of the docker bridge for this plugin (see docker-compose.yml and
// plugin.json NetworkCapabilities.Interface).
var gReticulumInterface = "spr-reticulum"

var gDaemon = &RNSDaemon{}

func jsonResponse(w http.ResponseWriter, value interface{}) {
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(value); err != nil {
		log.Println("failed to encode response:", err)
	}
}

func handleGetStatus(w http.ResponseWriter, r *http.Request) {
	jsonResponse(w, collectStatus(gDaemon))
}

func handleGetConfig(w http.ResponseWriter, r *http.Request) {
	// nothing secret in the config (no keys/tokens), safe to return as-is
	jsonResponse(w, getConfig())
}

func handlePutConfig(w http.ResponseWriter, r *http.Request) {
	cfg := defaultConfig()
	if err := json.NewDecoder(r.Body).Decode(&cfg); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	if cfg.TCPClientInterfaces == nil {
		cfg.TCPClientInterfaces = []TCPClientInterface{}
	}
	if err := cfg.Validate(); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	if err := setConfig(cfg); err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	// apply: re-render the RNS config and restart the daemon in the
	// background (rnsd shutdown can take a few seconds)
	go func() {
		if err := gDaemon.Restart(); err != nil {
			log.Println("rnsd restart after config update failed:", err)
		}
	}()
	jsonResponse(w, getConfig())
}

func handleRestart(w http.ResponseWriter, r *http.Request) {
	if err := gDaemon.Restart(); err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	jsonResponse(w, map[string]bool{"Restarted": true})
}

func handleGetPathTable(w http.ResponseWriter, r *http.Request) {
	if !gDaemon.Running() {
		http.Error(w, "rnsd is not running", 503)
		return
	}
	entries, err := collectPathTable()
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	jsonResponse(w, entries)
}

// spaHandler serves the bundled UI from /ui, falling back to index.html.
type spaHandler struct {
	staticPath string
	indexPath  string
}

func (h spaHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	path, err := filepath.Abs(r.URL.Path)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	path = filepath.Join(h.staticPath, path)
	_, err = os.Stat(path)
	if os.IsNotExist(err) {
		http.ServeFile(w, r, filepath.Join(h.staticPath, h.indexPath))
		return
	} else if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	http.FileServer(http.Dir(h.staticPath)).ServeHTTP(w, r)
}

func logRequest(handler http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		log.Printf("%s %s %s", r.RemoteAddr, r.Method, r.URL)
		handler.ServeHTTP(w, r)
	})
}

func main() {
	log.SetOutput(os.Stdout)

	if err := loadConfig(); err != nil {
		log.Println("failed to load config, using defaults:", err)
	}

	if err := gDaemon.Start(); err != nil {
		// keep serving the API/UI so the user can fix the config
		log.Println("failed to start rnsd:", err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /status", handleGetStatus)
	mux.HandleFunc("GET /config", handleGetConfig)
	mux.HandleFunc("PUT /config", handlePutConfig)
	mux.HandleFunc("POST /restart", handleRestart)
	mux.HandleFunc("GET /path-table", handleGetPathTable)
	mux.HandleFunc("GET /topology", handleGetTopology)
	mux.Handle("/", spaHandler{staticPath: "/ui", indexPath: "index.html"})

	socketPath := pluginSocketPath()
	os.Remove(socketPath)
	if err := os.MkdirAll(filepath.Dir(socketPath), 0755); err != nil {
		panic(err)
	}
	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		panic(err)
	}
	if err := os.Chmod(socketPath, 0770); err != nil {
		panic(err)
	}

	server := http.Server{Handler: logRequest(mux)}
	if err := server.Serve(listener); err != nil {
		log.Fatal(err)
	}
}
