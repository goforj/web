package webindex

import (
	"fmt"
	"go/ast"
	"path/filepath"
)

type routeScope struct {
	active          bool
	compositionFile string
	owners          map[string]struct{}
}

type scopedRouteGroup struct {
	Owners      []string
	Prefix      string
	Middlewares []string
}

func newRouteScope(root string, compositionPath string, parsed []*parsedFile) (routeScope, error) {
	if compositionPath == "" {
		return routeScope{}, nil
	}
	path := compositionPath
	if !filepath.IsAbs(path) {
		path = filepath.Join(root, path)
	}
	path = filepath.ToSlash(filepath.Clean(path))

	for _, pf := range parsed {
		if filepath.ToSlash(filepath.Clean(pf.Path)) != path {
			continue
		}
		scope := routeScope{
			active:          true,
			compositionFile: path,
			owners:          collectCompositionRouteOwners(pf),
		}
		return scope, nil
	}
	return routeScope{}, fmt.Errorf("route composition file not found: %s", compositionPath)
}

func (s routeScope) includesRoute(route discoveredRoute) bool {
	if !s.active {
		return true
	}
	if filepath.ToSlash(filepath.Clean(route.File)) == s.compositionFile {
		return true
	}
	if route.HandlerPackageHint == "" || route.HandlerReceiverHint == "" {
		return false
	}
	_, ok := s.owners[route.HandlerPackageHint+"."+route.HandlerReceiverHint]
	return ok
}

func (s routeScope) includesGroupFile(path string) bool {
	if !s.active {
		return true
	}
	return filepath.ToSlash(filepath.Clean(path)) == s.compositionFile
}

func collectCompositionRouteOwners(pf *parsedFile) map[string]struct{} {
	owners := map[string]struct{}{}
	for _, decl := range pf.File.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Body == nil {
			continue
		}
		switch fn.Name.Name {
		case "ProvideRoutes", "ProvideAppRoutes":
			for _, group := range returnedScopedRouteGroups(fn) {
				for _, owner := range group.Owners {
					owners[owner] = struct{}{}
				}
			}
		}
	}
	return owners
}

func returnedScopedRouteGroups(fn *ast.FuncDecl) []scopedRouteGroup {
	paramOwner := routeParamOwners(fn)
	routeVarOwners := map[string]map[string]struct{}{}
	groupVarGroups := map[string][]scopedRouteGroup{}
	returned := make([]scopedRouteGroup, 0)

	ast.Inspect(fn.Body, func(n ast.Node) bool {
		switch node := n.(type) {
		case *ast.AssignStmt:
			for i, lhs := range node.Lhs {
				ident, ok := lhs.(*ast.Ident)
				if !ok || ident.Name == "_" || i >= len(node.Rhs) {
					continue
				}
				mergeOwnerSet(routeVarOwners, ident.Name, routeOwnersFromNode(node.Rhs[i], paramOwner))
				if groups := routeGroupsFromExpr(node.Rhs[i], paramOwner, routeVarOwners, groupVarGroups); len(groups) > 0 {
					groupVarGroups[ident.Name] = append(groupVarGroups[ident.Name], groups...)
				}
			}
		case *ast.ReturnStmt:
			for _, result := range node.Results {
				returned = append(returned, routeGroupsFromExpr(result, paramOwner, routeVarOwners, groupVarGroups)...)
			}
		}
		return true
	})

	return returned
}

func routeGroupsFromExpr(expr ast.Expr, paramOwner map[string]string, routeVarOwners map[string]map[string]struct{}, groupVarGroups map[string][]scopedRouteGroup) []scopedRouteGroup {
	switch e := expr.(type) {
	case *ast.CallExpr:
		if group, ok := routeGroupFromCall(e, paramOwner, routeVarOwners); ok {
			return []scopedRouteGroup{group}
		}
		if fun, ok := e.Fun.(*ast.Ident); ok && fun.Name == "append" && len(e.Args) > 1 {
			groups := make([]scopedRouteGroup, 0, len(e.Args)-1)
			for _, arg := range e.Args[1:] {
				groups = append(groups, routeGroupsFromExpr(arg, paramOwner, routeVarOwners, groupVarGroups)...)
			}
			return groups
		}
	case *ast.CompositeLit:
		groups := make([]scopedRouteGroup, 0, len(e.Elts))
		for _, elt := range e.Elts {
			expr, ok := elt.(ast.Expr)
			if !ok {
				continue
			}
			groups = append(groups, routeGroupsFromExpr(expr, paramOwner, routeVarOwners, groupVarGroups)...)
		}
		return groups
	case *ast.Ident:
		return append([]scopedRouteGroup(nil), groupVarGroups[e.Name]...)
	}
	return nil
}

func routeGroupFromCall(call *ast.CallExpr, paramOwner map[string]string, routeVarOwners map[string]map[string]struct{}) (scopedRouteGroup, bool) {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok || len(call.Args) < 2 {
		return scopedRouteGroup{}, false
	}
	xid, ok := sel.X.(*ast.Ident)
	if !ok || (xid.Name != "http" && xid.Name != "web") || sel.Sel.Name != "NewRouteGroup" {
		return scopedRouteGroup{}, false
	}
	prefix := extractStringLiteral(call.Args[0])
	if prefix == "" {
		return scopedRouteGroup{}, false
	}
	return scopedRouteGroup{
		Owners:      routeOwnersForGroupArg(call.Args[1], paramOwner, routeVarOwners),
		Prefix:      prefix,
		Middlewares: middlewareExprs(call.Args[2:]),
	}, true
}

func routeParamOwners(fn *ast.FuncDecl) map[string]string {
	out := map[string]string{}
	if fn.Type == nil || fn.Type.Params == nil {
		return out
	}
	for _, p := range fn.Type.Params.List {
		t := typeNameFromExpr(p.Type)
		for _, name := range p.Names {
			if t != "" {
				out[name.Name] = t
			}
		}
	}
	return out
}

func routeOwnersForGroupArg(expr ast.Expr, paramOwner map[string]string, varOwners map[string]map[string]struct{}) []string {
	if ident, ok := expr.(*ast.Ident); ok {
		return ownerSetValues(varOwners[ident.Name])
	}
	return routeOwnersFromNode(expr, paramOwner)
}

func routeOwnersFromNode(node ast.Node, paramOwner map[string]string) []string {
	if node == nil {
		return nil
	}
	owners := map[string]struct{}{}
	ast.Inspect(node, func(n ast.Node) bool {
		if n == nil {
			return true
		}
		expr, ok := n.(ast.Expr)
		if !ok {
			return true
		}
		if owner := ownerFromRoutesArg(expr, paramOwner); owner != "" {
			owners[owner] = struct{}{}
		}
		return true
	})
	return ownerSetValues(owners)
}

func mergeOwnerSet(varOwners map[string]map[string]struct{}, name string, owners []string) {
	if len(owners) == 0 {
		return
	}
	set := varOwners[name]
	if set == nil {
		set = map[string]struct{}{}
		varOwners[name] = set
	}
	for _, owner := range owners {
		set[owner] = struct{}{}
	}
}

func ownerSetValues(owners map[string]struct{}) []string {
	if len(owners) == 0 {
		return nil
	}
	out := make([]string, 0, len(owners))
	for owner := range owners {
		out = append(out, owner)
	}
	return out
}
