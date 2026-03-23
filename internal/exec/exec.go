package exec

import (
	"fmt"
	"os"
	goexec "os/exec"
	"strings"
	"sync"
	"syscall"

	"github.com/eliminyro/claude-hook-engine/internal/secrets"
)

// ProviderConfigs maps provider names to their config maps.
// Set by the caller (cmd/main.go) before calling Run/Prepare.
var ProviderConfigs map[string]map[string]any

// parseArgs finds the "--" separator and returns the command string after it.
// Each arg is shell-quoted so that sh -c preserves the original arg boundaries.
func parseArgs(args []string) (string, error) {
	sepIdx := -1
	for i, a := range args {
		if a == "--" {
			sepIdx = i
			break
		}
	}
	if sepIdx < 0 {
		return "", fmt.Errorf("exec: missing '--' separator in args")
	}
	cmdParts := args[sepIdx+1:]
	if len(cmdParts) == 0 {
		return "", fmt.Errorf("exec: no command provided after '--'")
	}
	quoted := make([]string, len(cmdParts))
	for i, part := range cmdParts {
		quoted[i] = shellQuote(part)
	}
	return strings.Join(quoted, " "), nil
}

// shellQuote wraps a string in single quotes for safe use in sh -c,
// passing it through unchanged if it contains only safe characters.
func shellQuote(s string) string {
	if s == "" {
		return "''"
	}
	safe := true
	for _, c := range s {
		if !((c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') ||
			c == '_' || c == '-' || c == '.' || c == '/' || c == ':' || c == '=' || c == ',' || c == '@') {
			safe = false
			break
		}
	}
	if safe {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'"
}

// Prepare parses args, resolves all secret templates, and returns the shell path
// and fully-substituted command string ready for execution.
//
// If any template is in list mode, the listing output is returned and the command
// is not executed — the caller should print the output and exit.
func Prepare(args []string) (shellPath string, resolvedCmd string, listOutput string, err error) {
	cmdStr, err := parseArgs(args)
	if err != nil {
		return "", "", "", err
	}

	refs, err := secrets.ParseTemplates(cmdStr)
	if err != nil {
		return "", "", "", fmt.Errorf("exec: parse templates: %w", err)
	}

	// Check for list-mode templates — handle them separately
	for _, ref := range refs {
		if ref.Mode == secrets.ModeList {
			output, listErr := handleList(ref)
			if listErr != nil {
				return "", "", "", listErr
			}
			return "", "", output, nil
		}
	}

	// Fetch secrets in parallel
	values := make(map[string]string)
	if len(refs) > 0 {
		// Build providers for each unique provider name
		providers := map[string]secrets.Provider{}
		for _, ref := range refs {
			if _, ok := providers[ref.Provider]; ok {
				continue
			}
			p, buildErr := buildProvider(ref.Provider)
			if buildErr != nil {
				return "", "", "", buildErr
			}
			providers[ref.Provider] = p
		}

		var mu sync.Mutex
		var wg sync.WaitGroup
		errCh := make(chan error, len(refs))

		for _, ref := range refs {
			ref := ref
			wg.Add(1)
			go func() {
				defer wg.Done()
				p := providers[ref.Provider]
				val, fetchErr := p.Fetch(ref)
				if fetchErr != nil {
					errCh <- fetchErr
					return
				}
				mu.Lock()
				values[ref.Raw] = val
				mu.Unlock()
			}()
		}
		wg.Wait()
		close(errCh)

		for e := range errCh {
			if e != nil {
				return "", "", "", e
			}
		}
	}

	resolvedCmd = secrets.Substitute(cmdStr, values)

	shellPath, err = goexec.LookPath("sh")
	if err != nil {
		return "", "", "", fmt.Errorf("exec: cannot find sh: %w", err)
	}

	return shellPath, resolvedCmd, "", nil
}

// handleList resolves a list-mode template using the Lister interface.
func handleList(ref secrets.TemplateRef) (string, error) {
	p, err := buildProvider(ref.Provider)
	if err != nil {
		return "", err
	}

	lister, ok := p.(secrets.Lister)
	if !ok {
		return "", fmt.Errorf("%s: provider does not support listing", ref.Provider)
	}

	header, items, err := lister.List(ref)
	if err != nil {
		return "", err
	}

	return formatList(header, items), nil
}

// buildProvider creates a provider from the registry with config.
func buildProvider(name string) (secrets.Provider, error) {
	cfg := map[string]any{}
	if ProviderConfigs != nil {
		if c, ok := ProviderConfigs[name]; ok {
			cfg = c
		}
	}
	return secrets.BuildProvider(name, cfg)
}

func formatList(header string, items []string) string {
	if len(items) == 0 {
		return header + "\n  (none)"
	}
	var sb strings.Builder
	sb.WriteString(header)
	for _, item := range items {
		sb.WriteString("\n  ")
		sb.WriteString(item)
	}
	return sb.String()
}

// Run prepares the command and replaces the current process via syscall.Exec.
// If the template is a list operation, prints the listing and exits.
func Run(args []string) error {
	shellPath, resolvedCmd, listOutput, err := Prepare(args)
	if err != nil {
		return err
	}

	if listOutput != "" {
		fmt.Println(listOutput)
		return nil
	}

	return syscall.Exec(shellPath, []string{"sh", "-c", resolvedCmd}, os.Environ())
}
