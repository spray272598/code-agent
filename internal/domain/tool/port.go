package tool

import (
	"context"
	"errors"
	"strings"
	"sync"
	"time"
)

// ITool is the core tool interface used throughout the code-agent project.
type ITool interface {
	Name() string
	Description() string
	InputSchema() map[string]any
	Execute(ctx context.Context, args map[string]any) (Result, error)
}

// Result is the output of a tool execution.
type Result struct {
	Text    string `json:"text"`
	IsError bool   `json:"isError,omitempty"`
}

type ToolCategory string

const (
	CategoryRead   ToolCategory = "read"
	CategoryWrite  ToolCategory = "write"
	CategorySearch ToolCategory = "search"
	CategoryExec   ToolCategory = "exec"
	CategoryGlob   ToolCategory = "glob"
	CategoryPlan   ToolCategory = "plan"
	CategorySkill  ToolCategory = "skill"
	CategoryMCP    ToolCategory = "mcp"
	CategoryWeb    ToolCategory = "web"
	CategoryMemory ToolCategory = "memory"
)

type ToolMetadata struct {
	Name        string
	Version     string
	Author      string
	Tags        []string
	Examples    []string
	Category    ToolCategory
	IsReadOnly  bool
	IsCacheable bool
	IsLongRun   bool
	DependsOn   []string
	Namespace   string
	Deprecated  bool
	Replica     string
}

type IReadTool interface {
	ITool
}

type IWriteTool interface {
	ITool
}

type ISearchTool interface {
	ITool
}

type IExecTool interface {
	ITool
}

type ILongRunTool interface {
	ITool
	ExecuteAsync(ctx context.Context, args map[string]any, onResult func(Result, error)) (string, error)
	GetAsyncResult(ctx context.Context, taskID string) (Result, error)
	CancelAsync(ctx context.Context, taskID string) error
}

type IToolMeta interface {
	Metadata() ToolMetadata
}

type ToolInfo struct {
	Name        string
	Version     string
	Author      string
	Tags        []string
	Category    ToolCategory
	Description string
	IsReadOnly  bool
	IsCacheable bool
	IsLongRun   bool
	DependsOn   []string
	InputSchema map[string]any
	Examples    []string
	Deprecated  bool
	Replica     string
}

func ToolInfoFromMeta(t ITool, meta ToolMetadata) ToolInfo {
	info := ToolInfo{
		Name:        meta.Name,
		Version:     meta.Version,
		Author:      meta.Author,
		Tags:        meta.Tags,
		Category:    meta.Category,
		Description: t.Description(),
		IsReadOnly:  meta.IsReadOnly,
		IsCacheable: meta.IsCacheable,
		IsLongRun:   meta.IsLongRun,
		DependsOn:   meta.DependsOn,
		InputSchema: t.InputSchema(),
		Examples:    meta.Examples,
		Deprecated:  meta.Deprecated,
		Replica:     meta.Replica,
	}
	if info.Name == "" {
		info.Name = t.Name()
	}
	return info
}

func DefaultMeta(t ITool, category ToolCategory) ToolMetadata {
	return ToolMetadata{
		Name:        t.Name(),
		Version:     "1.0.0",
		Category:    category,
		IsReadOnly:  category == CategoryRead || category == CategorySearch || category == CategoryGlob,
		IsCacheable: category == CategoryRead || category == CategorySearch,
	}
}

func (m ToolMetadata) IsValid() bool {
	return m.Name != ""
}

func (m ToolMetadata) FullName() string {
	if m.Namespace != "" {
		return m.Namespace + "." + m.Name
	}
	return m.Name
}

func (m ToolMetadata) Matches(query string) bool {
	lower := strings.ToLower(query)
	if strings.Contains(strings.ToLower(m.Name), lower) {
		return true
	}
	if strings.Contains(strings.ToLower(string(m.Category)), lower) {
		return true
	}
	for _, tag := range m.Tags {
		if strings.Contains(strings.ToLower(tag), lower) {
			return true
		}
	}
	return false
}

func (m ToolMetadata) CompareVersion(other string) int {
	return compareVersions(m.Version, other)
}

func compareVersions(a, b string) int {
	aParts := parseVersion(a)
	bParts := parseVersion(b)
	for i := 0; i < 3; i++ {
		if i < len(aParts) && i < len(bParts) {
			if aParts[i] > bParts[i] {
				return 1
			}
			if aParts[i] < bParts[i] {
				return -1
			}
		} else if i < len(aParts) {
			return 1
		} else if i < len(bParts) {
			return -1
		}
	}
	return 0
}

func parseVersion(v string) []int {
	var parts []int
	current := 0
	for _, r := range v {
		if r >= '0' && r <= '9' {
			current = current*10 + int(r-'0')
		} else if current > 0 {
			parts = append(parts, current)
			current = 0
		}
	}
	if current > 0 {
		parts = append(parts, current)
	}
	return parts
}

type ToolDependency struct {
	Name     string
	Version  string
	Required bool
}

type ToolDependencyGraph struct {
	mu    sync.RWMutex
	edges map[string][]ToolDependency
}

func NewToolDependencyGraph() *ToolDependencyGraph {
	return &ToolDependencyGraph{edges: make(map[string][]ToolDependency)}
}

func (g *ToolDependencyGraph) AddDependency(from, to, version string, required bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.edges[from] = append(g.edges[from], ToolDependency{Name: to, Version: version, Required: required})
}

func (g *ToolDependencyGraph) GetDependencies(name string) []ToolDependency {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return g.edges[name]
}

func (g *ToolDependencyGraph) HasCircularDependency(start string) bool {
	g.mu.RLock()
	defer g.mu.RUnlock()
	visited := make(map[string]bool)
	var dfs func(node string) bool
	dfs = func(node string) bool {
		if visited[node] {
			return true
		}
		visited[node] = true
		for _, dep := range g.edges[node] {
			if dfs(dep.Name) {
				return true
			}
		}
		visited[node] = false
		return false
	}
	return dfs(start)
}

type ToolComposition struct {
	Name        string
	Description string
	Steps       []CompositionStep
}

type CompositionStep struct {
	ToolName  string
	Args      map[string]any
	DependsOn []int
	Condition string
}

func (c ToolComposition) Validate() error {
	for i, step := range c.Steps {
		for _, dep := range step.DependsOn {
			if dep >= i || dep < 0 {
				return ErrInvalidComposition
			}
		}
	}
	return nil
}

type BackgroundTask struct {
	ID        string
	ToolName  string
	Status    string
	Args      map[string]any
	Result    Result
	Error     error
	CreatedAt time.Time
	UpdatedAt time.Time
}

const (
	TaskPending   = "pending"
	TaskRunning   = "running"
	TaskCompleted = "completed"
	TaskFailed    = "failed"
	TaskCancelled = "cancelled"
)

var (
	ErrInvalidComposition = errors.New("invalid tool composition: dependency references are out of order")
	ErrCircularDependency = errors.New("circular tool dependency detected")
	ErrTaskNotFound       = errors.New("background task not found")
	ErrTaskNotCancellable = errors.New("task cannot be cancelled in current state")
)

// Registry is the minimal interface expected by external tool registration
// helpers (e.g. sshtool.RegisterAll). MapRegistry satisfies this.
type Registry interface {
	Register(t ITool)
	RegisterWithMeta(t ITool, meta ToolMetadata)
}

type sessionIDKey struct{}

// SessionIDFrom extracts a session ID from context. Returns "" if not set.
func SessionIDFrom(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	if v, ok := ctx.Value(sessionIDKey{}).(string); ok {
		return v
	}
	return ""
}

// WithSessionID returns a derived context carrying the session ID.
func WithSessionID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, sessionIDKey{}, id)
}
