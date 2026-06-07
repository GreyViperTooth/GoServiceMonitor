package main

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"
)

// Service represents a monitored service.
type Service struct {
	Name string `json:"name"`
	URL  string `json:"url"`
}

// CheckResult holds the outcome of a health check.
type CheckResult struct {
	Name         string        `json:"name"`
	URL          string        `json:"url"`
	Status       string        `json:"status"`
	ResponseTime time.Duration `json:"response_time_ms"`
	CheckedAt    time.Time     `json:"checked_at"`
}

// ServiceStore manages registered services with thread-safe access.
type ServiceStore struct {
	mu       sync.Mutex
	services []Service
}

func (s *ServiceStore) Add(svc Service) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.services = append(s.services, svc)
}

func (s *ServiceStore) All() []Service {
	s.mu.Lock()
	defer s.mu.Unlock()
	result := make([]Service, len(s.services))
	copy(result, s.services)
	return result
}

func (s *ServiceStore) FindByName(name string) (Service, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, svc := range s.services {
		if svc.Name == name {
			return svc, true
		}
	}
	return Service{}, false
}

// loggingMiddleware logs every incoming request with method, path, and duration.
func loggingMiddleware(logger *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		next.ServeHTTP(w, r)
		logger.Info("request handled",
			"method", r.Method,
			"path", r.URL.Path,
			"duration_ms", time.Since(start).Milliseconds(),
		)
	})
}

// checkService performs an HTTP GET to the service URL and returns the result.
func checkService(svc Service) CheckResult {
	client := &http.Client{Timeout: 5 * time.Second}
	start := time.Now()
	resp, err := client.Get(svc.URL)
	duration := time.Since(start)

	result := CheckResult{
		Name:         svc.Name,
		URL:          svc.URL,
		ResponseTime: duration / time.Millisecond,
		CheckedAt:    time.Now(),
	}

	if err != nil {
		result.Status = "unhealthy"
		return result
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 200 && resp.StatusCode < 400 {
		result.Status = "healthy"
	} else {
		result.Status = "unhealthy"
	}
	return result
}

// ReportEntry holds uptime stats for a single service.
type ReportEntry struct {
	Name          string  `json:"name"`
	URL           string  `json:"url"`
	UptimePercent float64 `json:"uptime_percent"`
	AvgResponseMs int64   `json:"avg_response_time_ms"`
	TotalChecks   int     `json:"total_checks"`
	LastChecked   string  `json:"last_checked"`
}

// HealthHistory stores the last N check results per service.
type HealthHistory struct {
	mu      sync.Mutex
	records map[string][]CheckResult
	maxSize int
}

// NewHealthHistory creates a history store that retains maxSize results per service.
func NewHealthHistory(maxSize int) *HealthHistory {
	return &HealthHistory{
		records: make(map[string][]CheckResult),
		maxSize: maxSize,
	}
}

// Record adds a check result to the history, trimming older entries.
func (h *HealthHistory) Record(result CheckResult) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.records[result.Name] = append(h.records[result.Name], result)
	if len(h.records[result.Name]) > h.maxSize {
		h.records[result.Name] = h.records[result.Name][len(h.records[result.Name])-h.maxSize:]
	}
}

// Report generates uptime statistics for all tracked services.
func (h *HealthHistory) Report() []ReportEntry {
	h.mu.Lock()
	defer h.mu.Unlock()
	var entries []ReportEntry
	for name, results := range h.records {
		if len(results) == 0 {
			continue
		}
		var healthy int
		var totalMs int64
		for _, r := range results {
			if r.Status == "healthy" {
				healthy++
			}
			totalMs += int64(r.ResponseTime)
		}
		entry := ReportEntry{
			Name:          name,
			URL:           results[0].URL,
			UptimePercent: float64(healthy) / float64(len(results)) * 100,
			AvgResponseMs: totalMs / int64(len(results)),
			TotalChecks:   len(results),
			LastChecked:   results[len(results)-1].CheckedAt.Format(time.RFC3339),
		}
		entries = append(entries, entry)
	}
	return entries
}

// startBackgroundChecker launches a goroutine that checks all services on a timer.
func startBackgroundChecker(store *ServiceStore, history *HealthHistory, logger *slog.Logger, interval time.Duration) {
	ticker := time.NewTicker(interval)
	go func() {
		for range ticker.C {
			services := store.All()
			if len(services) == 0 {
				continue
			}
			logger.Info("background check starting", "service_count", len(services))
			for _, svc := range services {
				result := checkService(svc)
				history.Record(result)
			}
			logger.Info("background check complete")
		}
	}()
}

func main() {
	//Structured Logging
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	slog.SetDefault(logger)

	store := &ServiceStore{} // Create history store (keeps last 100 results per service) and start background worker.
	history := NewHealthHistory(100)
	startBackgroundChecker(store, history, logger, 30*time.Second)
	mux := http.NewServeMux()

	//GET /health - does a simple liveness check
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{
			"status": "ok",
			"time":   time.Now().Format(time.RFC3339),
		})
	})

	// GET /services - list all registered services
	mux.HandleFunc("GET /services", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(store.All())
	})

	// POST /services - register a new service
	mux.HandleFunc("POST /services", func(w http.ResponseWriter, r *http.Request) {
		var svc Service
		if err := json.NewDecoder(r.Body).Decode(&svc); err != nil {
			http.Error(w, `{"error":"invalid JSON"}`, http.StatusBadRequest)
			return
		}
		if svc.Name == "" || svc.URL == "" {
			http.Error(w, `{"error":"name and url are required"}`, http.StatusBadRequest)
			return
		}
		store.Add(svc)
		logger.Info("service registered", "name", svc.Name, "url", svc.URL)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(svc)
	})

	// GET /services/{name}/check - check a single service
	mux.HandleFunc("GET /services/{name}/check", func(w http.ResponseWriter, r *http.Request) {
		name := r.PathValue("name")
		svc, found := store.FindByName(name)
		if !found {
			http.Error(w, `{"error":"service not found"}`, http.StatusNotFound)
			return
		}
		result := checkService(svc)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(result)
	})

	// GET /check-all - check all services concurrently
	mux.HandleFunc("GET /check-all", func(w http.ResponseWriter, r *http.Request) {
		services := store.All()
		if len(services) == 0 {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode([]CheckResult{})
			return
		}

		results := make(chan CheckResult, len(services))
		for _, svc := range services {
			go func(s Service) {
				results <- checkService(s)
			}(svc)
		}

		var allResults []CheckResult
		for range services {
			allResults = append(allResults, <-results)
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(allResults)
	})

	// GET /report - return uptime statistics from background checks
	mux.HandleFunc("GET /report", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(history.Report())
	})

	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	server := &http.Server{
		Addr:         ":" + port,
		Handler:      loggingMiddleware(logger, mux),
		ReadTimeout:  5 * time.Second,
		WriteTimeout: 10 * time.Second,
	}

	// Graceful shutdown: listen for SIGINT or SIGTERM.
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		logger.Info("server starting", "port", port)
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Error("server failed", "error", err)
			os.Exit(1)
		}
	}()

	// Block until a signal is received.
	sig := <-quit
	logger.Info("shutting down", "signal", sig.String())

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	if err := server.Shutdown(ctx); err != nil {
		logger.Error("shutdown error", "error", err)
		os.Exit(1)
	}
	logger.Info("server stopped")
}
