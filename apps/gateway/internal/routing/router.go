// Package routing provides deployment-to-upstream routing via YAML configuration
// with hot-reload support via fsnotify.
package routing

import (
	"errors"
	"fmt"
	"hash/fnv"
	"os"
	"sync"

	"github.com/fsnotify/fsnotify"
	"gopkg.in/yaml.v3"
)

// ErrDeploymentNotFound is returned when a deployment is not in the routing table.
var ErrDeploymentNotFound = errors.New("deployment not found")

// Router resolves a deployment ID to an upstream vLLM address.
type Router interface {
	Resolve(deploymentID string) (string, error)
	// PrefixResolve resolves to a specific upstream using consistent hashing on cacheKey.
	// cacheKey is typically a hash of the system prompt prefix.
	// Falls back to Resolve if only one upstream is available.
	PrefixResolve(deploymentID, cacheKey string) (string, error)
}

// FileRouter loads routes from a YAML file and hot-reloads on changes.
type FileRouter struct {
	mu        sync.RWMutex
	routes    map[string]string
	podRoutes map[string][]string
	path      string
	done      chan struct{}
}

// routeConfig represents the YAML structure.
type routeConfig struct {
	Routes    map[string]string   `yaml:"routes"`
	PodRoutes map[string][]string `yaml:"podRoutes"`
}

// NewFileRouter creates a new file-based router.
// It loads the YAML file and starts a background goroutine to watch for changes.
func NewFileRouter(path string) (Router, error) {
	fr := &FileRouter{
		path: path,
		done: make(chan struct{}),
	}

	// Load initial routes.
	if err := fr.reload(); err != nil {
		return nil, fmt.Errorf("failed to load routes: %w", err)
	}

	// Start the file watcher in a background goroutine.
	go fr.watchFile()

	return fr, nil
}

// reload reads and parses the YAML file and updates the routes and podRoutes maps.
func (fr *FileRouter) reload() error {
	data, err := os.ReadFile(fr.path)
	if err != nil {
		return fmt.Errorf("failed to read file: %w", err)
	}

	var cfg routeConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return fmt.Errorf("failed to parse YAML: %w", err)
	}

	fr.mu.Lock()
	fr.routes = cfg.Routes
	fr.podRoutes = cfg.PodRoutes
	fr.mu.Unlock()

	return nil
}

// watchFile monitors the YAML file for changes and reloads it.
func (fr *FileRouter) watchFile() {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		// Log error in a real implementation; for now, silently fail.
		return
	}
	defer watcher.Close()

	err = watcher.Add(fr.path)
	if err != nil {
		// File path may be invalid; silently exit.
		return
	}

	for {
		select {
		case event, ok := <-watcher.Events:
			if !ok {
				return
			}
			// Reload on write or create events.
			if event.Op&(fsnotify.Write|fsnotify.Create) != 0 {
				_ = fr.reload() // Ignore reload errors; keep using old routes.
			}

		case <-watcher.Errors:
			// Ignore watch errors.

		case <-fr.done:
			return
		}
	}
}

// Resolve returns the upstream address for the given deployment ID.
func (fr *FileRouter) Resolve(deploymentID string) (string, error) {
	fr.mu.RLock()
	defer fr.mu.RUnlock()

	addr, ok := fr.routes[deploymentID]
	if !ok {
		return "", ErrDeploymentNotFound
	}

	return addr, nil
}

// PrefixResolve resolves to a specific upstream using consistent hashing on cacheKey.
// If pod-level routes are available, uses consistent hash to pick a pod.
// Otherwise, falls back to service-level routing.
func (fr *FileRouter) PrefixResolve(deploymentID, cacheKey string) (string, error) {
	fr.mu.RLock()
	defer fr.mu.RUnlock()

	// Check if pod-level routes are available for this deployment.
	if pods, ok := fr.podRoutes[deploymentID]; ok && len(pods) > 0 {
		// Consistent hash: FNV-1a of cacheKey mod len(pods).
		h := fnv.New32a()
		h.Write([]byte(cacheKey))
		idx := int(h.Sum32()) % len(pods)
		return pods[idx], nil
	}

	// Fall back to service-level routing.
	addr, ok := fr.routes[deploymentID]
	if !ok {
		return "", ErrDeploymentNotFound
	}

	return addr, nil
}

// Close stops the file watcher. Not part of the Router interface but useful for cleanup.
func (fr *FileRouter) Close() {
	close(fr.done)
}
