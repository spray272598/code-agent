package einoorch

import (
	"os"
	"runtime"
	"strings"
	"time"
)

type PromptMode int

const (
	PromptModeFull PromptMode = iota
	PromptModeCompact
	PromptModeExtend
)

type Audience int

const (
	AudiencePrimary Audience = iota
	AudienceSubagent
)

type PromptContext struct {
	Version          int
	PromptMode       PromptMode
	Audience         Audience
	SystemLabel      string
	RoleInstructions string
	PersonaName      string
	PersonaDesc      string
	OSName           string
	ShellPath        string
	WorkingDirectory string
	CurrentDate      string
	IsNonInteractive bool
	MemoryEnabled    bool
	IncludesBrowser  bool
	UserID           string
	ProjectID        string
	BuildTimestamp   string
}

func NewPromptContext() *PromptContext {
	now := time.Now()
	ctx := &PromptContext{
		Version:        1,
		PromptMode:     PromptModeExtend,
		Audience:       AudiencePrimary,
		SystemLabel:    "Code-Agent",
		OSName:         runtime.GOOS,
		BuildTimestamp: now.Format(time.RFC3339),
		CurrentDate:    now.Format("2006-01-02"),
	}
	if runtime.GOOS == "windows" {
		ctx.ShellPath = os.Getenv("COMSPEC")
		if ctx.ShellPath == "" {
			ctx.ShellPath = "cmd.exe"
		}
	} else {
		ctx.ShellPath = os.Getenv("SHELL")
		if ctx.ShellPath == "" {
			ctx.ShellPath = "/bin/sh"
		}
	}
	ctx.WorkingDirectory, _ = os.Getwd()
	ctx.UserID = os.Getenv("USERNAME")
	if ctx.UserID == "" {
		ctx.UserID = os.Getenv("USER")
	}
	return ctx
}

func (c *PromptContext) UserInfoBlock() string {
	var b strings.Builder
	b.WriteString("<user_info>\n")
	b.WriteString("OS: ")
	b.WriteString(c.OSName)
	b.WriteString("\n")
	if c.ShellPath != "" {
		b.WriteString("Shell: ")
		b.WriteString(c.ShellPath)
		b.WriteString("\n")
	}
	if c.WorkingDirectory != "" {
		b.WriteString("Workspace Path: ")
		b.WriteString(c.WorkingDirectory)
		b.WriteString("\n")
	}
	b.WriteString("Current Date: ")
	b.WriteString(c.CurrentDate)
	b.WriteString("\n")
	b.WriteString("</user_info>")
	return b.String()
}

func (c *PromptContext) Header() string {
	var b strings.Builder
	b.WriteString("You are ")
	b.WriteString(c.SystemLabel)
	b.WriteString(", an AI coding agent operating within the Eino ReAct orchestration framework.\n")
	if c.IsNonInteractive {
		b.WriteString("This is a non-interactive session — you are an autonomous agent completing software engineering tasks.\n")
	} else {
		b.WriteString("This is an interactive session — you assist a user with software engineering tasks.\n")
	}
	b.WriteString("Your main goal is to complete the user's request, denoted within the <user_query> tag.\n")
	return b.String()
}

func (c *PromptContext) NormalizeForSubagent() {
	c.Audience = AudienceSubagent
	c.RoleInstructions = ""
	c.PersonaName = ""
	c.PersonaDesc = ""
	c.MemoryEnabled = false
}
