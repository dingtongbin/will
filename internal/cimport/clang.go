package cimport

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os/exec"
	"runtime"
	"strings"

	"github.com/will-lang/will/internal/ast"
)

// clangNode is the subset of clang's JSON AST the C surface needs.
type clangNode struct {
	Kind string `json:"kind"`
	Name string `json:"name"`
	Type struct {
		QualType string `json:"qualType"`
	} `json:"type"`
	Inner []clangNode `json:"inner"`
}

// ParseClang asks clang for the declarations of one header. Macro expansion and
// conditional compilation are clang's job; this package only maps the resulting
// function prototypes to will types.
func ParseClang(clang, header string, includeDirs []string, target string) ([]Func, error) {
	args := []string{"-Xclang", "-ast-dump=json", "-fsyntax-only", "-x", "c"}
	for _, dir := range includeDirs {
		if dir != "" {
			args = append(args, "-I", dir)
		}
	}
	if target != "" {
		args = append(args, "--target="+target)
	}
	args = append(args, header)
	cmd := exec.Command(clang, args...)
	var out, errBuf bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errBuf
	if err := cmd.Run(); err != nil {
		message := strings.TrimSpace(errBuf.String())
		if message == "" {
			message = err.Error()
		}
		return nil, fmt.Errorf("%s", message)
	}
	var root clangNode
	if err := json.Unmarshal(out.Bytes(), &root); err != nil {
		return nil, fmt.Errorf("clang ast: %v", err)
	}
	var funcs []Func
	walkClang(root, func(n clangNode) {
		if n.Kind != "FunctionDecl" || n.Name == "" {
			return
		}
		fn, ok := clangFunc(n, target)
		if ok {
			funcs = append(funcs, fn)
		}
	})
	return funcs, nil
}

func walkClang(n clangNode, visit func(clangNode)) {
	visit(n)
	for _, child := range n.Inner {
		walkClang(child, visit)
	}
}

func clangFunc(n clangNode, target string) (Func, bool) {
	qual := strings.TrimSpace(n.Type.QualType)
	open := strings.IndexByte(qual, '(')
	retText := qual
	if open >= 0 {
		retText = qual[:open]
	}
	result, ok := clangType(strings.TrimSpace(retText), target)
	if !ok {
		return Func{}, false
	}
	fn := Func{Name: n.Name, Result: result}
	for _, child := range n.Inner {
		if child.Kind != "ParmVarDecl" {
			continue
		}
		param, ok := clangType(child.Type.QualType, target)
		if !ok {
			return Func{}, false
		}
		fn.Params = append(fn.Params, param)
	}
	if strings.Contains(qual, "...") {
		fn.Variadic = true
	}
	return fn, true
}

// clangType is the C-to-will table from the C interop volume. A pointer of any
// kind is raw; a by-value struct or union waits for the external type layer.
// targetIsWindows follows the target when one is given; the host is only a
// fallback for a header parsed without a target.
func targetIsWindows(target string) bool {
	if target != "" {
		return strings.Contains(strings.ToLower(target), "win")
	}
	return runtime.GOOS == "windows"
}

func clangType(text, target string) (*ast.Type, bool) {
	text = strings.TrimSpace(text)
	for _, q := range []string{"const ", "volatile ", "restrict ", "__restrict ", "_Nonnull ", "_Nullable "} {
		text = strings.ReplaceAll(text, q, "")
	}
	text = strings.TrimSpace(text)
	if text == "" {
		return nil, false
	}
	if strings.Contains(text, "*") || strings.Contains(text, "[") || strings.Contains(text, "(^") {
		return scalar("raw"), true
	}
	if strings.HasPrefix(text, "enum ") {
		return scalar("i32"), true
	}
	if strings.HasPrefix(text, "struct ") || strings.HasPrefix(text, "union ") {
		return nil, false
	}
	switch text {
	case "void":
		return unit(), true
	case "_Bool", "bool":
		return scalar("bool"), true
	case "char", "signed char":
		return scalar("i8"), true
	case "unsigned char":
		return scalar("u8"), true
	case "short", "short int", "signed short", "signed short int":
		return scalar("i16"), true
	case "unsigned short", "unsigned short int":
		return scalar("u16"), true
	case "int", "signed", "signed int":
		return scalar("i32"), true
	case "unsigned", "unsigned int":
		return scalar("u32"), true
	case "long", "long int", "signed long", "signed long int":
		if targetIsWindows(target) {
			return scalar("i32"), true
		}
		return scalar("i64"), true
	case "unsigned long", "unsigned long int":
		if targetIsWindows(target) {
			return scalar("u32"), true
		}
		return scalar("u64"), true
	case "long long", "long long int", "signed long long", "signed long long int":
		return scalar("i64"), true
	case "unsigned long long", "unsigned long long int":
		return scalar("u64"), true
	case "float":
		return scalar("f32"), true
	case "double":
		return scalar("f64"), true
	case "size_t":
		return scalar("usize"), true
	case "ssize_t", "ptrdiff_t", "intptr_t":
		return scalar("isize"), true
	case "uintptr_t":
		return scalar("usize"), true
	case "int8_t":
		return scalar("i8"), true
	case "uint8_t":
		return scalar("u8"), true
	case "int16_t":
		return scalar("i16"), true
	case "uint16_t":
		return scalar("u16"), true
	case "int32_t":
		return scalar("i32"), true
	case "uint32_t":
		return scalar("u32"), true
	case "int64_t":
		return scalar("i64"), true
	case "uint64_t":
		return scalar("u64"), true
	}
	return nil, false
}
