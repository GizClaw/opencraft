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

// CompileTextRedactor compiles the configured rules into a whole-string
// rewriter, or returns nil when redaction is off (or configured with no
// rules). Tools that persist free text reuse the middleware's rule
// shape, so the deploy document says once what a secret looks like —
// [compileRedactRules] is the same compilation the result middleware
// uses.
func CompileTextRedactor(settings RedactSettings) (func(string) string, error) {
	if !settings.Enabled {
		return nil, nil
	}
	rules, err := compileRedactRules(settings.Rules)
	if err != nil {
		return nil, err
	}
	if len(rules) == 0 {
		return nil, nil
	}
	return func(text string) string {
		for _, rule := range rules {
			replacement := rule.Replacement
			if replacement == "" {
				replacement = toolmiddleware.DefaultRedaction
			}
			text = rule.Pattern.ReplaceAllString(text, replacement)
		}
		return text
	}, nil
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
