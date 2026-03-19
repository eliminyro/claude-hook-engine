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

// Prepare parses args, resolves all secret templates, and returns the shell path
// and fully-substituted command string ready for execution.
//
// Args must contain "--" as a separator; everything after it is joined as the command.
func Prepare(args []string) (shellPath string, resolvedCmd string, err error) {
	// Find -- separator
	sepIdx := -1
	for i, a := range args {
		if a == "--" {
			sepIdx = i
			break
		}
	}
	if sepIdx < 0 {
		return "", "", fmt.Errorf("exec: missing '--' separator in args")
	}

	cmdParts := args[sepIdx+1:]
	if len(cmdParts) == 0 {
		return "", "", fmt.Errorf("exec: no command provided after '--'")
	}
	cmdStr := strings.Join(cmdParts, " ")

	// Parse templates
	refs, err := secrets.ParseTemplates(cmdStr)
	if err != nil {
		return "", "", fmt.Errorf("exec: parse templates: %w", err)
	}

	// Fetch secrets in parallel
	values := make(map[string]string)
	if len(refs) > 0 {
		// Build providers
		vaultProvider := buildVaultProvider()
		gcpProvider := secrets.NewGCPProvider()

		var mu sync.Mutex
		var wg sync.WaitGroup
		errCh := make(chan error, len(refs))

		for _, ref := range refs {
			ref := ref // capture
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
						val, fetchErr = vaultProvider.Fetch(ref)
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
				return "", "", e
			}
		}
	}

	// Substitute values
	resolvedCmd = secrets.Substitute(cmdStr, values)

	// Resolve shell path
	shellPath, err = goexec.LookPath("sh")
	if err != nil {
		return "", "", fmt.Errorf("exec: cannot find sh: %w", err)
	}

	return shellPath, resolvedCmd, nil
}

// Run prepares the command and replaces the current process via syscall.Exec.
func Run(args []string) error {
	shellPath, resolvedCmd, err := Prepare(args)
	if err != nil {
		return err
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
