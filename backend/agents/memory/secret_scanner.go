package memory

import (
	"regexp"
	"strings"
	"sync"
)

// ---------------------------------------------------------------------------
// M13.I1 · Secret scanner for team memory (PSR M22174).
//
// Ports `src/services/teamMemorySync/secretScanner.ts`.
//
// Scans content for credentials before upload so secrets never leave
// the user's machine. Uses a curated subset of high-confidence rules
// from gitleaks (https://github.com/gitleaks/gitleaks, MIT license) —
// only rules with distinctive prefixes that have near-zero false-
// positive rates are included.
//
// JS regex notes applied to Go:
//   - Go regex supports (?i) inline so mode groups translate directly.
//   - Boundary assertions (\b) and $ work identically.
// ---------------------------------------------------------------------------

// SecretMatch is a single scanner result (one per rule that fired).
// The actual matched text is intentionally NOT returned — we never log
// or display secret values.
type SecretMatch struct {
	// RuleID is the gitleaks rule ID (kebab-case).
	RuleID string
	// Label is a human-readable label derived from the rule ID.
	Label string
}

// secretRule holds a single scanner pattern.
type secretRule struct {
	id     string
	source string
}

// Anthropic API key prefix, assembled at runtime so the literal byte
// sequence isn't present in the binary.
var antKeyPfx = strings.Join([]string{"sk", "ant", "api"}, "-")

// secretRules is the curated rule set. Ordered roughly by likelihood
// of appearing in dev-team content.
var secretRules = []secretRule{
	// Cloud providers
	{id: "aws-access-token", source: `\b((?:A3T[A-Z0-9]|AKIA|ASIA|ABIA|ACCA)[A-Z2-7]{16})\b`},
	{id: "gcp-api-key", source: `\b(AIza[\w\-]{35})(?:[\x60'";\s]|\\[nr]|$)`},
	{id: "digitalocean-pat", source: `\b(dop_v1_[a-f0-9]{64})(?:[\x60'";\s]|\\[nr]|$)`},
	{id: "digitalocean-access-token", source: `\b(doo_v1_[a-f0-9]{64})(?:[\x60'";\s]|\\[nr]|$)`},

	// AI APIs
	{id: "anthropic-api-key", source: `\b(` + antKeyPfx + `03-[a-zA-Z0-9_\-]{93}AA)(?:[\x60'";\s]|\\[nr]|$)`},
	{id: "anthropic-admin-api-key", source: `\b(sk-ant-admin01-[a-zA-Z0-9_\-]{93}AA)(?:[\x60'";\s]|\\[nr]|$)`},
	{id: "openai-api-key", source: `\b(sk-(?:proj|svcacct|admin)-(?:[A-Za-z0-9_\-]{74}|[A-Za-z0-9_\-]{58})T3BlbkFJ(?:[A-Za-z0-9_\-]{74}|[A-Za-z0-9_\-]{58})\b|sk-[a-zA-Z0-9]{20}T3BlbkFJ[a-zA-Z0-9]{20})(?:[\x60'";\s]|\\[nr]|$)`},
	{id: "huggingface-access-token", source: `\b(hf_[a-zA-Z]{34})(?:[\x60'";\s]|\\[nr]|$)`},

	// Version control
	{id: "github-pat", source: `ghp_[0-9a-zA-Z]{36}`},
	{id: "github-fine-grained-pat", source: `github_pat_\w{82}`},
	{id: "github-app-token", source: `(?:ghu|ghs)_[0-9a-zA-Z]{36}`},
	{id: "github-oauth", source: `gho_[0-9a-zA-Z]{36}`},
	{id: "github-refresh-token", source: `ghr_[0-9a-zA-Z]{36}`},
	{id: "gitlab-pat", source: `glpat-[\w\-]{20}`},
	{id: "gitlab-deploy-token", source: `gldt-[0-9a-zA-Z_\-]{20}`},

	// Communication
	{id: "slack-bot-token", source: `xoxb-[0-9]{10,13}-[0-9]{10,13}[a-zA-Z0-9\-]*`},
	{id: "slack-user-token", source: `xox[pe](?:-[0-9]{10,13}){3}-[a-zA-Z0-9\-]{28,34}`},
	{id: "slack-app-token", source: `(?i)xapp-\d-[A-Z0-9]+-\d+-[a-z0-9]+`},
	{id: "twilio-api-key", source: `SK[0-9a-fA-F]{32}`},
	{id: "sendgrid-api-token", source: `\b(SG\.[a-zA-Z0-9=_\-.]{66})(?:[\x60'";\s]|\\[nr]|$)`},

	// Dev tooling
	{id: "npm-access-token", source: `\b(npm_[a-zA-Z0-9]{36})(?:[\x60'";\s]|\\[nr]|$)`},
	{id: "pypi-upload-token", source: `pypi-AgEIcHlwaS5vcmc[\w\-]{50,1000}`},
	{id: "pulumi-api-token", source: `\b(pul-[a-f0-9]{40})(?:[\x60'";\s]|\\[nr]|$)`},

	// Observability
	{id: "grafana-api-key", source: `\b(eyJrIjoi[A-Za-z0-9+/]{70,400}={0,3})(?:[\x60'";\s]|\\[nr]|$)`},
	{id: "grafana-cloud-api-token", source: `\b(glc_[A-Za-z0-9+/]{32,400}={0,3})(?:[\x60'";\s]|\\[nr]|$)`},
	{id: "sentry-user-token", source: `\b(sntryu_[a-f0-9]{64})(?:[\x60'";\s]|\\[nr]|$)`},

	// Payment
	{id: "stripe-access-token", source: `\b((?:sk|rk)_(?:test|live|prod)_[a-zA-Z0-9]{10,99})(?:[\x60'";\s]|\\[nr]|$)`},
	{id: "shopify-access-token", source: `shpat_[a-fA-F0-9]{32}`},
	{id: "shopify-shared-secret", source: `shpss_[a-fA-F0-9]{32}`},

	// Crypto
	{id: "private-key", source: `(?i)-----BEGIN[ A-Z0-9_\-]{0,100}PRIVATE KEY(?: BLOCK)?-----[\s\S\-]{64,}?-----END[ A-Z0-9_\-]{0,100}PRIVATE KEY(?: BLOCK)?-----`},
}

// compiledRules is lazily compiled on first scan.
var (
	compiledOnce  sync.Once
	compiledRules []compiledRule
)

type compiledRule struct {
	id string
	re *regexp.Regexp
}

func getCompiledRules() []compiledRule {
	compiledOnce.Do(func() {
		compiledRules = make([]compiledRule, 0, len(secretRules))
		for _, r := range secretRules {
			re, err := regexp.Compile(r.source)
			if err != nil {
				// Skip rules that fail to compile (should not happen
				// with the curated set but guards against regression).
				continue
			}
			compiledRules = append(compiledRules, compiledRule{id: r.id, re: re})
		}
	})
	return compiledRules
}

// specialCaseLabels maps gitleaks kebab-case words to their canonical
// capitalisation.
var specialCaseLabels = map[string]string{
	"aws":          "AWS",
	"gcp":          "GCP",
	"api":          "API",
	"pat":          "PAT",
	"ad":           "AD",
	"tf":           "TF",
	"oauth":        "OAuth",
	"npm":          "NPM",
	"pypi":         "PyPI",
	"jwt":          "JWT",
	"github":       "GitHub",
	"gitlab":       "GitLab",
	"openai":       "OpenAI",
	"digitalocean": "DigitalOcean",
	"huggingface":  "HuggingFace",
	"hashicorp":    "HashiCorp",
	"sendgrid":     "SendGrid",
}

// RuleIDToLabel converts a gitleaks rule ID (kebab-case) to a human-
// readable label. e.g. "github-pat" → "GitHub PAT".
func RuleIDToLabel(ruleID string) string {
	parts := strings.Split(ruleID, "-")
	for i, p := range parts {
		if special, ok := specialCaseLabels[p]; ok {
			parts[i] = special
		} else if len(p) > 0 {
			parts[i] = strings.ToUpper(p[:1]) + p[1:]
		}
	}
	return strings.Join(parts, " ")
}

// ScanForSecrets scans content for potential secrets. Returns one
// match per rule that fired (deduplicated by rule ID). The actual
// matched text is intentionally NOT returned.
func ScanForSecrets(content string) []SecretMatch {
	rules := getCompiledRules()
	var matches []SecretMatch
	seen := make(map[string]bool)

	for _, rule := range rules {
		if seen[rule.id] {
			continue
		}
		if rule.re.MatchString(content) {
			seen[rule.id] = true
			matches = append(matches, SecretMatch{
				RuleID: rule.id,
				Label:  RuleIDToLabel(rule.id),
			})
		}
	}
	return matches
}

// GetSecretLabel returns a human-readable label for a rule ID.
func GetSecretLabel(ruleID string) string {
	return RuleIDToLabel(ruleID)
}
