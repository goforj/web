package webindex

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"go/ast"
	"go/constant"
	"go/token"
	"go/types"
	"math"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"unicode"

	"golang.org/x/tools/go/packages"
)

// typedJSONField is a candidate property after applying encoding/json embedding rules.
type typedJSONField struct {
	Name   string
	Type   types.Type
	Tag    reflect.StructTag
	Depth  int
	Tagged bool
}

// indexReachableComponents reserves final names from selected wire contracts before recursive schema construction creates references.
func (r *typedSchemaRegistry) indexReachableComponents(loaded []*packages.Package, selected typedSourceSelection) {
	candidates := map[string]*types.Named{}
	visited := map[string]struct{}{}
	var visit func(types.Type, typedSourceRange)
	visit = func(typeOf types.Type, source typedSourceRange) {
		if typeOf == nil {
			return
		}
		switch typed := typeOf.(type) {
		case *types.Alias:
			visit(types.Unalias(typed), source)
		case *types.Named:
			if isWellKnownNamedType(typed) {
				return
			}
			if codec, _ := customCodecContract(typed); codec != "" {
				return
			}
			if mapped, ok := typed.Underlying().(*types.Map); ok && !isJSONMapKeyType(mapped.Key()) {
				return
			}
			identity := canonicalNamedIdentity(typed)
			if _, exists := visited[identity]; exists {
				return
			}
			visited[identity] = struct{}{}
			candidates[identity] = typed
			visit(typed.Underlying(), source)
		case *types.Pointer:
			visit(typed.Elem(), source)
		case *types.Slice:
			if !isJSONByteElement(typed.Elem()) {
				visit(typed.Elem(), source)
			}
		case *types.Array:
			visit(typed.Elem(), source)
		case *types.Map:
			if isJSONMapKeyType(typed.Key()) {
				visit(typed.Elem(), source)
			}
		case *types.Struct:
			fields := make([]typedJSONField, 0, typed.NumFields())
			r.collectJSONFields(typed, 0, map[types.Type]bool{}, &fields)
			for _, field := range r.selectJSONFields(fields, source) {
				if jsonTagHasOption(field.Tag, "string") && jsonStringOptionApplies(field.Type) && !hasCustomCodec(field.Type) {
					continue
				}
				visit(field.Type, source)
			}
		}
	}
	for _, expression := range r.reachableContractExpressions(loaded, selected) {
		visit(expression.Type, expression.Source)
	}
	r.assignComponentNames(candidates)
}

// reachableContractExpressions uses exact selected call sites when available and retains package-level discovery for focused registry callers.
func (r *typedSchemaRegistry) reachableContractExpressions(loaded []*packages.Package, selected typedSourceSelection) []typedExpression {
	if selected != nil {
		bySource := map[string]typedExpression{}
		for key, expression := range r.expressions {
			if selected.contains(expression.Source) {
				bySource[key] = expression
			}
		}
		keys := make([]string, 0, len(bySource))
		for key := range bySource {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		out := make([]typedExpression, 0, len(keys))
		for _, key := range keys {
			out = append(out, bySource[key])
		}
		return out
	}
	bySource := map[string]typedExpression{}
	for _, pkg := range loaded {
		for _, expression := range packageContractExpressions(pkg) {
			bySource[typedSourceKey(expression.Source)] = expression
			for key, nested := range r.expressions {
				if nested.Source.File == expression.Source.File && nested.Source.StartOffset >= expression.Source.StartOffset && nested.Source.EndOffset <= expression.Source.EndOffset {
					bySource[key] = nested
				}
			}
		}
	}
	keys := make([]string, 0, len(bySource))
	for key := range bySource {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	out := make([]typedExpression, 0, len(keys))
	for _, key := range keys {
		out = append(out, bySource[key])
	}
	return out
}

// packageContractExpressions selects Bind targets and JSON payloads for direct registry users that did not provide route-reachable source ranges.
func packageContractExpressions(pkg *packages.Package) []typedExpression {
	if pkg == nil || pkg.TypesInfo == nil {
		return nil
	}
	contractExpressions := make([]typedExpression, 0)
	for _, file := range pkg.Syntax {
		ast.Inspect(file, func(node ast.Node) bool {
			call, ok := node.(*ast.CallExpr)
			if !ok {
				return true
			}
			selector, ok := call.Fun.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			argumentIndex := -1
			switch selector.Sel.Name {
			case "Bind":
				if len(call.Args) == 1 {
					argumentIndex = 0
				}
			case "JSON":
				if len(call.Args) >= 2 {
					argumentIndex = 1
				}
			}
			if argumentIndex < 0 {
				return true
			}
			argument := call.Args[argumentIndex]
			if selector.Sel.Name == "Bind" {
				if unary, ok := argument.(*ast.UnaryExpr); ok && unary.Op == token.AND {
					argument = unary.X
				}
			}
			typeOf := pkg.TypesInfo.TypeOf(argument)
			source, ok := typedRangeForNode(pkg.Fset, argument)
			if typeOf != nil && ok {
				contractExpressions = append(contractExpressions, typedExpression{Type: typeOf, Value: pkg.TypesInfo.Types[argument].Value, Source: source})
			}
			return true
		})
	}
	return contractExpressions
}

// schemaForType maps checked Go types conservatively so unsupported values remain unconstrained instead of becoming fake strings.
func (r *typedSchemaRegistry) schemaForType(typeOf types.Type, source typedSourceRange) map[string]any {
	if typeOf == nil {
		r.addDiagnostic("unresolved_schema_type", "Go type could not be resolved", source)
		return map[string]any{}
	}
	switch typed := typeOf.(type) {
	case *types.Alias:
		return r.schemaForType(types.Unalias(typed), source)
	case *types.Basic:
		return r.schemaForBasic(typed, source)
	case *types.Pointer:
		return nullableSchema(r.schemaForType(typed.Elem(), source))
	case *types.Slice:
		if isJSONByteElement(typed.Elem()) {
			return map[string]any{"type": "string", "format": "byte", "nullable": true}
		}
		return nullableSchema(map[string]any{
			"type":  "array",
			"items": r.schemaForType(typed.Elem(), source),
		})
	case *types.Array:
		return map[string]any{
			"type":     "array",
			"items":    r.schemaForType(typed.Elem(), source),
			"minItems": typed.Len(),
			"maxItems": typed.Len(),
		}
	case *types.Map:
		if !isJSONMapKeyType(typed.Key()) {
			r.addDiagnostic(
				"unsupported_map_key_type",
				fmt.Sprintf("map key type %s is not safely representable as a JSON object key", readableGoType(typed.Key())),
				source,
			)
			return map[string]any{}
		}
		return nullableSchema(map[string]any{
			"type":                 "object",
			"additionalProperties": r.schemaForType(typed.Elem(), source),
		})
	case *types.Struct:
		return r.schemaForStruct(typed, source)
	case *types.Named:
		if schema, ok := wellKnownNamedSchema(typed); ok {
			return schema
		}
		if codec, diagnosticCode := customCodecContract(typed); codec != "" {
			r.addDiagnostic(
				diagnosticCode,
				fmt.Sprintf("Go type %s implements %s; its runtime JSON contract requires an explicit OpenAPI schema", readableGoType(typed), codec),
				source,
			)
			return map[string]any{}
		}
		if mapped, ok := typed.Underlying().(*types.Map); ok && !isJSONMapKeyType(mapped.Key()) {
			return r.schemaForType(mapped, source)
		}
		component := r.ensureNamedComponent(typed, source)
		return map[string]any{"$ref": "#/components/schemas/" + component.Name}
	case *types.Interface:
		if typed.Empty() {
			return map[string]any{}
		}
		r.addDiagnostic(
			"unsupported_schema_type",
			fmt.Sprintf("interface type %s cannot be projected without an explicit contract", readableGoType(typed)),
			source,
		)
		return map[string]any{}
	case *types.Tuple:
		if typed.Len() == 1 {
			return r.schemaForType(typed.At(0).Type(), source)
		}
		r.addDiagnostic(
			"unsupported_schema_type",
			fmt.Sprintf("multi-value tuple %s cannot be represented as one response schema", readableGoType(typed)),
			source,
		)
		return map[string]any{}
	case *types.TypeParam:
		r.addDiagnostic(
			"uninstantiated_schema_type_parameter",
			fmt.Sprintf("type parameter %s requires a concrete instantiation before schema generation", typed.Obj().Name()),
			source,
		)
		return map[string]any{}
	case *types.Chan, *types.Signature, *types.Union:
		r.addDiagnostic(
			"unsupported_schema_type",
			fmt.Sprintf("Go type %s has no safe JSON schema projection", readableGoType(typeOf)),
			source,
		)
		return map[string]any{}
	default:
		r.addDiagnostic(
			"unsupported_schema_type",
			fmt.Sprintf("Go type %s has no supported schema projection", readableGoType(typeOf)),
			source,
		)
		return map[string]any{}
	}
}

// schemaForJSONExpression refines literal containers from checked child expressions while leaving all non-literals to their static wire type.
func (r *typedSchemaRegistry) schemaForJSONExpression(fset *token.FileSet, expression ast.Expr, checked typedExpression) map[string]any {
	switch typed := expression.(type) {
	case *ast.ParenExpr:
		return r.schemaForNestedJSONExpression(fset, typed.X)
	case *ast.UnaryExpr:
		if typed.Op == token.AND {
			return r.schemaForNestedJSONExpression(fset, typed.X)
		}
	case *ast.CompositeLit:
		unaliased := types.Unalias(checked.Type)
		if named, ok := unaliased.(*types.Named); ok {
			if isWellKnownNamedType(named) {
				return r.schemaForType(named, checked.Source)
			}
			if codec, _ := customCodecContract(named); codec != "" {
				return r.schemaForType(named, checked.Source)
			}
			if _, structure := named.Underlying().(*types.Struct); structure {
				return r.schemaForType(named, checked.Source)
			}
			unaliased = named.Underlying()
		}
		switch container := unaliased.(type) {
		case *types.Map:
			return r.schemaForJSONMapLiteral(fset, typed, container, checked.Source)
		case *types.Slice:
			if isJSONByteElement(container.Elem()) {
				return map[string]any{"type": "string", "format": "byte"}
			}
			return r.schemaForJSONSequenceLiteral(fset, typed, container.Elem(), -1, checked.Source)
		case *types.Array:
			return r.schemaForJSONSequenceLiteral(fset, typed, container.Elem(), container.Len(), checked.Source)
		case *types.Struct:
			return r.schemaForStruct(container, checked.Source)
		}
	}
	return r.schemaForType(checked.Type, checked.Source)
}

// schemaForNestedJSONExpression uses exact checked child types and becomes unconstrained when partial type information cannot justify a shape.
func (r *typedSchemaRegistry) schemaForNestedJSONExpression(fset *token.FileSet, expression ast.Expr) map[string]any {
	checked, _, ok := r.lookupExpression(fset, expression)
	if !ok {
		return map[string]any{}
	}
	return r.schemaForJSONExpression(fset, expression, checked)
}

// schemaForJSONMapLiteral records exact constant keys while checked value expressions preserve heterogeneous runtime shapes.
func (r *typedSchemaRegistry) schemaForJSONMapLiteral(fset *token.FileSet, literal *ast.CompositeLit, mapType *types.Map, source typedSourceRange) map[string]any {
	if !isJSONMapKeyType(mapType.Key()) {
		return r.schemaForType(mapType, source)
	}
	properties := map[string]any{}
	required := make([]string, 0, len(literal.Elts))
	for _, element := range literal.Elts {
		keyValue, ok := element.(*ast.KeyValueExpr)
		if !ok {
			return nonNullableSchema(r.schemaForType(mapType, source))
		}
		name, ok := r.jsonMapLiteralKey(fset, keyValue.Key)
		if !ok {
			return nonNullableSchema(r.schemaForType(mapType, source))
		}
		if _, exists := properties[name]; !exists {
			required = append(required, name)
		}
		properties[name] = r.schemaForNestedJSONExpression(fset, keyValue.Value)
	}
	sort.Strings(required)
	out := map[string]any{
		"type":                 "object",
		"properties":           properties,
		"additionalProperties": false,
	}
	if len(required) > 0 {
		out["required"] = required
	}
	return out
}

// jsonMapLiteralKey evaluates only constant string and integer keys that encoding/json maps deterministically to member names.
func (r *typedSchemaRegistry) jsonMapLiteralKey(fset *token.FileSet, expression ast.Expr) (string, bool) {
	checked, _, ok := r.lookupExpression(fset, expression)
	if !ok || checked.Value == nil {
		return "", false
	}
	switch checked.Value.Kind() {
	case constant.String:
		return constant.StringVal(checked.Value), true
	case constant.Int:
		return checked.Value.ExactString(), true
	default:
		return "", false
	}
}

// schemaForJSONSequenceLiteral models each explicit element and uses a union when a literal contains heterogeneous JSON values.
func (r *typedSchemaRegistry) schemaForJSONSequenceLiteral(fset *token.FileSet, literal *ast.CompositeLit, elementType types.Type, length int64, source typedSourceRange) map[string]any {
	elementSchemas := make([]map[string]any, 0, len(literal.Elts)+1)
	if !isEmptyInterfaceType(elementType) {
		elementSchemas = append(elementSchemas, r.schemaForType(elementType, source))
	} else {
		for _, element := range literal.Elts {
			if keyed, ok := element.(*ast.KeyValueExpr); ok {
				element = keyed.Value
			}
			elementSchemas = append(elementSchemas, r.schemaForNestedJSONExpression(fset, element))
		}
		if len(elementSchemas) == 0 || length >= 0 && !r.jsonArrayLiteralCoversAllPositions(fset, literal, length) {
			elementSchemas = append(elementSchemas, r.schemaForType(elementType, source))
		}
	}
	out := map[string]any{
		"type":  "array",
		"items": combinedJSONItemSchema(elementSchemas),
	}
	if length >= 0 {
		out["minItems"] = length
		out["maxItems"] = length
	}
	return out
}

// isEmptyInterfaceType identifies containers whose runtime elements may legitimately have unrelated checked schemas.
func isEmptyInterfaceType(typeOf types.Type) bool {
	interfaceType, ok := types.Unalias(typeOf).Underlying().(*types.Interface)
	return ok && interfaceType.Empty()
}

// jsonArrayLiteralCoversAllPositions detects when fixed-array zero values add no unseen alternative beyond the explicit elements.
func (r *typedSchemaRegistry) jsonArrayLiteralCoversAllPositions(fset *token.FileSet, literal *ast.CompositeLit, length int64) bool {
	if length < 0 {
		return true
	}
	covered := map[int64]struct{}{}
	next := int64(0)
	for _, element := range literal.Elts {
		index := next
		if keyed, ok := element.(*ast.KeyValueExpr); ok {
			checked, _, found := r.lookupExpression(fset, keyed.Key)
			if !found || checked.Value == nil || checked.Value.Kind() != constant.Int {
				return false
			}
			var exact bool
			index, exact = constant.Int64Val(checked.Value)
			if !exact {
				return false
			}
		}
		if index < 0 || index >= length {
			return false
		}
		covered[index] = struct{}{}
		next = index + 1
	}
	return int64(len(covered)) == length
}

// combinedJSONItemSchema deduplicates equivalent alternatives and lets an unconstrained element dominate narrower guesses.
func combinedJSONItemSchema(schemas []map[string]any) map[string]any {
	unique := make([]map[string]any, 0, len(schemas))
	seen := map[string]struct{}{}
	for _, schema := range schemas {
		if len(schema) == 0 {
			return map[string]any{}
		}
		encoded, err := json.Marshal(schema)
		if err != nil {
			return map[string]any{}
		}
		key := string(encoded)
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		unique = append(unique, schema)
	}
	if len(unique) == 1 {
		return cloneSchemaMap(unique[0])
	}
	alternatives := make([]any, 0, len(unique))
	for _, schema := range unique {
		alternatives = append(alternatives, cloneSchemaMap(schema))
	}
	return map[string]any{"anyOf": alternatives}
}

// nonNullableSchema narrows a known non-nil literal without mutating the reusable general type schema.
func nonNullableSchema(schema map[string]any) map[string]any {
	out := cloneSchemaMap(schema)
	delete(out, "nullable")
	return out
}

// customCodecContract detects runtime encoding and decoding hooks on either value or pointer method sets because request and response contracts use both directions.
func customCodecContract(named *types.Named) (string, string) {
	if named == nil {
		return "", ""
	}
	for _, candidate := range []types.Type{named, types.NewPointer(named)} {
		if methodSetHasMarshaler(candidate, "MarshalJSON") {
			return "json.Marshaler", "custom_marshaler_schema"
		}
		if methodSetHasUnmarshaler(candidate, "UnmarshalJSON") {
			return "json.Unmarshaler", "custom_unmarshaler_schema"
		}
	}
	for _, candidate := range []types.Type{named, types.NewPointer(named)} {
		if methodSetHasMarshaler(candidate, "MarshalText") {
			return "encoding.TextMarshaler", "custom_marshaler_schema"
		}
		if methodSetHasUnmarshaler(candidate, "UnmarshalText") {
			return "encoding.TextUnmarshaler", "custom_unmarshaler_schema"
		}
	}
	return "", ""
}

// hasCustomCodec keeps field-level tag projection from hiding a named type's runtime serialization hooks.
func hasCustomCodec(typeOf types.Type) bool {
	named := namedType(typeOf)
	if named == nil {
		return false
	}
	codec, _ := customCodecContract(named)
	return codec != ""
}

// methodSetHasMarshaler applies the standard marshaler signature so unrelated same-named helpers do not erase an otherwise trustworthy schema.
func methodSetHasMarshaler(typeOf types.Type, methodName string) bool {
	methodSet := types.NewMethodSet(typeOf)
	for index := 0; index < methodSet.Len(); index++ {
		method, ok := methodSet.At(index).Obj().(*types.Func)
		if !ok || method.Name() != methodName {
			continue
		}
		signature, ok := method.Type().(*types.Signature)
		if !ok || signature.Params().Len() != 0 || signature.Results().Len() != 2 {
			continue
		}
		bytesResult, ok := types.Unalias(signature.Results().At(0).Type()).(*types.Slice)
		if !ok || !isByteType(bytesResult.Elem()) {
			continue
		}
		if types.Identical(signature.Results().At(1).Type(), types.Universe.Lookup("error").Type()) {
			return true
		}
	}
	return false
}

// methodSetHasUnmarshaler applies the standard decoder signature so request-only codecs cannot expose an underlying storage shape as their wire contract.
func methodSetHasUnmarshaler(typeOf types.Type, methodName string) bool {
	methodSet := types.NewMethodSet(typeOf)
	for index := 0; index < methodSet.Len(); index++ {
		method, ok := methodSet.At(index).Obj().(*types.Func)
		if !ok || method.Name() != methodName {
			continue
		}
		signature, ok := method.Type().(*types.Signature)
		if !ok || signature.Params().Len() != 1 || signature.Results().Len() != 1 {
			continue
		}
		bytesParameter, ok := types.Unalias(signature.Params().At(0).Type()).(*types.Slice)
		if !ok || !isByteType(bytesParameter.Elem()) {
			continue
		}
		if types.Identical(signature.Results().At(0).Type(), types.Universe.Lookup("error").Type()) {
			return true
		}
	}
	return false
}

// schemaForBasic projects only JSON-representable primitive kinds and diagnoses language-only values such as complex numbers.
func (r *typedSchemaRegistry) schemaForBasic(basic *types.Basic, source typedSourceRange) map[string]any {
	switch basic.Kind() {
	case types.Bool, types.UntypedBool:
		return map[string]any{"type": "boolean"}
	case types.String, types.UntypedString:
		return map[string]any{"type": "string"}
	case types.Int8, types.Int16, types.Int32:
		return map[string]any{"type": "integer", "format": "int32"}
	case types.Int64:
		return map[string]any{"type": "integer", "format": "int64"}
	case types.Uint8:
		return map[string]any{"type": "integer", "format": "int32", "minimum": 0}
	case types.Uint16, types.Uint32:
		return map[string]any{"type": "integer", "format": "int64", "minimum": 0}
	case types.Uint, types.Uint64, types.Uintptr:
		return map[string]any{"type": "integer", "minimum": 0}
	case types.Int, types.UntypedInt:
		return map[string]any{"type": "integer"}
	case types.Float32:
		return map[string]any{"type": "number", "format": "float"}
	case types.Float64, types.UntypedFloat:
		return map[string]any{"type": "number", "format": "double"}
	case types.UntypedNil:
		return map[string]any{"nullable": true}
	case types.Invalid:
		r.addDiagnostic("unresolved_schema_type", "invalid Go type has no trustworthy schema", source)
		return map[string]any{}
	default:
		r.addDiagnostic(
			"unsupported_schema_type",
			fmt.Sprintf("Go primitive %s has no JSON schema projection", basic.Name()),
			source,
		)
		return map[string]any{}
	}
}

// ensureNamedComponent stores one component per canonical Go identity so unrelated named contracts are never shape-deduplicated.
func (r *typedSchemaRegistry) ensureNamedComponent(named *types.Named, source typedSourceRange) *typedSchemaComponent {
	identity := canonicalNamedIdentity(named)
	if existing := r.componentsByID[identity]; existing != nil {
		return existing
	}
	object := named.Origin().Obj()
	packagePath := ""
	if object.Pkg() != nil {
		packagePath = object.Pkg().Path()
	}
	component := &typedSchemaComponent{
		Identity:   identity,
		Name:       r.componentName(named),
		Package:    packagePath,
		TypeName:   readableNamedType(named),
		Confidence: "high",
	}
	r.componentsByID[identity] = component
	component.Schema = r.schemaForType(named.Underlying(), source)
	if enum := enumValues(named); len(enum) > 0 {
		component.Schema = cloneSchemaMap(component.Schema)
		component.Schema["enum"] = enum
	}
	if len(component.Schema) == 0 {
		component.Confidence = "low"
	}
	return component
}

// schemaForStruct applies exported-field, JSON-tag, and embedded-field dominance rules close to encoding/json.
func (r *typedSchemaRegistry) schemaForStruct(structure *types.Struct, source typedSourceRange) map[string]any {
	candidates := make([]typedJSONField, 0, structure.NumFields())
	r.collectJSONFields(structure, 0, map[types.Type]bool{}, &candidates)
	selected := r.selectJSONFields(candidates, source)
	properties := make(map[string]any, len(selected))
	required := make([]string, 0)
	for _, field := range selected {
		customCodec := hasCustomCodec(field.Type)
		stringEncoded := jsonTagHasOption(field.Tag, "string") && jsonStringOptionApplies(field.Type) && !customCodec
		var schema map[string]any
		if stringEncoded {
			schema = jsonStringSchema(field.Type)
			if named := namedType(field.Type); named != nil {
				if enum := wireStringEncodedEnumValues(enumValues(named)); len(enum) > 0 {
					schema["enum"] = enum
				}
			}
		} else {
			schema = r.schemaForType(field.Type, source)
		}
		if !customCodec {
			schema = r.applyValidationSchemaHints(schema, field.Type, field.Name, field.Tag, source, stringEncoded)
		}
		properties[field.Name] = schema
		if validationTagRequiresField(field.Tag) {
			required = append(required, field.Name)
		}
	}
	out := map[string]any{
		"type":       "object",
		"properties": properties,
	}
	if len(required) > 0 {
		sort.Strings(required)
		out["required"] = required
	}
	return out
}

// collectJSONFields expands untagged anonymous structs while preventing recursive embedding from looping forever.
func (r *typedSchemaRegistry) collectJSONFields(structure *types.Struct, depth int, visiting map[types.Type]bool, target *[]typedJSONField) {
	if structure == nil || visiting[structure] {
		return
	}
	visiting[structure] = true
	defer delete(visiting, structure)
	for index := 0; index < structure.NumFields(); index++ {
		field := structure.Field(index)
		if field == nil {
			continue
		}
		if !field.Exported() && (!field.Anonymous() || embeddedStruct(field.Type()) == nil) {
			continue
		}
		tag := reflect.StructTag(structure.Tag(index))
		name, skip, explicitlyNamed := jsonFieldName(field.Name(), tag)
		if skip {
			continue
		}
		if field.Anonymous() && !explicitlyNamed {
			if embedded := embeddedStruct(field.Type()); embedded != nil {
				r.collectJSONFields(embedded, depth+1, visiting, target)
				continue
			}
		}
		*target = append(*target, typedJSONField{
			Name:   name,
			Type:   field.Type(),
			Tag:    tag,
			Depth:  depth,
			Tagged: explicitlyNamed,
		})
	}
}

// selectJSONFields resolves embedded-name conflicts by shallowest depth and then explicit JSON naming, omitting ambiguous peers.
func (r *typedSchemaRegistry) selectJSONFields(candidates []typedJSONField, source typedSourceRange) []typedJSONField {
	byName := map[string][]typedJSONField{}
	for _, candidate := range candidates {
		byName[candidate.Name] = append(byName[candidate.Name], candidate)
	}
	names := make([]string, 0, len(byName))
	for name := range byName {
		names = append(names, name)
	}
	sort.Strings(names)
	selected := make([]typedJSONField, 0, len(names))
	for _, name := range names {
		fields := byName[name]
		minimumDepth := fields[0].Depth
		for _, field := range fields[1:] {
			if field.Depth < minimumDepth {
				minimumDepth = field.Depth
			}
		}
		atDepth := make([]typedJSONField, 0, len(fields))
		for _, field := range fields {
			if field.Depth == minimumDepth {
				atDepth = append(atDepth, field)
			}
		}
		if len(atDepth) == 1 {
			selected = append(selected, atDepth[0])
			continue
		}
		tagged := make([]typedJSONField, 0, len(atDepth))
		for _, field := range atDepth {
			if field.Tagged {
				tagged = append(tagged, field)
			}
		}
		if len(tagged) == 1 {
			selected = append(selected, tagged[0])
			continue
		}
		r.addDiagnostic(
			"ambiguous_json_field",
			fmt.Sprintf("JSON field %q is ambiguous at embedded depth %d and was omitted", name, minimumDepth),
			source,
		)
	}
	return selected
}

// jsonFieldName applies the naming portion of encoding/json tags without making omitempty imply requiredness.
func jsonFieldName(defaultName string, tag reflect.StructTag) (name string, skip bool, explicitlyNamed bool) {
	raw, ok := tag.Lookup("json")
	if !ok {
		return defaultName, false, false
	}
	parts := strings.Split(raw, ",")
	if parts[0] == "-" {
		return "", true, false
	}
	if parts[0] == "" {
		return defaultName, false, false
	}
	return parts[0], false, true
}

// jsonTagHasOption reports an exact encoding/json option without interpreting unrelated tag sections.
func jsonTagHasOption(tag reflect.StructTag, option string) bool {
	raw, ok := tag.Lookup("json")
	if !ok {
		return false
	}
	parts := strings.Split(raw, ",")
	for _, candidate := range parts[1:] {
		if candidate == option {
			return true
		}
	}
	return false
}

// jsonStringOptionApplies mirrors encoding/json's quoted scalar domain so unsupported uses retain their ordinary schemas.
func jsonStringOptionApplies(typeOf types.Type) bool {
	typeOf = types.Unalias(typeOf)
	if pointer, ok := typeOf.(*types.Pointer); ok {
		typeOf = types.Unalias(pointer.Elem())
	}
	if named, ok := typeOf.(*types.Named); ok {
		typeOf = named.Underlying()
	}
	basic, ok := typeOf.(*types.Basic)
	return ok && basic.Info()&(types.IsString|types.IsBoolean|types.IsInteger|types.IsFloat) != 0
}

// jsonStringSchema projects the wire representation rather than the scalar's in-memory Go representation.
func jsonStringSchema(typeOf types.Type) map[string]any {
	schema := map[string]any{"type": "string"}
	typeOf = types.Unalias(typeOf)
	if _, pointer := typeOf.(*types.Pointer); pointer {
		schema["nullable"] = true
	}
	return schema
}

// wireStringEncodedEnumValues converts Go scalar values into the JSON text stored inside encoding/json's quoted field representation.
func wireStringEncodedEnumValues(values []any) []any {
	out := make([]any, 0, len(values))
	for _, value := range values {
		encoded, err := json.Marshal(value)
		if err != nil {
			continue
		}
		out = append(out, string(encoded))
	}
	return deduplicateSchemaValues(out)
}

// validationTagRequiresField treats only top-level required rules before an omitempty short circuit as field-presence policy.
func validationTagRequiresField(tag reflect.StructTag) bool {
	for _, key := range []string{"validate", "binding"} {
		raw, ok := tag.Lookup(key)
		if !ok {
			continue
		}
		for _, rule := range validationRulesBeforeDive(raw) {
			switch validationRuleName(rule) {
			case "omitempty", "omitnil":
				break
			case "required":
				return true
			}
			if validationRuleName(rule) == "omitempty" || validationRuleName(rule) == "omitnil" {
				break
			}
		}
	}
	return false
}

// applyValidationSchemaHints preserves type-safe declarative formats and enums while diagnosing hints that cannot describe the projected wire value.
func (r *typedSchemaRegistry) applyValidationSchemaHints(schema map[string]any, typeOf types.Type, fieldName string, tag reflect.StructTag, source typedSourceRange, stringEncoded bool) map[string]any {
	if schema == nil {
		schema = map[string]any{}
	}
	storagePrimitive := schemaPrimitiveKind(typeOf)
	wirePrimitive := storagePrimitive
	if declared := schemaDeclaredPrimitive(schema); declared != "" {
		wirePrimitive = declared
	}
	if stringEncoded {
		wirePrimitive = "string"
	}
	formats := map[string]struct{}{}
	enumSets := make([][]any, 0)
	for _, key := range []string{"validate", "binding"} {
		raw, ok := tag.Lookup(key)
		if !ok {
			continue
		}
		for _, rule := range validationRulesBeforeDive(raw) {
			switch validationRuleName(rule) {
			case "uuid", "uuid3", "uuid4", "uuid5":
				if storagePrimitive == "string" && wirePrimitive == "string" {
					formats["uuid"] = struct{}{}
				} else {
					r.addDiagnostic("incompatible_validation_hint", fmt.Sprintf("JSON field %q uses validation format %q but its projected type is %s", fieldName, "uuid", validationPrimitiveLabel(wirePrimitive)), source)
				}
			case "email":
				if storagePrimitive == "string" && wirePrimitive == "string" {
					formats["email"] = struct{}{}
				} else {
					r.addDiagnostic("incompatible_validation_hint", fmt.Sprintf("JSON field %q uses validation format %q but its projected type is %s", fieldName, "email", validationPrimitiveLabel(wirePrimitive)), source)
				}
			default:
				if values, ok := strings.CutPrefix(rule, "oneof="); ok {
					parsed, parsedOK := parseValidationOneOfValues(values)
					coercionPrimitive := wirePrimitive
					if stringEncoded {
						coercionPrimitive = storagePrimitive
					}
					coerced, coercionOK := coerceValidationEnum(parsed, coercionPrimitive, typeOf)
					if storagePrimitive == "" {
						coercionOK = false
					}
					if stringEncoded && coercionOK {
						coerced = wireStringEncodedEnumValues(coerced)
					}
					if parsedOK && coercionOK {
						enumSets = append(enumSets, deduplicateSchemaValues(coerced))
					} else {
						r.addDiagnostic("incompatible_validation_hint", fmt.Sprintf("JSON field %q has oneof values that cannot be represented as %s", fieldName, validationPrimitiveLabel(wirePrimitive)), source)
					}
				}
			}
		}
	}
	enum := intersectSchemaValueSets(enumSets)
	if len(enumSets) > 0 && len(enum) == 0 {
		r.addDiagnostic("incompatible_validation_hint", fmt.Sprintf("JSON field %q has oneof rules with no common value", fieldName), source)
	}
	if len(formats) == 0 && len(enum) == 0 {
		return schema
	}
	out := schemaWithSiblings(schema)
	if len(formats) == 1 {
		for format := range formats {
			out["format"] = format
		}
	} else if len(formats) > 1 {
		r.addDiagnostic("incompatible_validation_hint", fmt.Sprintf("JSON field %q has conflicting validation formats", fieldName), source)
	}
	if len(enum) > 0 {
		out["enum"] = enum
	}
	return out
}

// validationRulesBeforeDive keeps container-element validators from being projected onto the containing field.
func validationRulesBeforeDive(raw string) []string {
	rules := make([]string, 0)
	for _, rule := range strings.Split(raw, ",") {
		rule = strings.TrimSpace(rule)
		if rule == "" {
			continue
		}
		if validationRuleName(rule) == "dive" {
			break
		}
		rules = append(rules, rule)
	}
	return rules
}

// validationRuleName separates a validator name from its parameter without interpreting the parameter's quoting.
func validationRuleName(rule string) string {
	name, _, _ := strings.Cut(strings.TrimSpace(rule), "=")
	return name
}

// parseValidationOneOfValues follows validator's single-quoted token convention while rejecting unmatched quotes.
func parseValidationOneOfValues(raw string) ([]string, bool) {
	values := make([]string, 0)
	var builder strings.Builder
	inQuote := false
	hasToken := false
	flush := func() {
		if !hasToken {
			return
		}
		value := strings.ReplaceAll(strings.ReplaceAll(builder.String(), "0x2C", ","), "0x7C", "|")
		values = append(values, value)
		builder.Reset()
		hasToken = false
	}
	for _, current := range raw {
		switch {
		case current == '\'':
			inQuote = !inQuote
			hasToken = true
		case !inQuote && unicode.IsSpace(current):
			flush()
		default:
			builder.WriteRune(current)
			hasToken = true
		}
	}
	if inQuote {
		return nil, false
	}
	flush()
	return values, len(values) > 0
}

// deduplicateSchemaValues preserves validator declaration order while removing duplicates across equivalent JSON values.
func deduplicateSchemaValues(values []any) []any {
	out := make([]any, 0, len(values))
	seen := map[string]struct{}{}
	for _, value := range values {
		encoded, err := json.Marshal(value)
		if err != nil {
			continue
		}
		key := string(encoded)
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, value)
	}
	return out
}

// intersectSchemaValueSets reflects that repeated oneof validators all apply rather than widening them into a union.
func intersectSchemaValueSets(sets [][]any) []any {
	if len(sets) == 0 {
		return nil
	}
	out := append([]any(nil), sets[0]...)
	for _, set := range sets[1:] {
		allowed := map[string]struct{}{}
		for _, value := range set {
			encoded, err := json.Marshal(value)
			if err == nil {
				allowed[string(encoded)] = struct{}{}
			}
		}
		filtered := out[:0]
		for _, value := range out {
			encoded, err := json.Marshal(value)
			if err != nil {
				continue
			}
			if _, exists := allowed[string(encoded)]; exists {
				filtered = append(filtered, value)
			}
		}
		out = filtered
	}
	return out
}

// schemaDeclaredPrimitive gives well-known wire mappings precedence over a named type's implementation-facing underlying kind.
func schemaDeclaredPrimitive(schema map[string]any) string {
	declared, _ := schema["type"].(string)
	switch declared {
	case "string", "boolean", "integer", "number":
		return declared
	default:
		return ""
	}
}

// schemaPrimitiveKind unwraps named and pointer types to the JSON primitive used for validation-hint coercion.
func schemaPrimitiveKind(typeOf types.Type) string {
	if typeOf == nil {
		return ""
	}
	typeOf = types.Unalias(typeOf)
	if pointer, ok := typeOf.(*types.Pointer); ok {
		return schemaPrimitiveKind(pointer.Elem())
	}
	if named, ok := typeOf.(*types.Named); ok {
		return schemaPrimitiveKind(named.Underlying())
	}
	basic, ok := typeOf.(*types.Basic)
	if !ok {
		return ""
	}
	switch {
	case basic.Info()&types.IsBoolean != 0:
		return "boolean"
	case basic.Info()&types.IsInteger != 0:
		return "integer"
	case basic.Info()&types.IsFloat != 0:
		return "number"
	case basic.Info()&types.IsString != 0:
		return "string"
	default:
		return ""
	}
}

// validationPrimitiveLabel keeps diagnostics useful when a validator hint targets an object or another non-primitive contract.
func validationPrimitiveLabel(primitive string) string {
	if primitive == "" {
		return "a non-primitive schema"
	}
	return primitive
}

// coerceValidationEnum converts validator text into the primitive JSON values emitted by OpenAPI.
func coerceValidationEnum(values []string, primitive string, typeOf types.Type) ([]any, bool) {
	out := make([]any, 0, len(values))
	basic := underlyingBasicType(typeOf)
	for _, value := range values {
		switch primitive {
		case "string":
			out = append(out, value)
		case "boolean":
			if value != "true" && value != "false" {
				return nil, false
			}
			out = append(out, value == "true")
		case "integer":
			if basic == nil {
				return nil, false
			}
			if basic.Info()&types.IsUnsigned != 0 {
				parsed, err := strconv.ParseUint(value, 10, basicIntegerBits(basic))
				if err != nil {
					return nil, false
				}
				out = append(out, json.Number(strconv.FormatUint(parsed, 10)))
				continue
			}
			parsed, err := strconv.ParseInt(value, 10, basicIntegerBits(basic))
			if err != nil {
				return nil, false
			}
			out = append(out, json.Number(strconv.FormatInt(parsed, 10)))
		case "number":
			bitSize := 64
			if basic != nil && basic.Kind() == types.Float32 {
				bitSize = 32
			}
			parsed, err := strconv.ParseFloat(value, bitSize)
			if err != nil || math.IsInf(parsed, 0) || math.IsNaN(parsed) {
				return nil, false
			}
			out = append(out, parsed)
		default:
			return nil, false
		}
	}
	return out, true
}

// underlyingBasicType unwraps aliases, pointers, and named declarations for range-aware validator coercion.
func underlyingBasicType(typeOf types.Type) *types.Basic {
	if typeOf == nil {
		return nil
	}
	typeOf = types.Unalias(typeOf)
	if pointer, ok := typeOf.(*types.Pointer); ok {
		return underlyingBasicType(pointer.Elem())
	}
	if named, ok := typeOf.(*types.Named); ok {
		return underlyingBasicType(named.Underlying())
	}
	basic, _ := typeOf.(*types.Basic)
	return basic
}

// basicIntegerBits returns the representable width used by validator conversion while treating platform-sized values conservatively as 64-bit.
func basicIntegerBits(basic *types.Basic) int {
	if basic == nil {
		return 64
	}
	switch basic.Kind() {
	case types.Int8, types.Uint8:
		return 8
	case types.Int16, types.Uint16:
		return 16
	case types.Int32, types.Uint32:
		return 32
	default:
		return 64
	}
}

// schemaWithSiblings wraps references because OpenAPI 3.0 ignores ordinary schema keywords placed beside a Reference Object.
func schemaWithSiblings(schema map[string]any) map[string]any {
	if _, isReference := schema["$ref"]; isReference {
		return map[string]any{"allOf": []any{cloneSchemaMap(schema)}}
	}
	return cloneSchemaMap(schema)
}

// nullableSchema represents pointer nullability without placing ignored siblings next to an OpenAPI 3.0 reference.
func nullableSchema(schema map[string]any) map[string]any {
	out := schemaWithSiblings(schema)
	out["nullable"] = true
	return out
}

// embeddedStruct unwraps aliases, pointers, and named types only when their JSON fields can be promoted.
func embeddedStruct(typeOf types.Type) *types.Struct {
	typeOf = types.Unalias(typeOf)
	if pointer, ok := typeOf.(*types.Pointer); ok {
		typeOf = types.Unalias(pointer.Elem())
	}
	if named, ok := typeOf.(*types.Named); ok {
		typeOf = named.Underlying()
	}
	structure, _ := typeOf.(*types.Struct)
	return structure
}

// isByteType recognizes byte aliases where a standard codec signature specifically requires a byte slice.
func isByteType(typeOf types.Type) bool {
	basic, ok := types.Unalias(typeOf).Underlying().(*types.Basic)
	return ok && basic.Kind() == types.Byte
}

// isJSONByteElement excludes defined byte-like elements with custom codecs because their slice wire shape is no longer safely inferred as base64.
func isJSONByteElement(typeOf types.Type) bool {
	unaliased := types.Unalias(typeOf)
	basic, ok := unaliased.Underlying().(*types.Basic)
	if !ok || basic.Kind() != types.Byte {
		return false
	}
	named, ok := unaliased.(*types.Named)
	if !ok {
		return true
	}
	codec, _ := customCodecContract(named)
	return codec == ""
}

// isJSONMapKeyType accepts only key kinds that encoding/json can deterministically convert to object member names without runtime methods.
func isJSONMapKeyType(typeOf types.Type) bool {
	unaliased := types.Unalias(typeOf)
	if named, ok := unaliased.(*types.Named); ok {
		for _, candidate := range []types.Type{named, types.NewPointer(named)} {
			if methodSetHasMarshaler(candidate, "MarshalText") || methodSetHasUnmarshaler(candidate, "UnmarshalText") {
				return false
			}
		}
	}
	basic, ok := unaliased.Underlying().(*types.Basic)
	if !ok {
		return false
	}
	return basic.Info()&(types.IsString|types.IsInteger) != 0
}

// namedType unwraps aliases and pointers when callers need the semantic named contract behind a value.
func namedType(typeOf types.Type) *types.Named {
	if typeOf == nil {
		return nil
	}
	typeOf = types.Unalias(typeOf)
	if pointer, ok := typeOf.(*types.Pointer); ok {
		typeOf = types.Unalias(pointer.Elem())
	}
	named, _ := typeOf.(*types.Named)
	return named
}

// wellKnownNamedSchema keeps standard timestamp and UUID-like contracts concise instead of expanding implementation fields.
func wellKnownNamedSchema(named *types.Named) (map[string]any, bool) {
	if named == nil || named.Obj() == nil {
		return nil, false
	}
	object := named.Origin().Obj()
	packagePath := ""
	if object.Pkg() != nil {
		packagePath = object.Pkg().Path()
	}
	switch {
	case packagePath == "time" && object.Name() == "Time":
		return map[string]any{"type": "string", "format": "date-time"}, true
	case packagePath == "encoding/json" && object.Name() == "RawMessage":
		return map[string]any{}, true
	case packagePath == "encoding/json" && object.Name() == "Number":
		return map[string]any{"type": "number"}, true
	case knownUUIDType(packagePath, object.Name()):
		return map[string]any{"type": "string", "format": "uuid"}, true
	default:
		return nil, false
	}
}

// knownUUIDType restricts format inference to packages whose UUID JSON representation is a documented string contract.
func knownUUIDType(packagePath, typeName string) bool {
	if typeName != "UUID" {
		return false
	}
	switch packagePath {
	case "github.com/google/uuid", "github.com/gofrs/uuid", "github.com/gofrs/uuid/v5", "github.com/satori/go.uuid":
		return true
	default:
		return false
	}
}

// isWellKnownNamedType reports whether a named value intentionally remains inline instead of becoming a component.
func isWellKnownNamedType(named *types.Named) bool {
	_, ok := wellKnownNamedSchema(named)
	return ok
}

// enumValues treats exported constants of the exact declared type as the package's public wire vocabulary while excluding private sentinels.
func enumValues(named *types.Named) []any {
	if named == nil || named.Obj() == nil || named.Obj().Pkg() == nil || named.TypeArgs().Len() > 0 {
		return nil
	}
	scope := named.Obj().Pkg().Scope()
	values := make([]any, 0)
	seen := map[string]struct{}{}
	for _, name := range scope.Names() {
		constantObject, ok := scope.Lookup(name).(*types.Const)
		if !ok || !constantObject.Exported() || !types.Identical(constantObject.Type(), named) {
			continue
		}
		value, ok := jsonEnumValue(constantObject.Val())
		if !ok {
			continue
		}
		fingerprint, err := json.Marshal(value)
		if err != nil {
			continue
		}
		key := string(fingerprint)
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		values = append(values, value)
	}
	sort.Slice(values, func(i, j int) bool {
		left, _ := json.Marshal(values[i])
		right, _ := json.Marshal(values[j])
		return string(left) < string(right)
	})
	return values
}

// jsonEnumValue converts only exact JSON primitive constants so generated enum values cannot lose precision silently.
func jsonEnumValue(value constant.Value) (any, bool) {
	switch value.Kind() {
	case constant.Bool:
		return constant.BoolVal(value), true
	case constant.String:
		return constant.StringVal(value), true
	case constant.Int:
		return json.Number(value.ExactString()), true
	case constant.Float:
		floatValue, exact := constant.Float64Val(value)
		if !exact || math.IsInf(floatValue, 0) || math.IsNaN(floatValue) {
			return nil, false
		}
		return floatValue, true
	default:
		return nil, false
	}
}

// canonicalGoTypeIdentity uses full import paths so import aliases and same-named packages cannot collapse semantic contracts.
func canonicalGoTypeIdentity(typeOf types.Type) string {
	if typeOf == nil {
		return ""
	}
	switch typed := typeOf.(type) {
	case *types.Alias:
		return canonicalGoTypeIdentity(types.Unalias(typed))
	case *types.Named:
		return canonicalNamedIdentity(typed)
	case *types.Pointer:
		return "*" + canonicalGoTypeIdentity(typed.Elem())
	case *types.Slice:
		return "[]" + canonicalGoTypeIdentity(typed.Elem())
	case *types.Array:
		return "[" + strconv.FormatInt(typed.Len(), 10) + "]" + canonicalGoTypeIdentity(typed.Elem())
	case *types.Map:
		return "map[" + canonicalGoTypeIdentity(typed.Key()) + "]" + canonicalGoTypeIdentity(typed.Elem())
	default:
		return types.TypeString(typeOf, func(pkg *types.Package) string {
			if pkg == nil {
				return ""
			}
			return pkg.Path()
		})
	}
}

// canonicalNamedIdentity includes origin arguments so distinct generic instantiations receive distinct components.
func canonicalNamedIdentity(named *types.Named) string {
	if named == nil || named.Obj() == nil {
		return ""
	}
	origin := named.Origin()
	object := origin.Obj()
	prefix := object.Name()
	if object.Pkg() != nil {
		prefix = object.Pkg().Path() + "." + object.Name()
	}
	arguments := named.TypeArgs()
	if arguments == nil || arguments.Len() == 0 {
		return prefix
	}
	identities := make([]string, 0, arguments.Len())
	for index := 0; index < arguments.Len(); index++ {
		identities = append(identities, canonicalGoTypeIdentity(arguments.At(index)))
	}
	return prefix + "[" + strings.Join(identities, ",") + "]"
}

// readableGoType uses package names only for display while canonical identity remains import-path based.
func readableGoType(typeOf types.Type) string {
	if typeOf == nil {
		return ""
	}
	return types.TypeString(typeOf, func(pkg *types.Package) string {
		if pkg == nil {
			return ""
		}
		return pkg.Name()
	})
}

// readableNamedType preserves concrete generic arguments in human-facing schema metadata.
func readableNamedType(named *types.Named) string {
	return readableGoType(named)
}

// assignComponentNames resolves every reachable identity as one set so traversal order cannot affect collision suffixes.
func (r *typedSchemaRegistry) assignComponentNames(candidates map[string]*types.Named) {
	if r.componentNames == nil {
		r.componentNames = map[string]string{}
	}
	identities := make([]string, 0, len(candidates))
	baseNames := make(map[string]string, len(candidates))
	groups := map[string][]string{}
	for identity, named := range candidates {
		identities = append(identities, identity)
		base := componentCandidateName(named)
		baseNames[identity] = base
		key := strings.ToLower(base)
		groups[key] = append(groups[key], identity)
	}
	sort.Strings(identities)
	proposed := make(map[string]string, len(candidates))
	for _, identity := range identities {
		base := baseNames[identity]
		if len(groups[strings.ToLower(base)]) == 1 {
			proposed[identity] = base
			continue
		}
		proposed[identity] = base + "_" + shortTypeHash(identity)
	}
	for {
		finalGroups := map[string][]string{}
		for _, identity := range identities {
			key := strings.ToLower(proposed[identity])
			finalGroups[key] = append(finalGroups[key], identity)
		}
		changed := false
		for _, collisions := range finalGroups {
			if len(collisions) < 2 {
				continue
			}
			sort.Strings(collisions)
			for index, identity := range collisions {
				proposed[identity] = baseNames[identity] + "_" + shortTypeHash(identity) + "_" + strconv.Itoa(index+1)
			}
			changed = true
		}
		if !changed {
			break
		}
	}
	for _, identity := range identities {
		r.componentNames[identity] = proposed[identity]
	}
}

// componentName returns a preallocated readable name and uses an injective identity encoding for an unexpected contract outside the reachability prepass.
func (r *typedSchemaRegistry) componentName(named *types.Named) string {
	identity := canonicalNamedIdentity(named)
	if reserved := r.componentNames[identity]; reserved != "" {
		return reserved
	}
	return componentCandidateName(named) + "_" + hex.EncodeToString([]byte(identity))
}

// componentCandidateName combines sanitized package and type semantics before global collision analysis.
func componentCandidateName(named *types.Named) string {
	if named == nil || named.Obj() == nil {
		return "Contract"
	}
	object := named.Origin().Obj()
	return upperFirst(componentPackageLabel(object.Pkg())) + componentTypeLabel(named)
}

// componentPackageLabel sanitizes package names while retaining a compact semantic qualifier.
func componentPackageLabel(pkg *types.Package) string {
	if pkg == nil {
		return "local"
	}
	label := sanitizeComponentToken(pkg.Name())
	if label == "" {
		return "package"
	}
	return label
}

// componentTypeLabel distinguishes generic instantiations without embedding raw Go syntax in component names.
func componentTypeLabel(named *types.Named) string {
	if named == nil || named.Obj() == nil {
		return "Contract"
	}
	label := upperFirst(sanitizeComponentToken(named.Origin().Obj().Name()))
	if label == "" {
		label = "Contract"
	}
	arguments := named.TypeArgs()
	if arguments == nil || arguments.Len() == 0 {
		return label
	}
	argumentLabels := make([]string, 0, arguments.Len())
	for index := 0; index < arguments.Len(); index++ {
		argumentLabels = append(argumentLabels, readableTypeLabel(arguments.At(index)))
	}
	return label + "Of" + strings.Join(argumentLabels, "And")
}

// readableTypeLabel turns common container and named argument types into stable component-name fragments.
func readableTypeLabel(typeOf types.Type) string {
	switch typed := typeOf.(type) {
	case *types.Alias:
		return readableTypeLabel(types.Unalias(typed))
	case *types.Named:
		object := typed.Origin().Obj()
		packageLabel := upperFirst(componentPackageLabel(object.Pkg()))
		return packageLabel + componentTypeLabel(typed)
	case *types.Pointer:
		return "PointerTo" + readableTypeLabel(typed.Elem())
	case *types.Slice:
		return "ListOf" + readableTypeLabel(typed.Elem())
	case *types.Array:
		return "Array" + strconv.FormatInt(typed.Len(), 10) + "Of" + readableTypeLabel(typed.Elem())
	case *types.Map:
		return "MapOf" + readableTypeLabel(typed.Key()) + "To" + readableTypeLabel(typed.Elem())
	case *types.Basic:
		return sanitizeComponentToken(upperFirst(typed.Name()))
	default:
		label := sanitizeComponentToken(readableGoType(typeOf))
		if label == "" {
			return "Value"
		}
		return label
	}
}

// upperFirst capitalizes an ASCII-independent first rune without applying deprecated word-boundary transformations.
func upperFirst(value string) string {
	if value == "" {
		return ""
	}
	runes := []rune(value)
	runes[0] = unicode.ToUpper(runes[0])
	return string(runes)
}

// sanitizeComponentToken retains ASCII identifier characters accepted by common OpenAPI generators.
func sanitizeComponentToken(value string) string {
	var builder strings.Builder
	for _, current := range value {
		if current >= 'a' && current <= 'z' || current >= 'A' && current <= 'Z' || current >= '0' && current <= '9' || current == '_' {
			builder.WriteRune(current)
		}
	}
	out := builder.String()
	if out == "" {
		return ""
	}
	if out[0] >= '0' && out[0] <= '9' {
		return "Type" + out
	}
	return out
}

// shortTypeHash disambiguates canonical identities without insertion-sensitive traversal suffixes.
func shortTypeHash(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:6])
}
