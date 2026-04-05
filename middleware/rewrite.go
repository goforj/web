package middleware

import (
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"github.com/goforj/web"
)

// RewriteConfig configures URL path rewriting.
type RewriteConfig struct {
	Rules      map[string]string
	RegexRules map[*regexp.Regexp]string
}

// DefaultRewriteConfig is the default rewrite config.
var DefaultRewriteConfig = RewriteConfig{}

// Rewrite rewrites the request path using wildcard rules.
func Rewrite(rules map[string]string) web.Middleware {
	config := DefaultRewriteConfig
	config.Rules = rules
	return RewriteWithConfig(config)
}

// RewriteWithConfig rewrites the request path using wildcard and regex rules.
func RewriteWithConfig(config RewriteConfig) web.Middleware {
	if config.Rules == nil && config.RegexRules == nil {
		panic("web: rewrite middleware requires rewrite rules")
	}

	regexRules := map[*regexp.Regexp]string{}
	for pattern, target := range wildcardRewriteRules(config.Rules) {
		regexRules[pattern] = target
	}
	for pattern, target := range config.RegexRules {
		regexRules[pattern] = target
	}

	return func(next web.Handler) web.Handler {
		return func(r web.Context) error {
			req := r.Request()
			if req != nil {
				if err := rewriteRequest(regexRules, req); err != nil {
					return err
				}
				r.SetRequest(req)
			}
			return next(r)
		}
	}
}

func wildcardRewriteRules(rules map[string]string) map[*regexp.Regexp]string {
	if len(rules) == 0 {
		return nil
	}

	regexRules := make(map[*regexp.Regexp]string, len(rules))
	for source, target := range rules {
		pattern := regexp.QuoteMeta(source)
		pattern = strings.ReplaceAll(pattern, `\*`, "(.*?)")
		if strings.HasPrefix(pattern, `\^`) {
			pattern = strings.ReplaceAll(pattern, `\^`, "^")
		}
		pattern += "$"
		regexRules[regexp.MustCompile(pattern)] = target
	}
	return regexRules
}

func rewriteRequest(rules map[*regexp.Regexp]string, req *http.Request) error {
	if len(rules) == 0 || req == nil || req.URL == nil {
		return nil
	}

	rawURI := req.RequestURI
	if rawURI == "" {
		rawURI = req.URL.RequestURI()
	}
	if rawURI != "" && rawURI[0] != '/' {
		prefix := ""
		if req.URL.Scheme != "" {
			prefix = req.URL.Scheme + "://"
		}
		if req.URL.Host != "" {
			prefix += req.URL.Host
		}
		if prefix != "" {
			rawURI = strings.TrimPrefix(rawURI, prefix)
		}
	}

	for pattern, target := range rules {
		replacer := rewriteCaptureTokens(pattern, rawURI)
		if replacer == nil {
			continue
		}

		nextURL, err := req.URL.Parse(replacer.Replace(target))
		if err != nil {
			return err
		}
		req.URL = nextURL
		req.RequestURI = nextURL.RequestURI()
		return nil
	}

	return nil
}

func rewriteCaptureTokens(pattern *regexp.Regexp, input string) *strings.Replacer {
	groups := pattern.FindAllStringSubmatch(input, -1)
	if groups == nil {
		return nil
	}
	values := groups[0][1:]
	replacements := make([]string, 0, len(values)*2)
	for i, value := range values {
		replacements = append(replacements, "$"+strconv.Itoa(i+1), value)
	}
	return strings.NewReplacer(replacements...)
}
