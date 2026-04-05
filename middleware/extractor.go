package middleware

import (
	"errors"
	"fmt"
	"net/textproto"
	"strings"

	"github.com/goforj/web"
)

const extractorLimit = 20

var (
	errHeaderExtractorValueMissing = errors.New("missing value in request header")
	errHeaderExtractorValueInvalid = errors.New("invalid value in request header")
	errQueryExtractorValueMissing  = errors.New("missing value in the query string")
	errParamExtractorValueMissing  = errors.New("missing value in path params")
	errCookieExtractorValueMissing = errors.New("missing value in cookies")
	errFormExtractorValueMissing   = errors.New("missing value in the form")
)

// ValuesExtractor extracts one or more values from a request.
type ValuesExtractor func(web.Context) ([]string, error)

// CreateExtractors creates extractors from a lookup definition.
func CreateExtractors(lookups string) ([]ValuesExtractor, error) {
	return createExtractors(lookups, "")
}

func createExtractors(lookups string, authScheme string) ([]ValuesExtractor, error) {
	if lookups == "" {
		return nil, nil
	}

	parts := strings.Split(lookups, ",")
	out := make([]ValuesExtractor, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		source, name, cutPrefix, err := parseLookup(part, authScheme)
		if err != nil {
			return nil, err
		}
		switch source {
		case "header":
			out = append(out, headerExtractor(name, cutPrefix))
		case "query":
			out = append(out, queryExtractor(name))
		case "param":
			out = append(out, paramExtractor(name))
		case "cookie":
			out = append(out, cookieExtractor(name))
		case "form":
			out = append(out, formExtractor(name))
		default:
			return nil, invalidConfigError("unsupported extractor source: " + source)
		}
	}
	if len(out) == 0 {
		return nil, invalidConfigError("at least one extractor is required")
	}
	return out, nil
}

func parseLookup(raw string, authScheme string) (source string, name string, cutPrefix string, err error) {
	parts := strings.Split(raw, ":")
	if len(parts) < 2 {
		return "", "", "", fmt.Errorf("extractor lookup could not be split correctly: %s", raw)
	}
	source = strings.TrimSpace(parts[0])
	name = strings.TrimSpace(parts[1])
	if source == "" || name == "" {
		return "", "", "", invalidConfigError("invalid extractor lookup: " + raw)
	}
	if len(parts) > 2 {
		cutPrefix = strings.TrimSpace(parts[2])
	} else if strings.EqualFold(name, "Authorization") && authScheme != "" {
		cutPrefix = authScheme
	}
	if cutPrefix != "" && !strings.HasSuffix(cutPrefix, " ") {
		cutPrefix += " "
	}
	return source, name, cutPrefix, nil
}

func headerExtractor(name string, cutPrefix string) ValuesExtractor {
	prefixLen := len(cutPrefix)
	name = textproto.CanonicalMIMEHeaderKey(name)
	return func(r web.Context) ([]string, error) {
		req := r.Request()
		if req == nil {
			return nil, errHeaderExtractorValueMissing
		}
		values := req.Header.Values(name)
		if len(values) == 0 {
			return nil, errHeaderExtractorValueMissing
		}
		result := make([]string, 0, len(values))
		for i, value := range values {
			if prefixLen == 0 {
				result = append(result, value)
			} else if len(value) > prefixLen && strings.EqualFold(value[:prefixLen], cutPrefix) {
				result = append(result, value[prefixLen:])
			}
			if i >= extractorLimit-1 {
				break
			}
		}
		if len(result) == 0 {
			if prefixLen > 0 {
				return nil, errHeaderExtractorValueInvalid
			}
			return nil, errHeaderExtractorValueMissing
		}
		return result, nil
	}
}

func queryExtractor(name string) ValuesExtractor {
	return func(r web.Context) ([]string, error) {
		req := r.Request()
		if req == nil {
			return nil, errQueryExtractorValueMissing
		}
		values := req.URL.Query()[name]
		if len(values) == 0 {
			return nil, errQueryExtractorValueMissing
		}
		if len(values) > extractorLimit {
			values = values[:extractorLimit]
		}
		return append([]string(nil), values...), nil
	}
}

func paramExtractor(name string) ValuesExtractor {
	return func(r web.Context) ([]string, error) {
		value := strings.TrimSpace(r.Param(name))
		if value == "" {
			return nil, errParamExtractorValueMissing
		}
		return []string{value}, nil
	}
}

func cookieExtractor(name string) ValuesExtractor {
	return func(r web.Context) ([]string, error) {
		cookie, err := r.Cookie(name)
		if err != nil || cookie == nil || cookie.Value == "" {
			return nil, errCookieExtractorValueMissing
		}
		return []string{cookie.Value}, nil
	}
}

func formExtractor(name string) ValuesExtractor {
	return func(r web.Context) ([]string, error) {
		req := r.Request()
		if req == nil {
			return nil, errFormExtractorValueMissing
		}
		if err := req.ParseForm(); err != nil {
			return nil, err
		}
		values := req.PostForm[name]
		if len(values) == 0 {
			return nil, errFormExtractorValueMissing
		}
		if len(values) > extractorLimit {
			values = values[:extractorLimit]
		}
		result := make([]string, 0, len(values))
		for _, value := range values {
			value = strings.TrimSpace(value)
			if value != "" {
				result = append(result, value)
			}
		}
		if len(result) == 0 {
			return nil, errFormExtractorValueMissing
		}
		return result, nil
	}
}
