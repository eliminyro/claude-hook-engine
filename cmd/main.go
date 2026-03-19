package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/eliminyro/claude-hook-engine/internal/exec"
	"github.com/eliminyro/claude-hook-engine/internal/hook"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: claude-hook-engine <pre|post|exec> [args...]")
		os.Exit(1)
	}

	switch os.Args[1] {
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
	case "exec":
		if err := runExec(os.Args[2:]); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	default:
		fmt.Fprintf(os.Stderr, "unknown subcommand: %s\n", os.Args[1])
		os.Exit(1)
	}
}

func rulesPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".claude", "hooks", "rules.json")
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

func runExec(args []string) error {
	return exec.Run(args)
}
