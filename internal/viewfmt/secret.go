package viewfmt

import "regexp"

// These match the dashboard's rule in web/src/features/servers/EnvTable.tsx.
var (
	secretEnvName = regexp.MustCompile(`(?i)(pass(word)?|passwd|secret|token|api[_-]?key|private|credential|auth|cookie|session|signature|salt|dsn|access[_-]?key|jwt|bearer|(^|_)pwd($|_)|(^|_)key$)`)
	urlPassword   = regexp.MustCompile(`(?i)^[a-z][a-z0-9+.-]*://[^/@\s]*:[^@\s]+@`)
	secretQuery   = regexp.MustCompile(`(?i)[?&](api[_-]?key|access[_-]?token|token|secret|password|passwd|pwd|sig|signature|key)=[^&\s]`)
)

// SecretEnv reports whether an environment variable's value is hidden
// until revealed: a name that suggests a secret, or a value that holds a
// password in a URL or a token in a query.
func SecretEnv(name, value string) bool {
	return secretEnvName.MatchString(name) || urlPassword.MatchString(value) || secretQuery.MatchString(value)
}

// SecretMask stands for a hidden value.
const SecretMask = "••••••"
