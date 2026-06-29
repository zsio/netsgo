package client

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestUnifiedClientRuntimeDoesNotCallProxyRequestFromTunnelSpec(t *testing.T) {
	dirEntries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("read client package dir: %v", err)
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
			pos := fset.Position(fn.Pos())
			if fn.Name.Name == "proxyRequestFromTunnelSpec" {
				t.Fatalf("proxyRequestFromTunnelSpec must be deleted, not just left unused: %s", pos)
			}
			if functionConvertsTunnelSpecToProxyNewRequest(fn) {
				t.Fatalf("unified client runtime must not keep an equivalent TunnelSpec -> ProxyNewRequest downgrade helper: %s", pos)
			}
		}
		ast.Inspect(file, func(node ast.Node) bool {
			call, ok := node.(*ast.CallExpr)
			if !ok {
				return true
			}
			switch fun := call.Fun.(type) {
			case *ast.Ident:
				if fun.Name == "proxyRequestFromTunnelSpec" {
					pos := fset.Position(call.Pos())
					t.Fatalf("unified client runtime must not call proxyRequestFromTunnelSpec: %s", pos)
				}
			case *ast.SelectorExpr:
				if fun.Sel != nil && fun.Sel.Name == "proxyRequestFromTunnelSpec" {
					pos := fset.Position(call.Pos())
					t.Fatalf("unified client runtime must not call proxyRequestFromTunnelSpec (including method form): %s", pos)
				}
			}
			return true
		})
	}
}

func TestUnifiedClientRuntimeDefinesFixedTargetStore(t *testing.T) {
	dirEntries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("read client package dir: %v", err)
	}
	fset := token.NewFileSet()
	foundRuntimeType := false
	foundRuntimeStore := false
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
		ast.Inspect(file, func(node ast.Node) bool {
			switch n := node.(type) {
			case *ast.TypeSpec:
				if n.Name != nil && n.Name.Name == "fixedServiceTargetRuntime" {
					foundRuntimeType = true
				}
			case *ast.Field:
				for _, fieldName := range n.Names {
					if fieldName.Name == "fixedTargetRuntimes" {
						foundRuntimeStore = true
					}
				}
			}
			return true
		})
	}
	if !foundRuntimeType {
		t.Fatal("unified fixed TCP/UDP/HTTP targets require a fixedServiceTargetRuntime type; do not keep using ProxyNewRequest")
	}
	if !foundRuntimeStore {
		t.Fatal("Client must own a fixedTargetRuntimes store keyed by tunnel_id for unified fixed target runtime")
	}
}

func TestClientCleanupClearsFixedTargetRuntimes(t *testing.T) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "client.go", nil, 0)
	if err != nil {
		t.Fatalf("parse client.go: %v", err)
	}
	var cleanupFn *ast.FuncDecl
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Name == nil || fn.Name.Name != "cleanup" {
			continue
		}
		if fn.Recv == nil || len(fn.Recv.List) == 0 {
			continue
		}
		star, ok := fn.Recv.List[0].Type.(*ast.StarExpr)
		if !ok {
			continue
		}
		ident, ok := star.X.(*ast.Ident)
		if !ok || ident.Name != "Client" {
			continue
		}
		cleanupFn = fn
		break
	}
	if cleanupFn == nil {
		t.Fatal("Client.cleanup method not found")
	}
	found := false
	ast.Inspect(cleanupFn, func(node ast.Node) bool {
		sel, ok := node.(*ast.SelectorExpr)
		if !ok || sel.Sel == nil {
			return true
		}
		if sel.Sel.Name == "fixedTargetRuntimes" {
			found = true
			return false
		}
		return true
	})
	if !found {
		t.Fatal("Client.cleanup must clear fixedTargetRuntimes store; current cleanup does not reference fixedTargetRuntimes")
	}
}

func TestClientHandleStreamUsesFixedTargetRuntimes(t *testing.T) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "client.go", nil, 0)
	if err != nil {
		t.Fatalf("parse client.go: %v", err)
	}
	var handleStreamFn *ast.FuncDecl
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Name == nil || fn.Name.Name != "handleStream" {
			continue
		}
		if !receiverIsClient(fn) {
			continue
		}
		handleStreamFn = fn
		break
	}
	if handleStreamFn == nil {
		t.Fatal("Client.handleStream method not found")
	}

	methods := clientMethods(file)
	if !clientMethodReferencesSelector(handleStreamFn, methods, "fixedTargetRuntimes") {
		t.Fatal("Client.handleStream must dispatch fixed TCP/UDP/HTTP target streams via fixedTargetRuntimes before falling back to legacy c.proxies")
	}
}

func TestClientHandleTunnelUnprovisionUsesFixedTargetRuntimes(t *testing.T) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "unified_tunnel.go", nil, 0)
	if err != nil {
		t.Fatalf("parse unified_tunnel.go: %v", err)
	}
	methods := clientMethods(file)
	handleUnprovision := methods["handleTunnelUnprovision"]
	if handleUnprovision == nil {
		t.Fatal("Client.handleTunnelUnprovision method not found")
	}
	if clientMethodReferencesSelector(handleUnprovision, methods, "fixedTargetRuntimes") {
		return
	}
	t.Fatal("Client.handleTunnelUnprovision must delete fixedTargetRuntimes by tunnel_id/revision; deleting only SOCKS5 targets and legacy c.proxies is insufficient")
}

func functionConvertsTunnelSpecToProxyNewRequest(fn *ast.FuncDecl) bool {
	if fn == nil || fn.Type == nil || fn.Type.Params == nil || fn.Type.Results == nil {
		return false
	}
	hasTunnelSpecParam := false
	for _, field := range fn.Type.Params.List {
		if exprIsProtocolTypeOrPointerTo(field.Type, "TunnelSpec") {
			hasTunnelSpecParam = true
			break
		}
	}
	if !hasTunnelSpecParam {
		return false
	}
	for _, field := range fn.Type.Results.List {
		if exprIsProtocolTypeOrPointerTo(field.Type, "ProxyNewRequest") {
			return true
		}
	}
	return false
}

func receiverIsClient(fn *ast.FuncDecl) bool {
	if fn.Recv == nil || len(fn.Recv.List) == 0 {
		return false
	}
	recvType := fn.Recv.List[0].Type
	if star, ok := recvType.(*ast.StarExpr); ok {
		recvType = star.X
	}
	ident, ok := recvType.(*ast.Ident)
	return ok && ident.Name == "Client"
}

func clientMethods(file *ast.File) map[string]*ast.FuncDecl {
	methods := make(map[string]*ast.FuncDecl)
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Name == nil || !receiverIsClient(fn) {
			continue
		}
		methods[fn.Name.Name] = fn
	}
	return methods
}

func clientMethodReferencesSelector(fn *ast.FuncDecl, methods map[string]*ast.FuncDecl, selectorName string) bool {
	visited := make(map[string]bool)
	var visit func(*ast.FuncDecl) bool
	visit = func(current *ast.FuncDecl) bool {
		if current == nil || current.Name == nil {
			return false
		}
		if visited[current.Name.Name] {
			return false
		}
		visited[current.Name.Name] = true
		found := false
		ast.Inspect(current, func(node ast.Node) bool {
			if found {
				return false
			}
			switch n := node.(type) {
			case *ast.SelectorExpr:
				if n.Sel != nil && n.Sel.Name == selectorName {
					found = true
					return false
				}
			case *ast.CallExpr:
				methodName, ok := clientMethodCallName(n)
				if ok && visit(methods[methodName]) {
					found = true
					return false
				}
			}
			return true
		})
		return found
	}
	return visit(fn)
}

func clientMethodCallName(call *ast.CallExpr) (string, bool) {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok || sel.Sel == nil {
		return "", false
	}
	recv, ok := sel.X.(*ast.Ident)
	if !ok || recv.Name != "c" {
		return "", false
	}
	return sel.Sel.Name, true
}

func exprIsProtocolTypeOrPointerTo(expr ast.Expr, typeName string) bool {
	if exprIsProtocolType(expr, typeName) {
		return true
	}
	if star, ok := expr.(*ast.StarExpr); ok {
		return exprIsProtocolType(star.X, typeName)
	}
	return false
}

func exprIsProtocolType(expr ast.Expr, typeName string) bool {
	sel, ok := expr.(*ast.SelectorExpr)
	if !ok || sel.Sel == nil || sel.Sel.Name != typeName {
		return false
	}
	pkg, ok := sel.X.(*ast.Ident)
	return ok && pkg.Name == "protocol"
}
