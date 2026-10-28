package gcmeta

import (
	"fmt"
	"strconv"
	"strings"
)

type typeKind uint8

const (
	typeName typeKind = iota
	typeView
	typeArray
	typeFunc
	typeUnit
)

// typeNode is one parsed type text from a willc field. It keeps the shape the
// layout needs, not the source spelling.
type typeNode struct {
	kind   typeKind
	name   string
	elem   *typeNode
	length int
	args   []*typeNode
}

func parseTypeText(text string) (*typeNode, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil, fmt.Errorf("empty type text")
	}
	if text == "()" {
		return &typeNode{kind: typeUnit}, nil
	}
	if strings.HasPrefix(text, "[]") {
		elem, err := parseTypeText(text[2:])
		if err != nil {
			return nil, err
		}
		return &typeNode{kind: typeView, elem: elem}, nil
	}
	if strings.HasPrefix(text, "[") {
		end := strings.IndexByte(text, ']')
		if end < 0 {
			return nil, fmt.Errorf("unterminated array type %q", text)
		}
		n, err := strconv.Atoi(strings.TrimSpace(text[1:end]))
		if err != nil || n < 0 {
			return nil, fmt.Errorf("bad array length in %q", text)
		}
		elem, err := parseTypeText(text[end+1:])
		if err != nil {
			return nil, err
		}
		return &typeNode{kind: typeArray, elem: elem, length: n}, nil
	}
	if strings.HasPrefix(text, "func(") {
		return &typeNode{kind: typeFunc, name: text}, nil
	}
	if strings.HasPrefix(text, "ref ") || strings.HasPrefix(text, "mut ") {
		return &typeNode{kind: typeName, name: strings.TrimSpace(text)}, nil
	}
	if lt := strings.IndexByte(text, '<'); lt > 0 && strings.HasSuffix(text, ">") {
		args, err := splitArgs(text[lt+1 : len(text)-1])
		if err != nil {
			return nil, err
		}
		node := &typeNode{kind: typeName, name: text[:lt]}
		for _, arg := range args {
			child, err := parseTypeText(arg)
			if err != nil {
				return nil, err
			}
			node.args = append(node.args, child)
		}
		return node, nil
	}
	return &typeNode{kind: typeName, name: text}, nil
}

func splitArgs(text string) ([]string, error) {
	var out []string
	depth := 0
	start := 0
	for i := 0; i < len(text); i++ {
		switch text[i] {
		case '<', '[', '(':
			depth++
		case '>', ']', ')':
			depth--
		case ',':
			if depth == 0 {
				out = append(out, strings.TrimSpace(text[start:i]))
				start = i + 1
			}
		}
	}
	if depth != 0 {
		return nil, fmt.Errorf("unbalanced type arguments in %q", text)
	}
	out = append(out, strings.TrimSpace(text[start:]))
	return out, nil
}

func typeTextOf(node *typeNode) string {
	if node == nil {
		return ""
	}
	switch node.kind {
	case typeUnit:
		return "()"
	case typeView:
		return "[]" + typeTextOf(node.elem)
	case typeArray:
		return "[" + strconv.Itoa(node.length) + "]" + typeTextOf(node.elem)
	case typeFunc:
		return node.name
	}
	if len(node.args) == 0 {
		return node.name
	}
	args := make([]string, len(node.args))
	for i, arg := range node.args {
		args[i] = typeTextOf(arg)
	}
	return node.name + "<" + strings.Join(args, ",") + ">"
}
