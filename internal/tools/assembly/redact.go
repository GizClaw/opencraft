package assembly

import (
	"regexp"

	"github.com/GizClaw/flowcraft/core/errdefs"
	toolmiddleware "github.com/GizClaw/flowcraft/core/tool/middleware"
)

// RedactRuleSettings is one regex redaction rule from the deploy
// document. Pattern uses Go's regexp (RE2) syntax; an empty
// Replacement falls back to toolmiddleware.DefaultRedaction.
type RedactRuleSettings struct {
	Pattern     string `json:"pattern"`
	Replacement string `json:"replacement,omitempty"`
}

// RedactSettings enables model-facing stripping of secrets from tool
// results. Rules also always apply to the audit trail (see audit.go),
// independent of this switch.
type RedactSettings struct {
	Enabled bool                 `json:"enabled"`
	Rules   []RedactRuleSettings `json:"rules,omitempty"`
}

// compileRedactRules validates and compiles the configured rules.
func compileRedactRules(specs []RedactRuleSettings) ([]toolmiddleware.RedactRule, error) {
	rules := make([]toolmiddleware.RedactRule, 0, len(specs))
	for i, spec := range specs {
		if spec.Pattern == "" {
			return nil, errdefs.Validationf(
				"tool middleware: redact.rules[%d].pattern is required", i)
		}
		re, err := regexp.Compile(spec.Pattern)
		if err != nil {
			return nil, errdefs.Validationf(
				"tool middleware: redact.rules[%d].pattern: %v", i, err)
		}
		rules = append(rules, toolmiddleware.RedactRule{
			Pattern:     re,
			Replacement: spec.Replacement,
		})
	}
	return rules, nil
}
