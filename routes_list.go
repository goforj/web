package web

import (
	"fmt"
	"hash/fnv"
	"regexp"
	"sort"
	"strings"
	"unicode"
)

const maxMiddlewareColumnWidth = 50

const (
	ansiReset     = "\x1b[0m"
	ansiBold      = "\x1b[1m"
	ansiPath      = "\x1b[38;5;113m"
	ansiGet       = "\x1b[32m"
	ansiPost      = "\x1b[33m"
	ansiDelete    = "\x1b[31m"
	ansiPatch     = "\x1b[35m"
	ansiPut       = "\x1b[34m"
	ansiHandler   = "\x1b[97m"
	ansiMiddleware = "\x1b[38;5;13m"
	ansiCell      = "\x1b[37m"
	ansiBorder    = "\x1b[38;5;240m"
)

var ansiPattern = regexp.MustCompile("\x1b\\[[0-9;]*m")

type middlewareRenderConfig struct {
	useShortcodes bool
	nameToCode    map[string]string
}

// RouteEntry represents a single route entry in the list and JSON responses.
type RouteEntry struct {
	Path        string   `json:"path"`
	Handler     string   `json:"handler"`
	Methods     []string `json:"methods"`
	Middlewares []string `json:"middlewares"`
}

// BuildRouteEntries builds a sorted slice of route entries from registered groups and extra entries.
func BuildRouteEntries(groups []RouteGroup, extra ...RouteEntry) []RouteEntry {
	grouped := map[string]*RouteEntry{}

	for _, group := range groups {
		prefix := group.RoutePrefix()
		groupMW := group.MiddlewareNames()

		for _, route := range group.Routes() {
			fullPath := prefix + route.Path()
			handlerName := route.HandlerName()
			allMW := append(append([]string(nil), groupMW...), route.MiddlewareNames()...)
			key := fullPath + ":" + handlerName

			if _, ok := grouped[key]; !ok {
				grouped[key] = &RouteEntry{
					Path:        fullPath,
					Handler:     handlerName,
					Methods:     []string{route.Method()},
					Middlewares: allMW,
				}
				continue
			}
			grouped[key].Methods = append(grouped[key].Methods, route.Method())
		}
	}

	for _, entry := range extra {
		key := entry.Path + ":" + entry.Handler
		if _, ok := grouped[key]; !ok {
			copied := entry
			grouped[key] = &copied
			continue
		}
		grouped[key].Methods = append(grouped[key].Methods, entry.Methods...)
	}

	return sortRouteEntries(grouped)
}

// RenderRouteTable renders a route table using simple ASCII borders and ANSI colors.
func RenderRouteTable(entries []RouteEntry) string {
	ptrs := make([]*RouteEntry, 0, len(entries))
	for i := range entries {
		ptrs = append(ptrs, &entries[i])
	}

	useShortcodes := shouldUseMiddlewareShortcodes(ptrs)
	cfg := middlewareRenderConfig{}
	var legend map[string]string
	if useShortcodes {
		legend, cfg.nameToCode = buildMiddlewareShortcodes(ptrs)
		cfg.useShortcodes = true
	}

	rawRows := buildRawRows(ptrs, cfg)
	headers := []string{"Path", "Methods", "Handler", "Middleware"}
	widths := columnWidths(headers, rawRows)

	var b strings.Builder
	border := renderBorder(widths)
	title := fmt.Sprintf(" API Routes \u203a (%d)", len(entries))

	b.WriteString(border)
	b.WriteByte('\n')
	if useShortcodes && len(legend) > 0 {
		b.WriteString(colorize(ansiBorder, "|"))
		b.WriteString(colorize(ansiBold+ansiHandler, " Middleware Legend"))
		b.WriteByte('\n')
		maxCodeWidth := 0
		for code := range legend {
			if len(code) > maxCodeWidth {
				maxCodeWidth = len(code)
			}
		}
		for _, code := range sortedKeys(legend) {
			padded := fmt.Sprintf("%-*s", maxCodeWidth, code)
			b.WriteString(colorize(ansiBorder, "|"))
			b.WriteString(" ")
			b.WriteString(colorize(ansiMiddleware, padded))
			b.WriteString(" · ")
			b.WriteString(legend[code])
			b.WriteByte('\n')
		}
		b.WriteString(border)
		b.WriteByte('\n')
	}
	b.WriteString(colorize(ansiBorder, "|"))
	b.WriteString(colorize(ansiBold+ansiHandler, title))
	b.WriteByte('\n')
	b.WriteString(border)
	b.WriteByte('\n')
	b.WriteString(renderTableRow(headers, widths, nil))
	b.WriteByte('\n')
	b.WriteString(border)
	for _, row := range rawRows {
		b.WriteByte('\n')
		b.WriteString(renderTableRow(row, widths, colorizedRow(row)))
	}
	b.WriteByte('\n')
	b.WriteString(border)
	return b.String()
}

func buildRawRows(entries []*RouteEntry, cfg middlewareRenderConfig) [][]string {
	rows := make([][]string, 0, len(entries))
	for _, entry := range entries {
		rows = append(rows, []string{
			entry.Path,
			normalizeMethods(entry.Methods),
			entry.Handler,
			renderMiddlewareCell(entry.Middlewares, cfg),
		})
	}
	return rows
}

func colorizedRow(raw []string) []string {
	return []string{
		colorize(ansiBold+ansiPath, raw[0]),
		colorizeMethods(raw[1]),
		colorize(ansiHandler, raw[2]),
		colorizeMiddleware(raw[3]),
	}
}

func renderTableRow(raw []string, widths []int, colored []string) string {
	cells := make([]string, 0, len(raw))
	for i := range raw {
		value := raw[i]
		if colored != nil {
			value = colored[i]
		}
		cells = append(cells, " "+padRight(value, widths[i])+" ")
	}
	return colorize(ansiBorder, "|") + strings.Join(cells, colorize(ansiBorder, "|")) + colorize(ansiBorder, "|")
}

func renderBorder(widths []int) string {
	parts := make([]string, 0, len(widths))
	for _, width := range widths {
		parts = append(parts, strings.Repeat("-", width+2))
	}
	return colorize(ansiBorder, "+"+strings.Join(parts, "+")+"+")
}

func columnWidths(headers []string, rows [][]string) []int {
	widths := make([]int, len(headers))
	for i, h := range headers {
		widths[i] = len(h)
	}
	for _, row := range rows {
		for i, cell := range row {
			if len(cell) > widths[i] {
				widths[i] = len(cell)
			}
		}
	}
	return widths
}

func padRight(value string, width int) string {
	visible := len(stripANSI(value))
	if visible >= width {
		return value
	}
	return value + strings.Repeat(" ", width-visible)
}

func stripANSI(s string) string {
	return ansiPattern.ReplaceAllString(s, "")
}

func colorize(prefix, value string) string {
	if value == "" {
		return value
	}
	return prefix + value + ansiReset
}

func sortRouteEntries(grouped map[string]*RouteEntry) []RouteEntry {
	sorted := make([]RouteEntry, 0, len(grouped))
	for _, entry := range grouped {
		entry.Methods = unique(entry.Methods)
		sort.Strings(entry.Methods)
		sorted = append(sorted, *entry)
	}
	sort.Slice(sorted, func(i, j int) bool {
		if sorted[i].Path == sorted[j].Path {
			leftMethods := normalizeMethods(sorted[i].Methods)
			rightMethods := normalizeMethods(sorted[j].Methods)
			if leftMethods == rightMethods {
				return sorted[i].Handler < sorted[j].Handler
			}
			return leftMethods < rightMethods
		}
		return sorted[i].Path < sorted[j].Path
	})
	return sorted
}

var allHTTPMethods = []string{
	"GET", "POST", "PUT", "DELETE", "PATCH", "OPTIONS", "HEAD",
	"CONNECT", "TRACE", "PROPFIND", "REPORT",
}

func unique(values []string) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(values))
	for _, value := range values {
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}

func normalizeMethods(methods []string) string {
	uniq := unique(methods)
	sort.Strings(uniq)

	all := append([]string(nil), allHTTPMethods...)
	sort.Strings(all)
	if len(uniq) == len(all) {
		match := true
		for i := range uniq {
			if uniq[i] != all[i] {
				match = false
				break
			}
		}
		if match {
			return "ALL"
		}
	}
	return strings.Join(uniq, ", ")
}

func colorizeMethods(methods string) string {
	if methods == "ALL" {
		return colorize(ansiCell, methods)
	}
	parts := strings.Split(methods, ",")
	colored := make([]string, 0, len(parts))
	for _, part := range parts {
		method := strings.TrimSpace(part)
		colored = append(colored, colorizeMethod(method))
	}
	return strings.Join(colored, ", ")
}

func colorizeMethod(method string) string {
	switch method {
	case "GET":
		return colorize(ansiGet, method)
	case "GETWS":
		return colorize(ansiGet, method)
	case "POST":
		return colorize(ansiPost, method)
	case "DELETE":
		return colorize(ansiDelete, method)
	case "PATCH":
		return colorize(ansiPatch, method)
	case "PUT":
		return colorize(ansiPut, method)
	default:
		return colorize(ansiCell, method)
	}
}

func colorizeMiddleware(middleware string) string {
	if middleware == "" {
		return ""
	}
	return colorize(ansiMiddleware, middleware)
}

func renderMiddlewareCell(middlewares []string, cfg middlewareRenderConfig) string {
	if cfg.useShortcodes {
		codes := make([]string, 0, len(middlewares))
		for _, middleware := range middlewares {
			if code, ok := cfg.nameToCode[middleware]; ok {
				codes = append(codes, code)
				continue
			}
			codes = append(codes, middleware)
		}
		return strings.Join(codes, ", ")
	}
	return strings.Join(middlewares, ", ")
}

func shouldUseMiddlewareShortcodes(entries []*RouteEntry) bool {
	for _, entry := range entries {
		if len(strings.Join(entry.Middlewares, ", ")) > maxMiddlewareColumnWidth {
			return true
		}
	}
	return false
}

func buildMiddlewareShortcodes(entries []*RouteEntry) (map[string]string, map[string]string) {
	codeToName := map[string]string{}
	nameToCode := map[string]string{}
	seen := map[string]struct{}{}

	for _, entry := range entries {
		for _, middleware := range entry.Middlewares {
			if _, ok := seen[middleware]; ok {
				continue
			}
			seen[middleware] = struct{}{}
			base := friendlyMiddlewareCode(middleware)
			offset := uint32(0)
			for {
				code := base
				if offset > 0 {
					code = fmt.Sprintf("%s-%02X", base, byte(fnvSuffix(middleware, offset)))
				}
				if existing, ok := codeToName[code]; !ok || existing == middleware {
					codeToName[code] = middleware
					nameToCode[middleware] = code
					break
				}
				offset++
			}
		}
	}

	return codeToName, nameToCode
}

func friendlyMiddlewareCode(name string) string {
	pkgPart, fnPart := splitMiddlewareName(name)
	pkgCode := uppercaseHint(pkgPart)
	fnCode := uppercaseHint(fnPart)

	if pkgCode == "" && fnCode == "" {
		return "MW"
	}
	if pkgCode == "" {
		return fnCode
	}
	if fnCode == "" {
		return pkgCode
	}
	return pkgCode + "." + fnCode
}

func uppercaseHint(part string) string {
	if part == "" {
		return ""
	}
	var caps []rune
	for _, r := range part {
		if unicode.IsUpper(r) {
			caps = append(caps, r)
		}
	}
	if len(caps) > 0 {
		if len(caps) > 4 {
			caps = caps[:4]
		}
		return string(caps)
	}
	runes := []rune(part)
	return strings.ToUpper(string(runes[0]))
}

func fnvSuffix(name string, offset uint32) byte {
	h := fnv.New32a()
	_, _ = h.Write([]byte(name))
	return byte(h.Sum32() + offset)
}

func splitMiddlewareName(name string) (string, string) {
	parts := strings.Split(name, ".")
	if len(parts) >= 2 {
		return parts[len(parts)-2], parts[len(parts)-1]
	}
	if len(parts) == 1 {
		return parts[0], ""
	}
	return "", ""
}

func sortedKeys(values map[string]string) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
