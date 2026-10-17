package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/patlivet/domain-suggestion-engine/internal/api"
)

// Injected at build time via -ldflags.
var (
	version = "dev"
	builtAt = "unknown"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	slog.SetDefault(logger)

	cfg := api.Config{
		GeminiAPIKey:    os.Getenv("GEMINI_API_KEY"),
		GeminiModel:     envString("GEMINI_MODEL", "gemini-3.5-flash-lite"),
		CacheSize:       envInt("CACHE_SIZE", 500), // 0 disables the response cache
		LLMShare:        envFloat("LLM_SHARE", 0.60),
		CommonWordSlots: envInt("COMMON_WORD_SLOTS", 2),
		AlgoEnabled:     envBool("ALGO_ENABLED", true),
		// Only hacks by default. Measured over 3 runs each, compounds and
		// affixes improve specificity but the judge prefers hacks alone
		// (53.2% of 1686 answers), and specificity does not track human
		// ratings. exact is worse still: it returns the query word itself.
		ActiveGenerators: envStrings("GENERATORS", "hacks"),
		AllGenerators:    []string{"hacks", "exact", "compounds", "affixes"},
		LLMVariants:      envStrings("LLM_VARIANTS", "evocative,wordplay,crafted"),
		// 2 shards = 6 parallel calls: ~23% faster and judged better, for ~38%
		// more cost (eval-results/README.md, 2026-10-05).
		LLMShards: envInt("LLM_SHARDS", 2),
		// 48 in-flight Gemini calls is eight requests' worth of six calls each.
		GeminiMaxConcurrent: envInt("GEMINI_MAX_CONCURRENT", 48),
		CheckAvailability:   envBool("CHECK_AVAILABILITY", false),
		DNSResolverAddr:     envString("DNS_RESOLVER", "1.1.1.1:53"),
		DNSCacheSize:        envInt("DNS_CACHE_SIZE", 5000),
		DNSCacheTTL:         envDuration("DNS_CACHE_TTL", 1*time.Hour),
		Version:             version,
		BuiltAt:             builtAt,
	}

	if len(configErrs) > 0 {
		for _, err := range configErrs {
			slog.Error("invalid configuration", "err", err)
		}
		os.Exit(1)
	}

	handler, err := api.NewHandler(cfg)
	if err != nil {
		slog.Error("failed to initialize handler", "err", err)
		os.Exit(1)
	}

	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	srv := &http.Server{
		Addr:         ":" + port,
		Handler:      handler,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 15 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	slog.Info("starting server", "port", port, "version", version, "built_at", builtAt,
		"algo_enabled", cfg.AlgoEnabled, "generators", cfg.ActiveGenerators)

	go func() {
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			slog.Error("server error", "err", err)
			os.Exit(1)
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	slog.Info("shutting down")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		slog.Error("shutdown error", "err", err)
	}
}

// configErrs collects environment values that did not parse. Falling back to
// the default silently (LLM_SHARDS=two becoming 2) hid mistakes; start-up now
// fails and names them.
var configErrs []error

func envInt(key string, def int) int {
	if v := os.Getenv(key); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			configErrs = append(configErrs, fmt.Errorf("%s=%q: not an integer", key, v))
			return def
		}
		return n
	}
	return def
}

func envString(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func envFloat(key string, def float64) float64 {
	if v := os.Getenv(key); v != "" {
		f, err := strconv.ParseFloat(v, 64)
		if err != nil {
			configErrs = append(configErrs, fmt.Errorf("%s=%q: not a number", key, v))
			return def
		}
		return f
	}
	return def
}

func envBool(key string, def bool) bool {
	if v := os.Getenv(key); v != "" {
		b, err := strconv.ParseBool(v)
		if err != nil {
			configErrs = append(configErrs, fmt.Errorf("%s=%q: not true or false", key, v))
			return def
		}
		return b
	}
	return def
}

func envStrings(key, def string) []string {
	v := os.Getenv(key)
	if v == "" {
		v = def
	}
	parts := strings.Split(v, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

func envDuration(key string, def time.Duration) time.Duration {
	if v := os.Getenv(key); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil {
			configErrs = append(configErrs, fmt.Errorf("%s=%q: not a duration", key, v))
			return def
		}
		return d
	}
	return def
}
