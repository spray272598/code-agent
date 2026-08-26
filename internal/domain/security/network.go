package security

import (
	"fmt"
	"strings"
	"sync"
)

type NetworkEnforcer struct {
	mu     sync.RWMutex
	policy NetworkPolicy
}

func NewNetworkEnforcer(policy NetworkPolicy) *NetworkEnforcer {
	return &NetworkEnforcer{policy: policy}
}

type NetworkPolicy struct {
	BlockAll       bool     `yaml:"block_all" json:"blockAll"`
	AllowSites     []string `yaml:"allow_sites" json:"allowSites"`
	DenySites      []string `yaml:"deny_sites" json:"denySites"`
	AllowProtocols []string `yaml:"allow_protocols" json:"allowProtocols"`
}

func DefaultNetworkPolicy() NetworkPolicy {
	return NetworkPolicy{
		BlockAll:       false,
		AllowSites:     []string{"api.github.com", "registry.npmjs.org", "pypi.org"},
		DenySites:      nil,
		AllowProtocols: []string{"https", "http"},
	}
}

func (e *NetworkEnforcer) CanConnect(host, port string) (bool, string) {
	e.mu.RLock()
	defer e.mu.RUnlock()

	if e.policy.BlockAll {
		return false, "all network blocked by policy"
	}

	host = strings.ToLower(strings.TrimSpace(host))

	for _, denied := range e.policy.DenySites {
		deniedLower := strings.ToLower(denied)
		if matchesSite(host, deniedLower) {
			return false, fmt.Sprintf("site denied: %s", denied)
		}
	}

	for _, allowed := range e.policy.AllowSites {
		allowedLower := strings.ToLower(allowed)
		if matchesSite(host, allowedLower) {
			return true, ""
		}
	}

	if len(e.policy.AllowSites) > 0 {
		return false, fmt.Sprintf("site not in allowlist: %s", host)
	}

	return true, ""
}

func (e *NetworkEnforcer) CanUseProtocol(protocol string) (bool, string) {
	e.mu.RLock()
	defer e.mu.RUnlock()
	protocol = strings.ToLower(strings.TrimSpace(protocol))
	if len(e.policy.AllowProtocols) == 0 {
		return true, ""
	}
	for _, p := range e.policy.AllowProtocols {
		if strings.EqualFold(p, protocol) {
			return true, ""
		}
	}
	return false, fmt.Sprintf("protocol not allowed: %s", protocol)
}

func (e *NetworkEnforcer) FilterURL(urlStr string) Decision {
	e.mu.RLock()
	defer e.mu.RUnlock()

	urlStr = strings.TrimSpace(urlStr)
	if urlStr == "" {
		return Decision{Action: ActionAllow, Reason: "empty url"}
	}

	for _, denied := range e.policy.DenySites {
		if strings.Contains(strings.ToLower(urlStr), strings.ToLower(denied)) {
			return Decision{
				Action: ActionDeny,
				Reason: fmt.Sprintf("url matches denied site: %s", denied),
				RuleID: "network_deny",
			}
		}
	}

	if len(e.policy.AllowSites) > 0 {
		allowed := false
		for _, allowedSite := range e.policy.AllowSites {
			if strings.Contains(strings.ToLower(urlStr), strings.ToLower(allowedSite)) {
				allowed = true
				break
			}
		}
		if !allowed {
			return Decision{
				Action: ActionDeny,
				Reason: "url not in allowlist",
				RuleID: "network_allowlist",
			}
		}
	}

	proto := detectProtocol(urlStr)
	if proto != "" {
		ok, _ := e.CanUseProtocol(proto)
		if !ok {
			return Decision{
				Action: ActionDeny,
				Reason: fmt.Sprintf("protocol not allowed: %s", proto),
				RuleID: "network_protocol",
			}
		}
	}

	return Decision{Action: ActionAllow}
}

func (e *NetworkEnforcer) BlockAll(block bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.policy.BlockAll = block
}

func (e *NetworkEnforcer) AddAllowedSite(site string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	for _, s := range e.policy.AllowSites {
		if strings.EqualFold(s, site) {
			return
		}
	}
	e.policy.AllowSites = append(e.policy.AllowSites, site)
}

func (e *NetworkEnforcer) AddDeniedSite(site string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	for _, s := range e.policy.DenySites {
		if strings.EqualFold(s, site) {
			return
		}
	}
	e.policy.DenySites = append(e.policy.DenySites, site)
}

func (e *NetworkEnforcer) RemoveAllowedSite(site string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	for i, s := range e.policy.AllowSites {
		if strings.EqualFold(s, site) {
			e.policy.AllowSites = append(e.policy.AllowSites[:i], e.policy.AllowSites[i+1:]...)
			return
		}
	}
}

func (e *NetworkEnforcer) RemoveDeniedSite(site string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	for i, s := range e.policy.DenySites {
		if strings.EqualFold(s, site) {
			e.policy.DenySites = append(e.policy.DenySites[:i], e.policy.DenySites[i+1:]...)
			return
		}
	}
}

func (e *NetworkEnforcer) Snapshot() NetworkPolicy {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.policy
}

func matchesSite(host, pattern string) bool {
	if host == pattern {
		return true
	}
	if strings.HasSuffix(host, "."+pattern) {
		return true
	}
	if strings.HasPrefix(pattern, "*.") {
		suffix := pattern[1:]
		if strings.HasSuffix(host, suffix) {
			return true
		}
	}
	return false
}

func detectProtocol(url string) string {
	lower := strings.ToLower(url)
	if strings.HasPrefix(lower, "https://") {
		return "https"
	}
	if strings.HasPrefix(lower, "http://") {
		return "http"
	}
	if strings.HasPrefix(lower, "ssh://") {
		return "ssh"
	}
	if strings.HasPrefix(lower, "ftp://") {
		return "ftp"
	}
	return ""
}
