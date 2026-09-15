package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/eliminyro/claude-hook-engine/internal/hook"
)

// version is the release tag, stamped at link time with
// -ldflags "-X main.version=<tag>". The default marks a non-release build.
var version = "dev"

func main() {
	hook.Version = version

	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: claude-hook-engine <pre|post|session-start|update|version> [args...]")
		os.Exit(1)
	}

	switch os.Args[1] {
	case "version":
		fmt.Println(version)
	case "pre":
		if err := runPre(); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	case "post":
		if err := runPost(); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	case "update":
		if err := runUpdate(); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	case "session-start":
		if err := runSessionStart(); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	default:
		fmt.Fprintf(os.Stderr, "unknown subcommand: %s\n", os.Args[1])
		os.Exit(1)
	}
}

// rulesPath returns the config base path (extensionless; Load tries .json/.yaml/.yml).
// CLAUDE_HOOK_ENGINE_RULES overrides it per-process for every subcommand, letting a
// subprocess use an alternate config without touching the installed ~/.claude/hooks/rules.
func rulesPath() string {
	if p := os.Getenv("CLAUDE_HOOK_ENGINE_RULES"); p != "" {
		return p
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".claude", "hooks", "rules")
}

func runPre() error {
	output, err := hook.HandlePre(os.Stdin, rulesPath())
	if err != nil {
		return err
	}
	if output != "" {
		fmt.Print(output)
	}
	return nil
}

// runUpdate installs the latest release now, whatever the schedule says.
func runUpdate() error {
	line, err := hook.Update(rulesPath())
	if err != nil {
		return fmt.Errorf("update failed: %w", err)
	}
	fmt.Println(line)
	return nil
}

func runPost() error {
	output, err := hook.HandlePost(os.Stdin, rulesPath())
	if err != nil {
		return err
	}
	if output != "" {
		fmt.Print(output)
	}
	return nil
}

func runSessionStart() error {
	output, err := hook.HandleSessionStart(os.Stdin, rulesPath())
	if err != nil {
		return err
	}
	if output != "" {
		fmt.Print(output)
	}
	return nil
}
