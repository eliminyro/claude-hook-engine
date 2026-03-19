package secrets

// Provider fetches a secret value for the given TemplateRef.
type Provider interface {
	Fetch(ref TemplateRef) (string, error)
}
