// Package model implements M3 / 3.1 multi-model routing and cost control.
//
// The router maps an intent type (normal / deep / team) to a concrete LLM
// endpoint. It falls back to the default route when no specific route is
// configured, and further falls back when a route has no usable API key —
// so a partially configured deployment never fails on the first request.
package model

import (
	"fmt"
	"strings"
)

// ModelRoute is a concrete LLM endpoint selection.
type ModelRoute struct {
	MatchIntent string  // "normal" | "deep" | "team" | "default"
	Provider    string  // openai-compatible provider id (informational)
	Model       string  // model id, e.g. "deepseek-ai/DeepSeek-V3"
	APIBase     string  // base URL (empty → SDK default)
	APIKey      string  // may be empty → route considered unusable, fall back
	// Per-1k-token prices (USD) for cost estimation & budget control.
	CostPer1kIn  float64
	CostPer1kOut float64
}

// Usable reports whether this route can actually serve a request.
func (r ModelRoute) Usable() bool {
	return r.Model != "" && r.APIKey != ""
}

// Router selects a ModelRoute for a given intent, with default + usability
// fallbacks.
type Router struct {
	routes  map[string]ModelRoute
	def     ModelRoute
	order   []string // match priority (intent names), default last
}

// NewRouter builds a router from explicit routes. The route whose
// MatchIntent == "default" becomes the fallback; if absent, the first
// provided route is used as default.
func NewRouter(routes []ModelRoute) *Router {
	rt := &Router{routes: map[string]ModelRoute{}}
	for _, r := range routes {
		key := strings.ToLower(strings.TrimSpace(r.MatchIntent))
		if key == "" {
			key = "default"
		}
		rt.routes[key] = r
		if key == "default" {
			rt.def = r
		} else {
			rt.order = append(rt.order, key)
		}
	}
	if rt.def.Model == "" && len(routes) > 0 {
		rt.def = routes[0]
	}
	return rt
}

// Select returns the best route for an intent type ("normal"/"deep"/"team").
// It prefers an intent-specific, usable route, then the default usable route.
// If nothing is usable it returns the default route anyway (caller decides).
func (rt *Router) Select(intent string) ModelRoute {
	key := strings.ToLower(strings.TrimSpace(intent))
	if r, ok := rt.routes[key]; ok && r.Usable() {
		return r
	}
	// Fallback to default if the specific route is missing or unusable.
	if rt.def.Usable() {
		return rt.def
	}
	// Default also unusable: return the specific (possibly unkeyed) route so
	// the failure is surfaced at model-init time with a clear error.
	if r, ok := rt.routes[key]; ok {
		return r
	}
	return rt.def
}

// Candidates returns the configured intent keys (for diagnostics).
func (rt *Router) Candidates() []string {
	out := make([]string, 0, len(rt.order))
	out = append(out, rt.order...)
	if rt.def.MatchIntent != "" {
		out = append(out, rt.def.MatchIntent)
	}
	return out
}

// Validate reports a human-readable problem if the router cannot serve any
// request, or empty string if at least one usable route exists.
func (rt *Router) Validate() string {
	if rt.def.Usable() {
		return ""
	}
	for _, k := range rt.order {
		if rt.routes[k].Usable() {
			return ""
		}
	}
	return fmt.Sprintf("no usable model route (default model=%q key=%q)", rt.def.Model, maskKey(rt.def.APIKey))
}

func maskKey(k string) string {
	if k == "" {
		return "<empty>"
	}
	return "<set>"
}
