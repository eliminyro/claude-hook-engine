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

// parseArgs finds the "--" separator and returns the command string after it.
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
	return strings.Join(cmdParts, " "), nil
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
		vaultProvider := buildVaultProvider()
		gcpProvider := secrets.NewGCPProvider()

		var mu sync.Mutex
		var wg sync.WaitGroup
		errCh := make(chan error, len(refs))

		for _, ref := range refs {
			ref := ref
			wg.Add(1)
			go func() {
				defer wg.Done()
				var val string
				var fetchErr error

				switch ref.Provider {
				case "vault":
					if vaultProvider == nil {
						fetchErr = fmt.Errorf("vault: VAULT_ADDR and VAULT_TOKEN (or ~/.vault-token) required")
					} else {
						fetchErr = requireField(ref)
						if fetchErr == nil {
							val, fetchErr = vaultProvider.Fetch(ref)
						}
					}
				case "gcp":
					val, fetchErr = gcpProvider.Fetch(ref)
				default:
					fetchErr = fmt.Errorf("unknown provider: %s", ref.Provider)
				}

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

// requireField ensures a vault fetch template has a field specified.
func requireField(ref secrets.TemplateRef) error {
	if ref.Provider == "vault" && ref.Field == "" {
		return fmt.Errorf("vault: field required in %s — use {{vault:%s@%s:<field>}}", ref.Raw, ref.Mount, ref.Path)
	}
	return nil
}

// handleList resolves a list-mode template and returns formatted output.
func handleList(ref secrets.TemplateRef) (string, error) {
	switch ref.Provider {
	case "vault":
		return handleVaultList(ref)
	case "gcp":
		return handleGCPList(ref)
	default:
		return "", fmt.Errorf("unknown provider: %s", ref.Provider)
	}
}

func handleVaultList(ref secrets.TemplateRef) (string, error) {
	vaultProvider := buildVaultProvider()
	if vaultProvider == nil {
		return "", fmt.Errorf("vault: VAULT_ADDR and VAULT_TOKEN (or ~/.vault-token) required")
	}

	var items []string
	var header string
	var err error

	switch ref.Level {
	case secrets.LevelEngines:
		header = "KV engines:"
		items, err = vaultProvider.ListEngines()
	case secrets.LevelPaths:
		header = fmt.Sprintf("Paths in %s:", ref.Mount)
		items, err = vaultProvider.ListPaths(ref.Mount)
	case secrets.LevelFields:
		header = fmt.Sprintf("Fields at %s@%s:", ref.Mount, ref.Path)
		items, err = vaultProvider.ListFields(ref.Mount, ref.Path)
	default:
		return "", fmt.Errorf("vault: unknown list level: %s", ref.Level)
	}

	if err != nil {
		return "", err
	}

	return formatList(header, items), nil
}

func handleGCPList(ref secrets.TemplateRef) (string, error) {
	gcpProvider := secrets.NewGCPProvider()

	header := fmt.Sprintf("Secrets in %s:", ref.Project)
	items, err := gcpProvider.ListSecrets(ref.Project)
	if err != nil {
		return "", err
	}

	return formatList(header, items), nil
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

// buildVaultProvider creates a VaultProvider from environment variables or ~/.vault-token.
// Returns nil if no credentials are available.
func buildVaultProvider() *secrets.VaultProvider {
	addr := os.Getenv("VAULT_ADDR")
	token := os.Getenv("VAULT_TOKEN")

	if token == "" {
		// Try ~/.vault-token
		home, err := os.UserHomeDir()
		if err == nil {
			data, err := os.ReadFile(home + "/.vault-token")
			if err == nil {
				token = strings.TrimSpace(string(data))
			}
		}
	}

	if addr == "" || token == "" {
		return nil
	}

	return secrets.NewVaultProvider(addr, token)
}
