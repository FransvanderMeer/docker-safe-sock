package main

import (
	"bufio"
	"context"
	"encoding/json"
	"flag"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httputil"
	"os"
	"os/signal"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"
)

var (
	addr           = flag.String("addr", "", "Addresses to listen on (comma separated). Default: 127.0.0.1:2375 (or DSS_ADDR env)")
	socketPath     = flag.String("socket", "", "Path to Docker socket. Default: /var/run/docker.sock (or DSS_SOCKET env)")
	safeSocketPath = flag.String("safe-socket", "", "Path to create safe Unix socket. (or DSS_SAFE_SOCKET env)")
	// Allowed paths regex
	allowedPaths = regexp.MustCompile(`^/(v[\d\.]+/)??(version|_ping|events|containers/json|containers/[a-zA-Z0-9_.-]+/json)$`)
)

func main() {
	parseConfig()

	// Verify socket exists
	if _, err := os.Stat(*socketPath); os.IsNotExist(err) {
		log.Fatalf("Docker socket not found at %s", *socketPath)
	}

	proxy := &httputil.ReverseProxy{
		Director: func(req *http.Request) {
			req.URL.Scheme = "http"
			req.URL.Host = "docker" // Host header is ignored by unix dialer but required for http
		},
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
				return net.Dial("unix", *socketPath)
			},
		},
		ModifyResponse: modifyResponse,
	}

	handler := authMiddleware(proxy)
	var servers []*http.Server

	// 1. TCP Listeners
	if *addr != "" {
		addrs := strings.Split(*addr, ",")
		for _, a := range addrs {
			a = strings.TrimSpace(a)
			if a == "" {
				continue
			}

			srv := &http.Server{
				Addr:    a,
				Handler: handler,
			}
			servers = append(servers, srv)

			go func(s *http.Server) {
				log.Printf("Listening on TCP %s, proxying to %s", s.Addr, *socketPath)
				if err := s.ListenAndServe(); err != http.ErrServerClosed {
					log.Fatalf("TCP Server error on %s: %v", s.Addr, err)
				}
			}(srv)
		}
	}

	// 2. Unix Listener
	if *safeSocketPath != "" {
		// Remove existing socket file
		if _, err := os.Stat(*safeSocketPath); err == nil {
			if err := os.Remove(*safeSocketPath); err != nil {
				log.Fatalf("Failed to remove existing socket %s: %v", *safeSocketPath, err)
			}
		}

		l, err := net.Listen("unix", *safeSocketPath)
		if err != nil {
			log.Fatalf("Failed to listen on unix socket %s: %v", *safeSocketPath, err)
		}

		// Set permissions to 0666 so anyone can write (it's safe-sock after all)
		if err := os.Chmod(*safeSocketPath, 0666); err != nil {
			log.Printf("Warning: Failed to chmod %s: %v", *safeSocketPath, err)
		}

		srv := &http.Server{
			Handler: handler,
		}
		servers = append(servers, srv)

		go func(s *http.Server, l net.Listener) {
			log.Printf("Listening on Unix %s, proxying to %s", *safeSocketPath, *socketPath)
			if err := s.Serve(l); err != http.ErrServerClosed {
				log.Fatalf("Unix Server error on %s: %v", *safeSocketPath, err)
			}
		}(srv, l)
	}

	if len(servers) == 0 {
		log.Fatal("No listeners configured. Set -addr or -safe-socket.")
	}

	// Graceful shutdown
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	<-stop

	log.Println("Shutting down servers...")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	for _, s := range servers {
		if err := s.Shutdown(ctx); err != nil {
			log.Printf("Shutdown error for %s: %v", s.Addr, err)
		}
	}
	log.Println("All servers stopped")
}

func parseConfig() {
	flag.Parse()

	// Priority: Flag > Env > Default
	if *safeSocketPath == "" {
		env := os.Getenv("DSS_SAFE_SOCKET")
		if env != "" {
			*safeSocketPath = env
		}
	}

	// Only default addr if NEITHER addr nor safe-socket is set
	if *addr == "" && *safeSocketPath == "" {
		env := os.Getenv("DSS_ADDR")
		if env != "" {
			*addr = env
		} else {
			*addr = "127.0.0.1:2375"
		}
	} else if *addr == "" {
		// safe-socket is set, but addr flag is empty. Check env, otherwise leave empty (no TCP).
		env := os.Getenv("DSS_ADDR")
		if env != "" {
			*addr = env
		}
	}

	// Expand "auto:bridge" or "bridges" to actual Docker bridge IPs
	if strings.Contains(*addr, "auto:bridge") || strings.Contains(*addr, "bridges") {
		bridgeAddrs := getDockerBridgeAddrs()
		if len(bridgeAddrs) > 0 {
			log.Printf("Discovered Docker bridge IPs: %v", bridgeAddrs)
			// Replace keywords or append
			// Simple approach: Replace 'auto:bridge' with joined list
			expanded := strings.ReplaceAll(*addr, "auto:bridge", strings.Join(bridgeAddrs, ","))
			expanded = strings.ReplaceAll(expanded, "bridges", strings.Join(bridgeAddrs, ","))
			*addr = expanded
		} else {
			log.Println("Warning: 'auto:bridge' specified but no bridge interfaces found.")
		}
	}

	if *socketPath == "" {
		env := os.Getenv("DSS_SOCKET")
		if env != "" {
			*socketPath = env
		} else {
			*socketPath = "/var/run/docker.sock"
		}
	}
}

// getDockerBridgeAddrs finds IPs of interfaces starting with 'docker' or 'br-'
func getDockerBridgeAddrs() []string {
	var addrs []string
	ifaces, err := net.Interfaces()
	if err != nil {
		log.Printf("Error listing interfaces: %v", err)
		return nil
	}

	for _, i := range ifaces {
		if strings.HasPrefix(i.Name, "docker") || strings.HasPrefix(i.Name, "br-") {
			uniAddrs, err := i.Addrs()
			if err != nil {
				continue
			}
			for _, a := range uniAddrs {
				// Check if it's an IPv4 address
				if ipnet, ok := a.(*net.IPNet); ok && !ipnet.IP.IsLoopback() {
					if ipnet.IP.To4() != nil {
						addrs = append(addrs, ipnet.IP.String()+":2375")
					}
				}
			}
		}
	}
	return addrs
}

func authMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// 1. Only allow GET
		if r.Method != http.MethodGet {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}

		// 2. Allowlist paths
		if !allowedPaths.MatchString(r.URL.Path) {
			log.Printf("Blocked path: %s", r.URL.Path)
			http.Error(w, "Forbidden", http.StatusForbidden)
			return
		}

		next.ServeHTTP(w, r)
	})
}

func modifyResponse(resp *http.Response) error {
	path := resp.Request.URL.Path

	// Handle Container List: /containers/json
	if strings.Contains(path, "/containers/json") {
		// Traefik uses list to get basic info. Usually Env is NOT in list response unless size=true?
		// Actually, 'docker ps' doesn't show envs. Inspection does.
		// But let's be safe. If the response is a list of containers, we might want to ensure no sensitive data if it was there.
		// The standard /containers/json response does NOT contain Env.
		// It contains specific fields.
		return nil
	}

	// Handle Container Inspect: /containers/{id}/json
	if strings.Contains(path, "/containers/") && strings.HasSuffix(path, "/json") && !strings.Contains(path, "containers/json") {
		return filterInspectResponse(resp)
	}

	// Handle Events: /events
	if strings.Contains(path, "/events") {
		// Create a pipe to filter the stream
		pr, pw := io.Pipe()

		go func() {
			defer resp.Body.Close()
			defer pw.Close()

			scanner := bufio.NewScanner(resp.Body)
			for scanner.Scan() {
				line := scanner.Bytes()
				if shouldKeepEvent(line) {
					if _, err := pw.Write(append(line, '\n')); err != nil {
						return // Downstream closed
					}
				}
			}
		}()

		resp.Body = pr
		return nil
	}

	return nil
}

// filterInspectResponse strips Env variables from container inspection
func filterInspectResponse(resp *http.Response) error {
	if resp.StatusCode != http.StatusOK {
		return nil
	}

	var data map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		return err
	}
	resp.Body.Close()

	// Strip Config.Env
	if config, ok := data["Config"].(map[string]interface{}); ok {
		config["Env"] = []string{} // Clear Envs
	}

	// Strip ContainerJSONBase.HostConfig.Env (if present in some versions?) usually it's in Config.

	newBody, err := json.Marshal(data)
	if err != nil {
		return err
	}

	resp.Body = io.NopCloser(strings.NewReader(string(newBody)))
	resp.ContentLength = int64(len(newBody))
	resp.Header.Set("Content-Length", strconv.Itoa(len(newBody)))
	return nil
}

// shouldKeepEvent Unmarshals the minimal check to decide if we keep the event
func shouldKeepEvent(line []byte) bool {
	var event struct {
		Type   string `json:"type"`
		Action string `json:"action"`
	}
	if err := json.Unmarshal(line, &event); err != nil {
		return false // Drop malformed json
	}

	// Check for container events
	if event.Type == "container" {
		switch event.Action {
		case "start", "die", "pause", "unpause", "create", "destroy", "rename", "update":
			return true
		}
		// health_status: healthy/unhealthy is a colon separated action
		if strings.HasPrefix(event.Action, "health_status") {
			return true
		}
	}

	// Check for network events (Traefik watches these too)
	if event.Type == "network" {
		switch event.Action {
		case "create", "destroy", "connect", "disconnect":
			return true
		}
	}

	// Log dropped event for debugging (optional, but helpful if user reports issues)
	// log.Printf("Dropping event: type=%s action=%s", event.Type, event.Action)
	return false
}
