package server

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestUnifiedServerExposeRuntimeDoesNotReadStoredProxyNewRequest(t *testing.T) {
	fset := token.NewFileSet()
	// Curated list of runtime/reconcile files that must derive server-expose
	// runtime config from stored endpoint/spec fields, not from the embedded
	// StoredTunnel.ProxyNewRequest. Auto-scanning every server file would
	// produce too many false positives from the legacy runtime
	// (proxy.go, tunnel_manager.go, tunnel_ready.go, tunnel_registry.go,
	// control_loop.go, tunnel_api.go, store.go, tunnel_restore.go) which
	// still legitimately operates on the legacy ProxyNewRequest shape.
	// When a new runtime/reconcile file is added to the unified path, it
	// MUST be appended here; leaving it off silently bypasses the guard.
	files := []string{
		"server_expose_unified.go",
		"unified_tunnel_reconcile.go",
		"unified_tunnel_runtime.go",
		"unified_tunnel_api.go",
	}
	disallowedStoredFlatFields := map[string]bool{
		"Type":       true,
		"LocalIP":    true,
		"LocalPort":  true,
		"RemotePort": true,
		"BindIP":     true,
		"Domain":     true,
	}
	var violations []string

	for _, path := range files {
		file, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", path, err)
		}

		ast.Inspect(file, func(n ast.Node) bool {
			sel, ok := n.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			if sel.Sel.Name == "ProxyNewRequest" {
				if pkg, ok := sel.X.(*ast.Ident); ok && pkg.Name == "protocol" {
					return true
				}
				violations = append(violations, fset.Position(sel.Pos()).String()+" in "+path)
				return true
			}
			if path == "server_expose_unified.go" && disallowedStoredFlatFields[sel.Sel.Name] && serverSelectorIsInsideFunction(fset, file, sel, "serverExposeRuntimeProxyRequest") {
				if ident, ok := sel.X.(*ast.Ident); ok && ident.Name == "stored" {
					violations = append(violations, fset.Position(sel.Pos()).String()+" promoted flat field "+sel.Sel.Name+" in "+path)
				}
			}
			return true
		})
	}

	if len(violations) > 0 {
		t.Fatalf("unified server-expose runtime/reconcile must derive runtime config from stored endpoint/spec fields, not StoredTunnel.ProxyNewRequest or its promoted flat fields; found %d violation(s):\n%s", len(violations), strings.Join(violations, "\n"))
	}
}

func serverSelectorIsInsideFunction(fset *token.FileSet, file *ast.File, sel *ast.SelectorExpr, name string) bool {
	if file == nil || sel == nil {
		return false
	}
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Name == nil || fn.Name.Name != name || fn.Body == nil {
			continue
		}
		return sel.Pos() >= fn.Body.Pos() && sel.End() <= fn.Body.End()
	}
	return false
}

func TestUnifiedServerRuntimeDoesNotDefineTunnelSpecToProxyNewRequestHelper(t *testing.T) {
	dirEntries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("read server package dir: %v", err)
	}
	fset := token.NewFileSet()
	for _, entry := range dirEntries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		path := filepath.Join(".", name)
		file, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", path, err)
		}
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Name == nil {
				continue
			}
			if serverFuncDowngradesTunnelSpecToProxyNewRequest(fn) {
				pos := fset.Position(fn.Pos())
				t.Fatalf("server runtime must not define a TunnelSpec -> ProxyNewRequest downgrade helper (server-side equivalent of proxyRequestFromTunnelSpec): %s", pos)
			}
		}
	}
}

func serverFuncDowngradesTunnelSpecToProxyNewRequest(fn *ast.FuncDecl) bool {
	if fn == nil || fn.Type == nil || fn.Type.Params == nil || fn.Type.Results == nil {
		return false
	}
	hasSpecParam := false
	for _, field := range fn.Type.Params.List {
		if serverExprIsProtocolTypeOrPointerTo(field.Type, "TunnelSpec") {
			hasSpecParam = true
			break
		}
	}
	if !hasSpecParam {
		return false
	}
	for _, field := range fn.Type.Results.List {
		if serverExprIsProtocolTypeOrPointerTo(field.Type, "ProxyNewRequest") {
			return true
		}
	}
	return false
}

func TestUnifiedServerRuntimeOnlyAllowsNamedServerExposeProxyRequestAdapter(t *testing.T) {
	// This is a signature-level boundary guard; body-level runtime source checks
	// live in TestUnifiedServerExposeRuntimeDoesNotReadStoredProxyNewRequest.
	allowedAdapters := map[string]struct{}{
		"serverExposeRuntimeProxyRequest": {},
	}
	dirEntries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("read server package dir: %v", err)
	}
	fset := token.NewFileSet()
	for _, entry := range dirEntries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		path := filepath.Join(".", name)
		file, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", path, err)
		}
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Name == nil {
				continue
			}
			if !serverFuncAdaptsStoredTunnelToProxyNewRequest(fn) {
				continue
			}
			if _, ok := allowedAdapters[fn.Name.Name]; !ok {
				t.Fatalf("server runtime must keep StoredTunnel -> ProxyNewRequest adaptation at an explicit allowlisted boundary, found %s at %s; update allowedAdapters here if this is an intentional rename or split", fn.Name.Name, fset.Position(fn.Pos()))
			}
		}
	}
}

func serverFuncAdaptsStoredTunnelToProxyNewRequest(fn *ast.FuncDecl) bool {
	if fn == nil || fn.Type == nil || fn.Type.Params == nil || fn.Type.Results == nil {
		return false
	}
	hasStoredTunnelParam := false
	for _, field := range fn.Type.Params.List {
		if serverExprIsIdentTypeOrPointerTo(field.Type, "StoredTunnel") {
			hasStoredTunnelParam = true
			break
		}
	}
	if !hasStoredTunnelParam {
		return false
	}
	for _, field := range fn.Type.Results.List {
		if serverExprIsProtocolTypeOrPointerTo(field.Type, "ProxyNewRequest") {
			return true
		}
	}
	return false
}

func serverExprIsIdentTypeOrPointerTo(expr ast.Expr, typeName string) bool {
	if ident, ok := expr.(*ast.Ident); ok && ident.Name == typeName {
		return true
	}
	if star, ok := expr.(*ast.StarExpr); ok {
		return serverExprIsIdentTypeOrPointerTo(star.X, typeName)
	}
	return false
}

func serverExprIsProtocolTypeOrPointerTo(expr ast.Expr, typeName string) bool {
	if serverExprIsProtocolType(expr, typeName) {
		return true
	}
	if star, ok := expr.(*ast.StarExpr); ok {
		return serverExprIsProtocolType(star.X, typeName)
	}
	return false
}

func serverExprIsProtocolType(expr ast.Expr, typeName string) bool {
	sel, ok := expr.(*ast.SelectorExpr)
	if !ok || sel.Sel == nil || sel.Sel.Name != typeName {
		return false
	}
	pkg, ok := sel.X.(*ast.Ident)
	return ok && pkg.Name == "protocol"
}
